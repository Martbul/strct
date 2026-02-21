// Package wifi manages the Orange Pi's wireless network mode.
//
// Responsibilities (this package only):
//   - Creating the WiFi AP via hostapd
//   - DHCP + DNS via dnsmasq
//   - NAT/IP forwarding via iptables
//   - Extender mode via ap+sta concurrent radio
//
// NOT in this package:
//   - VPN (see internal/features/vpn)
//   - Ad blocking (see internal/features/adblock)
//
// Both vpn and adblock read wifi.Status to know which subnet/interface
// is active, and apply themselves on top independently.
package wifi

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/strct-org/strct-agent/internal/config"
	"github.com/strct-org/strct-agent/internal/platform/executil"
)

// ─── Types ────────────────────────────────────────────────────────────────────

type Mode string

const (
	ModeOff      Mode = "off"
	ModeRouter   Mode = "router"   // eth0 WAN → wlan0 AP + NAT + DHCP
	ModeExtender Mode = "extender" // wlan0 client → wlan0_ap virtual AP
)

type RouterConfig struct {
	SSID       string `json:"ssid"`
	Password   string `json:"password"`
	Band       string `json:"band"`        // "2.4GHz" | "5GHz"
	Channel    int    `json:"channel"`     // 1/6/11 for 2.4GHz; 36/40/44/48 for 5GHz
	MaxClients int    `json:"max_clients"` // hostapd: max_num_sta
	SubnetBase string `json:"subnet_base"` // e.g. "192.168.100" → gateway .1, DHCP .50-.150
	DNSProvider string `json:"dns_provider"` // cloudflare|google|adguard|quad9
}

type ExtenderConfig struct {
	UpstreamSSID     string `json:"upstream_ssid"`
	UpstreamPassword string `json:"upstream_password"`
	ExtenderSSID     string `json:"extender_ssid"`
	ExtenderPassword string `json:"extender_password"`
	ExtenderBand     string `json:"extender_band"` // must match upstream band
	UseSecondRadio   bool   `json:"use_second_radio"` // use wlan1 instead of virtual wlan0_ap
}

type WiFiConfig struct {
	Mode     Mode           `json:"mode"`
	Router   RouterConfig   `json:"router"`
	Extender ExtenderConfig `json:"extender"`
}

// Status is the shared read-only view that sibling packages (vpn, adblock)
// use to know what's currently active. Get it via Service.Status().
type Status struct {
	Mode         Mode   `json:"mode"`
	Active       bool   `json:"active"`
	SSID         string `json:"ssid,omitempty"`
	APInterface  string `json:"ap_interface,omitempty"`  // "wlan0" or "wlan0_ap" or "wlan1"
	SubnetBase   string `json:"subnet_base,omitempty"`   // e.g. "192.168.100"
	GatewayIP    string `json:"gateway_ip,omitempty"`    // e.g. "192.168.100.1"
	ConnectedIPs int    `json:"connected_ips"`
	UpstreamSSID string `json:"upstream_ssid,omitempty"` // extender mode only
	Error        string `json:"error,omitempty"`
}

// ─── Service ──────────────────────────────────────────────────────────────────

type WiFi struct {
	cfg    config.Config
	state  WiFiConfig
	status Status
	mu     sync.RWMutex
	cmd    executil.Runner
}

func New(cfg config.Config, cmd executil.Runner) *WiFi {
	return &WiFi{
		cfg: cfg,
		cmd: cmd,
		state: WiFiConfig{
			Mode: ModeOff,
			Router: RouterConfig{
				SSID:        "StrctNet",
				Password:    "changeme123",
				Band:        "5GHz",
				Channel:     36,
				MaxClients:  20,
				SubnetBase:  "192.168.100",
				DNSProvider: "cloudflare",
			},
			Extender: ExtenderConfig{
				ExtenderSSID:     "StrctNet-Ext",
				ExtenderPassword: "changeme123",
				ExtenderBand:     "5GHz",
				UseSecondRadio:   false,
			},
		},
	}
}

func NewFromConfig(cfg *config.Config) *WiFi {
	return New(*cfg, executil.Real{})
}

// Status returns a snapshot of the current WiFi state.
// Called by vpn.Service and adblock.Service to know what subnet/interface is active.
func (s *WiFi) Status() Status {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.status
}

func (s *WiFi) RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/wifi/config",  s.handleGetConfig)
	mux.HandleFunc("POST /api/wifi/config", s.handleSetConfig)
	mux.HandleFunc("GET /api/wifi/status",  s.handleGetStatus)
	mux.HandleFunc("GET /api/wifi/scan",    s.handleScanNetworks)
	mux.HandleFunc("POST /api/wifi/stop",   s.handleStop)
}

func (s *WiFi) Start(ctx context.Context) error {
	slog.Info("wifi: service started")

	go func() {
		ticker := time.NewTicker(30 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				s.teardown()
				return
			case <-ticker.C:
				s.refreshStatus()
			}
		}
	}()

	return nil
}

// ─── HTTP handlers ────────────────────────────────────────────────────────────

func (s *WiFi) handleGetConfig(w http.ResponseWriter, r *http.Request) {
	s.mu.RLock()
	state := s.state
	s.mu.RUnlock()
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(state)
}

func (s *WiFi) handleSetConfig(w http.ResponseWriter, r *http.Request) {
	var req WiFiConfig
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid JSON", http.StatusBadRequest)
		return
	}
	if err := validateConfig(req); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	s.mu.Lock()
	s.state = req
	s.mu.Unlock()

	go func() {
		if err := s.apply(); err != nil {
			slog.Error("wifi: apply failed", "err", err)
			s.mu.Lock()
			s.status.Error = err.Error()
			s.mu.Unlock()
		}
	}()

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"status": "applying"})
}

func (s *WiFi) handleGetStatus(w http.ResponseWriter, r *http.Request) {
	s.mu.RLock()
	st := s.status
	s.mu.RUnlock()
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(st)
}

func (s *WiFi) handleScanNetworks(w http.ResponseWriter, r *http.Request) {
	out, err := s.cmd.CombinedOutput("iw", "dev", "wlan0", "scan")
	if err != nil {
		http.Error(w, "scan failed: "+err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(parseIWScan(out))
}

func (s *WiFi) handleStop(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	s.state.Mode = ModeOff
	s.mu.Unlock()
	go s.teardown()
	w.WriteHeader(http.StatusOK)
}

// ─── Mode application ─────────────────────────────────────────────────────────

func (s *WiFi) apply() error {
	s.mu.RLock()
	mode := s.state.Mode
	s.mu.RUnlock()

	s.teardown()

	switch mode {
	case ModeRouter:
		return s.applyRouter()
	case ModeExtender:
		return s.applyExtender()
	case ModeOff:
		return nil
	default:
		return fmt.Errorf("unknown mode: %s", mode)
	}
}

// applyRouter sets up the Orange Pi as a full WiFi router/AP.
//
// Network flow: Internet → eth0 → Orange Pi → wlan0 → connected devices
//
//  1. hostapd creates the AP on wlan0
//  2. wlan0 gets a static gateway IP (SubnetBase.1)
//  3. dnsmasq provides DHCP (.50-.150) and DNS forwarding
//  4. iptables MASQUERADE on eth0 enables NAT
//
// Ad blocking and VPN are NOT applied here — they are applied by their
// own packages after reading wifi.Service.Status().
func (s *WiFi) applyRouter() error {
	s.mu.RLock()
	cfg := s.state.Router
	s.mu.RUnlock()

	slog.Info("wifi: applying router mode", "ssid", cfg.SSID, "band", cfg.Band)

	if err := s.writeHostapdConf(cfg, "wlan0"); err != nil {
		return fmt.Errorf("hostapd config: %w", err)
	}
	if err := s.cmd.Run("systemctl", "restart", "hostapd"); err != nil {
		return fmt.Errorf("start hostapd: %w", err)
	}

	gatewayIP := cfg.SubnetBase + ".1/24"
	s.cmd.Run("ip", "addr", "flush", "dev", "wlan0") //nolint:errcheck
	if err := s.cmd.Run("ip", "addr", "add", gatewayIP, "dev", "wlan0"); err != nil {
		return fmt.Errorf("set wlan0 IP: %w", err)
	}
	s.cmd.Run("ip", "link", "set", "wlan0", "up") //nolint:errcheck

	if err := s.writeDnsmasqConf(cfg.SubnetBase, cfg.DNSProvider, "wlan0"); err != nil {
		return fmt.Errorf("dnsmasq config: %w", err)
	}
	if err := s.cmd.Run("systemctl", "restart", "dnsmasq"); err != nil {
		return fmt.Errorf("start dnsmasq: %w", err)
	}

	// NAT: share eth0 internet with wlan0 devices
	os.WriteFile("/proc/sys/net/ipv4/ip_forward", []byte("1"), 0644)        //nolint:errcheck
	s.cmd.Run("iptables", "-t", "nat", "-F")                                 //nolint:errcheck
	s.cmd.Run("iptables", "-F", "FORWARD")                                   //nolint:errcheck
	if err := s.cmd.Run("iptables", "-t", "nat", "-A", "POSTROUTING", "-o", "eth0", "-j", "MASQUERADE"); err != nil {
		return fmt.Errorf("iptables NAT: %w", err)
	}
	s.cmd.Run("iptables", "-A", "FORWARD", "-i", "wlan0", "-o", "eth0", "-j", "ACCEPT")                                                  //nolint:errcheck
	s.cmd.Run("iptables", "-A", "FORWARD", "-i", "eth0", "-o", "wlan0", "-m", "state", "--state", "RELATED,ESTABLISHED", "-j", "ACCEPT") //nolint:errcheck

	s.mu.Lock()
	s.status = Status{
		Mode:        ModeRouter,
		Active:      true,
		SSID:        cfg.SSID,
		APInterface: "wlan0",
		SubnetBase:  cfg.SubnetBase,
		GatewayIP:   cfg.SubnetBase + ".1",
	}
	s.mu.Unlock()

	slog.Info("wifi: router mode active", "ssid", cfg.SSID, "gateway", gatewayIP)
	return nil
}

// applyExtender sets up the Orange Pi as a WiFi range extender.
//
// Network flow: Upstream router → wlan0 (client) → Orange Pi → wlan0_ap (AP) → devices
//
// Uses Linux ap+sta concurrent mode on a single radio. Bandwidth is halved
// since one radio handles both client and AP duties.
// UseSecondRadio=true uses wlan1 for the AP (requires a USB WiFi dongle).
func (s *WiFi) applyExtender() error {
	s.mu.RLock()
	cfg := s.state.Extender
	s.mu.RUnlock()

	slog.Info("wifi: applying extender mode", "upstream", cfg.UpstreamSSID, "new_ssid", cfg.ExtenderSSID)

	apInterface := "wlan0_ap"
	if cfg.UseSecondRadio {
		apInterface = "wlan1"
	} else {
		s.cmd.Run("iw", "dev", "wlan0_ap", "del") //nolint:errcheck
		if err := s.cmd.Run("iw", "dev", "wlan0", "interface", "add", "wlan0_ap", "type", "__ap"); err != nil {
			return fmt.Errorf("create virtual AP interface: %w", err)
		}
		s.cmd.Run("ip", "link", "set", "wlan0_ap", "up") //nolint:errcheck
	}

	if err := s.writeWpaSupplicantConf(cfg.UpstreamSSID, cfg.UpstreamPassword); err != nil {
		return fmt.Errorf("wpa_supplicant config: %w", err)
	}
	s.cmd.Run("killall", "wpa_supplicant") //nolint:errcheck
	if err := s.cmd.Run("wpa_supplicant", "-B", "-i", "wlan0", "-c", "/etc/wpa_supplicant/wpa_supplicant-wlan0.conf"); err != nil {
		return fmt.Errorf("start wpa_supplicant: %w", err)
	}
	if err := s.cmd.Run("dhclient", "wlan0"); err != nil {
		return fmt.Errorf("dhclient wlan0: %w", err)
	}

	extCfg := RouterConfig{
		SSID:       cfg.ExtenderSSID,
		Password:   cfg.ExtenderPassword,
		Band:       cfg.ExtenderBand,
		Channel:    0, // ACS: auto-match upstream channel
		MaxClients: 20,
		SubnetBase: "192.168.200",
	}
	if err := s.writeHostapdConf(extCfg, apInterface); err != nil {
		return fmt.Errorf("hostapd config: %w", err)
	}
	if err := s.cmd.Run("systemctl", "restart", "hostapd"); err != nil {
		return fmt.Errorf("start hostapd: %w", err)
	}

	s.cmd.Run("ip", "addr", "flush", "dev", apInterface) //nolint:errcheck
	if err := s.cmd.Run("ip", "addr", "add", "192.168.200.1/24", "dev", apInterface); err != nil {
		return fmt.Errorf("set AP interface IP: %w", err)
	}

	if err := s.writeDnsmasqConf("192.168.200", "cloudflare", apInterface); err != nil {
		return fmt.Errorf("dnsmasq config: %w", err)
	}
	s.cmd.Run("systemctl", "restart", "dnsmasq") //nolint:errcheck

	os.WriteFile("/proc/sys/net/ipv4/ip_forward", []byte("1"), 0644)                                                                          //nolint:errcheck
	s.cmd.Run("iptables", "-t", "nat", "-F")                                                                                                   //nolint:errcheck
	s.cmd.Run("iptables", "-t", "nat", "-A", "POSTROUTING", "-o", "wlan0", "-j", "MASQUERADE")                                                //nolint:errcheck
	s.cmd.Run("iptables", "-A", "FORWARD", "-i", apInterface, "-o", "wlan0", "-j", "ACCEPT")                                                  //nolint:errcheck
	s.cmd.Run("iptables", "-A", "FORWARD", "-i", "wlan0", "-o", apInterface, "-m", "state", "--state", "RELATED,ESTABLISHED", "-j", "ACCEPT") //nolint:errcheck

	s.mu.Lock()
	s.status = Status{
		Mode:         ModeExtender,
		Active:       true,
		SSID:         cfg.ExtenderSSID,
		APInterface:  apInterface,
		SubnetBase:   "192.168.200",
		GatewayIP:    "192.168.200.1",
		UpstreamSSID: cfg.UpstreamSSID,
	}
	s.mu.Unlock()

	slog.Info("wifi: extender mode active", "new_ssid", cfg.ExtenderSSID, "upstream", cfg.UpstreamSSID)
	return nil
}

// ─── Config file writers ──────────────────────────────────────────────────────

func (s *WiFi) writeHostapdConf(cfg RouterConfig, iface string) error {
	hwMode := "a"
	if cfg.Band == "2.4GHz" {
		hwMode = "g"
	}
	if cfg.MaxClients == 0 {
		cfg.MaxClients = 20
	}
	content := fmt.Sprintf(`# Generated by strct-agent
interface=%s
driver=nl80211
ssid=%s
hw_mode=%s
channel=%d
ieee80211n=1
ieee80211ac=1
wmm_enabled=1
country_code=US
ieee80211d=1
wpa=2
wpa_key_mgmt=WPA-PSK
wpa_passphrase=%s
rsn_pairwise=CCMP
ieee80211w=1
ignore_broadcast_ssid=0
max_num_sta=%d
`, iface, cfg.SSID, hwMode, cfg.Channel, cfg.Password, cfg.MaxClients)

	return os.WriteFile("/etc/hostapd/hostapd.conf", []byte(content), 0600)
}

// writeDnsmasqConf writes /etc/dnsmasq.d/strct.conf.
//
// Directives:
//
//	interface=IFACE           only serve DHCP/DNS on the AP interface
//	dhcp-range=X.50,X.150    IP range handed to connected devices
//	dhcp-option=3,X.1        default gateway = Orange Pi
//	dhcp-option=6,X.1        DNS server = Orange Pi (dnsmasq itself)
//	server=1.1.1.1            upstream DNS dnsmasq forwards to
//	no-resolv                 don't read /etc/resolv.conf (use server= only)
func (s *WiFi) writeDnsmasqConf(subnetBase, dnsProvider, iface string) error {
	dnsServers := map[string][2]string{
		"cloudflare": {"1.1.1.1", "1.0.0.1"},
		"google":     {"8.8.8.8", "8.8.4.4"},
		"adguard":    {"94.140.14.14", "94.140.15.15"},
		"quad9":      {"9.9.9.9", "149.112.112.112"},
	}
	dns, ok := dnsServers[dnsProvider]
	if !ok {
		dns = dnsServers["cloudflare"]
	}

	content := fmt.Sprintf(`# Generated by strct-agent
interface=%s
bind-interfaces
dhcp-range=%s.50,%s.150,24h
dhcp-option=3,%s.1
dhcp-option=6,%s.1
server=%s
server=%s
no-resolv
log-queries
`, iface, subnetBase, subnetBase, subnetBase, subnetBase, dns[0], dns[1])

	return os.WriteFile("/etc/dnsmasq.d/strct.conf", []byte(content), 0644)
}

func (s *WiFi) writeWpaSupplicantConf(ssid, password string) error {
	content := fmt.Sprintf(`ctrl_interface=DIR=/var/run/wpa_supplicant GROUP=netdev
update_config=1
country=US

network={
    ssid="%s"
    psk="%s"
    key_mgmt=WPA-PSK
}
`, ssid, password)
	if err := os.MkdirAll("/etc/wpa_supplicant", 0755); err != nil {
		return err
	}
	return os.WriteFile("/etc/wpa_supplicant/wpa_supplicant-wlan0.conf", []byte(content), 0600)
}

// ─── Teardown ─────────────────────────────────────────────────────────────────

func (s *WiFi) teardown() {
	slog.Info("wifi: tearing down")
	s.cmd.Run("systemctl", "stop", "hostapd")                                      //nolint:errcheck
	s.cmd.Run("systemctl", "stop", "dnsmasq")                                      //nolint:errcheck
	s.cmd.Run("killall", "wpa_supplicant")                                          //nolint:errcheck
	s.cmd.Run("killall", "dhclient")                                                //nolint:errcheck
	s.cmd.Run("iptables", "-t", "nat", "-F")                                       //nolint:errcheck
	s.cmd.Run("iptables", "-F", "FORWARD")                                          //nolint:errcheck
	s.cmd.Run("iw", "dev", "wlan0_ap", "del")                                      //nolint:errcheck
	os.WriteFile("/proc/sys/net/ipv4/ip_forward", []byte("0"), 0644)               //nolint:errcheck

	s.mu.Lock()
	s.status = Status{Mode: ModeOff, Active: false}
	s.mu.Unlock()
}

// ─── Helpers ──────────────────────────────────────────────────────────────────

func (s *WiFi) refreshStatus() {
	s.mu.RLock()
	mode := s.state.Mode
	s.mu.RUnlock()
	if mode == ModeOff {
		return
	}
	out, err := s.cmd.CombinedOutput("arp", "-a")
	if err == nil {
		s.mu.Lock()
		s.status.ConnectedIPs = strings.Count(string(out), "wlan0")
		s.status.Error = ""
		s.mu.Unlock()
	}
}

type ScannedNetwork struct {
	SSID       string `json:"ssid"`
	Signal     int    `json:"signal_dbm"`
	Frequency  string `json:"frequency"`
	Encrypted  bool   `json:"encrypted"`
	MACAddress string `json:"mac"`
}

func parseIWScan(data []byte) []ScannedNetwork {
	var networks []ScannedNetwork
	var current *ScannedNetwork
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "BSS ") {
			if current != nil {
				networks = append(networks, *current)
			}
			mac := strings.TrimPrefix(line, "BSS ")
			mac = strings.Split(mac, "(")[0]
			current = &ScannedNetwork{MACAddress: strings.TrimSpace(mac)}
			continue
		}
		if current == nil {
			continue
		}
		switch {
		case strings.HasPrefix(line, "SSID: "):
			current.SSID = strings.TrimPrefix(line, "SSID: ")
		case strings.HasPrefix(line, "signal: "):
			fmt.Sscanf(strings.TrimPrefix(line, "signal: "), "%d", &current.Signal)
		case strings.HasPrefix(line, "freq: "):
			var freq int
			fmt.Sscanf(strings.TrimPrefix(line, "freq: "), "%d", &freq)
			if freq >= 5000 {
				current.Frequency = "5GHz"
			} else {
				current.Frequency = "2.4GHz"
			}
		case strings.Contains(line, "Privacy"):
			current.Encrypted = true
		}
	}
	if current != nil {
		networks = append(networks, *current)
	}
	return networks
}

func validateConfig(cfg WiFiConfig) error {
	switch cfg.Mode {
	case ModeRouter:
		if cfg.Router.SSID == "" {
			return fmt.Errorf("router.ssid is required")
		}
		if len(cfg.Router.Password) < 8 {
			return fmt.Errorf("router.password must be >= 8 characters")
		}
	case ModeExtender:
		if cfg.Extender.UpstreamSSID == "" {
			return fmt.Errorf("extender.upstream_ssid is required")
		}
		if cfg.Extender.ExtenderSSID == "" {
			return fmt.Errorf("extender.extender_ssid is required")
		}
		if len(cfg.Extender.ExtenderPassword) < 8 {
			return fmt.Errorf("extender.extender_password must be >= 8 characters")
		}
	case ModeOff:
	default:
		return fmt.Errorf("invalid mode: %s", cfg.Mode)
	}
	return nil
}