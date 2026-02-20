package router

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/exec"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/strct-org/strct-agent/internal/config"
)

type Config struct {
	DeviceID   string
	BackendURL string
}

type PortRule struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	Port     int    `json:"port"`
	DeviceIP string `json:"device_ip"`
	Protocol string `json:"protocol"` // TCP, UDP, BOTH
}

type RouterConfig struct {
	SSID            string     `json:"ssid"`
	Password        string     `json:"password"`
	SecurityMode    string     `json:"security_mode"`
	IsHidden        bool       `json:"is_hidden"`
	Frequency       string     `json:"frequency"`
	TxPower         string     `json:"tx_power"`
	DNSProvider     string     `json:"dns_provider"`
	FirewallEnabled bool       `json:"firewall_enabled"`
	GuestNetwork    bool       `json:"guest_network"`
	PortRules       []PortRule `json:"port_rules"`
}

type ConnectedDevice struct {
	ID      string `json:"id"`
	IP      string `json:"ip"`
	MAC     string `json:"mac"`
	Name    string `json:"name"` // Hostname if available
	Blocked bool   `json:"blocked"`
	Limited bool   `json:"limited"` // Bandwidth limited
}

type RouterController struct {
	Config      Config
	State       RouterConfig
	Devices     []ConnectedDevice
	mu          sync.RWMutex
	blockedMACs map[string]bool
}

func New(cfg Config) *RouterController {
	initialState := RouterConfig{
		SSID:            "OrangePi_AP",
		Password:        "orange123",
		SecurityMode:    "WPA3",
		Frequency:       "5GHz",
		TxPower:         "20",
		DNSProvider:     "google",
		FirewallEnabled: true,
		PortRules:       []PortRule{},
	}

	return &RouterController{
		Config:      cfg,
		State:       initialState,
		Devices:     []ConnectedDevice{},
		blockedMACs: make(map[string]bool),
	}
}
func NewFromConfig(cfg *config.Config) *RouterController {
	return New(Config{
		DeviceID:   cfg.DeviceID,
		BackendURL: cfg.EffectiveBackendURL(),
	})
}

func (rc *RouterController) RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/router/devices", rc.HandleGetDevices)
	mux.HandleFunc("POST /api/router/block", rc.HandleBlockDevice)
	mux.HandleFunc("GET /api/router/config", rc.HandleGetConfig)
	mux.HandleFunc("POST /api/router/config", rc.HandleSetConfig)
}

func (r *RouterController) Start(ctx context.Context) error {
	slog.Info("router: starting")

	go r.applySystemConfig()

	deviceTicker := time.NewTicker(10 * time.Second)
	defer deviceTicker.Stop()

	go func() {
		for {
			select {
			case <-ctx.Done():
				return
			case <-deviceTicker.C:
				r.scanDevices()
			}
		}
	}()

	return nil
}

func (rc *RouterController) HandleGetConfig(w http.ResponseWriter, req *http.Request) {
	rc.mu.RLock()
	defer rc.mu.RUnlock()

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(rc.State)
}

// !TODO: HandleSetConfig applies config on every POST, no validation
func (rc *RouterController) HandleSetConfig(w http.ResponseWriter, req *http.Request) {
	var newConfig RouterConfig
	if err := json.NewDecoder(req.Body).Decode(&newConfig); err != nil {
		http.Error(w, "Invalid JSON", http.StatusBadRequest)
		return
	}

	rc.mu.Lock()
	rc.State = newConfig
	rc.mu.Unlock()

	// Apply changes asynchronously to not block the UI
	go rc.applySystemConfig()

	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(map[string]string{"status": "applying_changes"})
}

// HandleGetDevices returns the list of connected clients via ARP
func (rc *RouterController) HandleGetDevices(w http.ResponseWriter, req *http.Request) {
	rc.mu.RLock()
	defer rc.mu.RUnlock()

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(rc.Devices)
}

// HandleBlockDevice toggles internet access for a specific MAC
func (rc *RouterController) HandleBlockDevice(w http.ResponseWriter, req *http.Request) {
	type BlockRequest struct {
		MAC   string `json:"mac"`
		Block bool   `json:"block"`
	}

	var payload BlockRequest
	if err := json.NewDecoder(req.Body).Decode(&payload); err != nil {
		http.Error(w, "Invalid JSON", http.StatusBadRequest)
		return
	}

	rc.mu.Lock()
	if payload.Block {
		rc.blockedMACs[payload.MAC] = true
		// iptables -A INPUT -m mac --mac-source XX:XX -j DROP
		exec.Command("iptables", "-A", "INPUT", "-m", "mac", "--mac-source", payload.MAC, "-j", "DROP").Run()
		exec.Command("iptables", "-A", "FORWARD", "-m", "mac", "--mac-source", payload.MAC, "-j", "DROP").Run()
	} else {
		delete(rc.blockedMACs, payload.MAC)
		// iptables -D ... (Delete rule)
		exec.Command("iptables", "-D", "INPUT", "-m", "mac", "--mac-source", payload.MAC, "-j", "DROP").Run()
		exec.Command("iptables", "-D", "FORWARD", "-m", "mac", "--mac-source", payload.MAC, "-j", "DROP").Run()
	}
	rc.mu.Unlock()

	// Refresh device list to update status
	go rc.scanDevices()

	w.WriteHeader(http.StatusOK)
}

func (rc *RouterController) applySystemConfig() {
	rc.mu.RLock()
	cfg := rc.State
	rc.mu.RUnlock()

	slog.Info("router: applying system configuration", "config", cfg)

	var nameserver string
	switch cfg.DNSProvider {
	case "cloudflare":
		nameserver = "1.1.1.1"
	case "google":
		nameserver = "8.8.8.8"
	default:
		nameserver = "1.1.1.1"
	}

	//!TODO: Use executil inkection of the exec.command
	// Write /etc/resolv.conf
	dnsContent := fmt.Sprintf("nameserver %s\n", nameserver)
	os.WriteFile("/etc/resolv.conf", []byte(dnsContent), 0644)

	// 2. Wi-Fi Power (Boosting)
	// iwconfig wlan0 txpower 30
	exec.Command("iwconfig", "wlan0", "txpower", cfg.TxPower).Run()

	// 3. Port Forwarding (Clean up old rules first - simplified)
	exec.Command("iptables", "-t", "nat", "-F", "PREROUTING").Run()

	for _, rule := range cfg.PortRules {
		// iptables -t nat -A PREROUTING -p tcp --dport 80 -j DNAT --to-destination 192.168.1.50:80
		protocol := strings.ToLower(rule.Protocol)
		if protocol == "both" {
			rc.addNatRule("tcp", rule.Port, rule.DeviceIP)
			rc.addNatRule("udp", rule.Port, rule.DeviceIP)
		} else {
			rc.addNatRule(protocol, rule.Port, rule.DeviceIP)
		}
	}

	// 4. Update Hostapd (SSID/Password)
	// In a real scenario, you would use a template engine here.
	// For this example, we log it, as restarting hostapd drops the connection
	// which stops the response from reaching the client.
	slog.Info("router: updated Wi-Fi settings", "ssid", cfg.SSID, "hidden", cfg.IsHidden)

	// Example of how you would restart hostapd:
	// exec.Command("systemctl", "restart", "hostapd").Run()
}

func (rc *RouterController) addNatRule(proto string, port int, destIP string) {
	dest := fmt.Sprintf("%s:%d", destIP, port)
	portStr := fmt.Sprintf("%d", port)
	exec.Command("iptables", "-t", "nat", "-A", "PREROUTING", "-p", proto, "--dport", portStr, "-j", "DNAT", "--to-destination", dest).Run()
	// Allow through firewall
	exec.Command("iptables", "-A", "FORWARD", "-p", proto, "-d", destIP, "--dport", portStr, "-j", "ACCEPT").Run()
}

func (rc *RouterController) scanDevices() {
	// Run `arp -a`
	out, err := exec.Command("arp", "-a").Output()
	if err != nil {
		slog.Error("router: ARP scan failed", "err", err)
		return
	}

	// Parse Output
	// Example: ? (192.168.1.15) at a1:b2:c3:d4:e5:f6 [ether] on wlan0
	scanner := bufio.NewScanner(bytes.NewReader(out))
	var detected []ConnectedDevice

	// Regex to extract IP and MAC
	re := regexp.MustCompile(`\((.*?)\) at ([0-9a-f:]{17})`)

	rc.mu.RLock()
	blockedList := rc.blockedMACs
	rc.mu.RUnlock()

	for scanner.Scan() {
		line := scanner.Text()
		matches := re.FindStringSubmatch(line)
		if len(matches) == 3 {
			ip := matches[1]
			mac := matches[2]

			// Filter out incomplete entries
			if mac == "<incomplete>" {
				continue
			}

			// Check if blocked
			isBlocked := blockedList[mac]

			device := ConnectedDevice{
				ID:      mac, // Use MAC as ID
				IP:      ip,
				MAC:     mac,
				Name:    "Unknown Device", // Could do a reverse DNS lookup here
				Blocked: isBlocked,
				Limited: false, // Would check `tc` settings here
			}
			detected = append(detected, device)
		}
	}

	rc.mu.Lock()
	rc.Devices = detected
	rc.mu.Unlock()

	// Optionally push to backend
	go rc.reportDevicesToBackend(detected)
}

func (rc *RouterController) reportDevicesToBackend(devices []ConnectedDevice) {
	// Logic similar to monitor.reportToBackend
	// POST /api/v1/device/agent/{id}/connected_devices

	url := fmt.Sprintf("%s/api/v1/device/agent/%s/connected_devices", rc.Config.BackendURL, rc.Config.DeviceID)
	payload, _ := json.Marshal(devices)

	req, _ := http.NewRequest("POST", url, bytes.NewBuffer(payload))
	req.Header.Set("Content-Type", "application/json")

	//!TODO: Use one http.Client into the RouterController struct instead of creating a new one for each request
	client := &http.Client{Timeout: 5 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		slog.Error("router: failed to report devices to backend", "err", err)
		return
	}
	defer resp.Body.Close()

}
