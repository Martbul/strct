package vpn

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os/exec"
	"strings"
	"sync"
	"time"
)


type Config struct {
	DeviceID string
}

type VPNState struct {
	IsInstalled    bool   `json:"is_installed"`
	IsRunning      bool   `json:"is_running"`
	IsExitNode     bool   `json:"is_exit_node"`
	TailscaleIP    string `json:"tailscale_ip"`
	Account        string `json:"account"` 
	AuthKeySet     bool   `json:"auth_key_set"`
}

type VPN struct {
	Config Config
	State  VPNState
	mu     sync.RWMutex
}


func New(cfg Config) *VPN {
	return &VPN{
		Config: cfg,
		State:  VPNState{},
	}
}

func (v *VPN) Start() error {
	log.Printf("[VPN] Starting Tailscale VPN Controller")

	// Check installation and status on startup
	go v.refreshStatus()

	// Periodic refresh
	ticker := time.NewTicker(30 * time.Second)
	go func() {
		for range ticker.C {
			v.refreshStatus()
		}
	}()

	return nil
}

// --- HTTP Handlers ---

// HandleGetStatus returns the current VPN state
func (v *VPN) HandleGetStatus(w http.ResponseWriter, r *http.Request) {
	v.mu.RLock()
	defer v.mu.RUnlock()

	// Force a quick refresh if requested (optional)
	// v.refreshStatus() 

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(v.State)
}

// HandleSetup accepts an Auth Key to log in the machine
func (v *VPN) HandleSetup(w http.ResponseWriter, r *http.Request) {
	type SetupRequest struct {
		AuthKey string `json:"auth_key"`
	}

	var req SetupRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid JSON", http.StatusBadRequest)
		return
	}

	if req.AuthKey == "" {
		http.Error(w, "Auth key is required", http.StatusBadRequest)
		return
	}

	go v.runSetup(req.AuthKey)

	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(map[string]string{"status": "setup_initiated"})
}

// HandleToggleExitNode turns the "Exit Node" feature on/off
func (v *VPN) HandleToggleExitNode(w http.ResponseWriter, r *http.Request) {
	type ToggleRequest struct {
		Enable bool `json:"enable"`
	}

	var req ToggleRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid JSON", http.StatusBadRequest)
		return
	}

	go v.setExitNode(req.Enable)

	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(map[string]string{"status": "toggling_exit_node"})
}

// --- System Logic ---

func (v *VPN) refreshStatus() {
	v.mu.Lock()
	defer v.mu.Unlock()

	// 1. Check if binary exists
	_, err := exec.LookPath("tailscale")
	v.State.IsInstalled = err == nil

	if !v.State.IsInstalled {
		v.State.IsRunning = false
		return
	}

	// 2. Run `tailscale status --json`
	// This command provides rich details about the connection
	cmd := exec.Command("tailscale", "status", "--json")
	out, err := cmd.Output()
	
	if err != nil {
		// If command fails, daemon might be stopped or not logged in
		v.State.IsRunning = false
		v.State.Account = ""
		v.State.TailscaleIP = ""
		return
	}

	v.State.IsRunning = true

	// Parse JSON Output from Tailscale
	var status struct {
		Self struct {
			TailscaleIPs []string `json:"TailscaleIPs"`
			UserID       int      `json:"UserID"`
		} `json:"Self"`
		User map[string]struct {
			LoginName string `json:"LoginName"`
		} `json:"User"`
	}
	
	if err := json.Unmarshal(out, &status); err == nil {
		if len(status.Self.TailscaleIPs) > 0 {
			v.State.TailscaleIP = status.Self.TailscaleIPs[0]
		}
		
		// Find user login name
		for id, user := range status.User {
			// Convert UserID to string for map lookup or iterate
			// Simplified: just grab the first user that looks like an email
			if strings.Contains(user.LoginName, "@") {
				v.State.Account = user.LoginName
				break
			}
			// Fallback: match ID
			if fmt.Sprintf("%d", status.Self.UserID) == id {
				v.State.Account = user.LoginName
			}
		}
	}

	// 3. Check if Exit Node is advertised
	// We check arguments of the running process or `tailscale debug prefs`
	// A simpler way is to check `ip rule` or internal prefs, but checking command args is reliable enough for basic state
	// NOTE: `tailscale status` doesn't explicitly say "I am an exit node", 
	// we usually know this if we started it with --advertise-exit-node.
	// For accurate checking, we can inspect prefs:
	prefsCmd := exec.Command("tailscale", "debug", "prefs")
	prefsOut, _ := prefsCmd.Output()
	v.State.IsExitNode = strings.Contains(string(prefsOut), "\"AdvertiseRoutes\": null") == false // Rough check, assumes exit node is a route 0.0.0.0/0
	// Better check:
	v.State.IsExitNode = strings.Contains(string(prefsOut), "0.0.0.0/0") || strings.Contains(string(prefsOut), "::/0")
}

func (v *VPN) runSetup(authKey string) {
	log.Println("[VPN] Running Tailscale Setup...")

	// 1. Enable IP Forwarding (Required for Exit Node)
	// echo 'net.ipv4.ip_forward = 1' | sudo tee -a /etc/sysctl.d/99-tailscale.conf
	// sudo sysctl -p /etc/sysctl.d/99-tailscale.conf
	exec.Command("sysctl", "-w", "net.ipv4.ip_forward=1").Run()
	exec.Command("sysctl", "-w", "net.ipv6.conf.all.forwarding=1").Run()

	// 2. Bring Tailscale Up
	// --authkey: automatically logs in
	// --advertise-exit-node: tells Tailscale this device is a VPN server
	// --accept-routes: accepts other subnet routes
	cmd := exec.Command("tailscale", "up",
		"--authkey="+authKey,
		"--advertise-exit-node",
		"--accept-routes",
		"--reset", // Reset any previous conflicting settings
	)

	err := cmd.Run()
	if err != nil {
		log.Printf("[VPN] Setup Failed: %v", err)
	} else {
		log.Println("[VPN] Setup Complete. Device is now an Exit Node.")
	}

	v.refreshStatus()
}

func (v *VPN) setExitNode(enable bool) {
	log.Printf("[VPN] Toggling Exit Node: %v", enable)

	// We must re-run `tailscale up` to change advertisement settings
	args := []string{"up", "--reset"}
	
	if enable {
		// Enable forwarding kernel settings just in case
		exec.Command("sysctl", "-w", "net.ipv4.ip_forward=1").Run()
		args = append(args, "--advertise-exit-node")
	} else {
		// To disable, we simply don't pass the flag (or pass empty routes if needed, depends on version)
		// Usually running `tailscale up` without the flag clears it, but explicit is better:
		// Tailscale doesn't have a "stop advertising" flag easily, so we usually run `up` with just default args.
		// However, keeping state is safer.
	}

	err := exec.Command("tailscale", args...).Run()
	if err != nil {
		log.Printf("[VPN] Failed to toggle exit node: %v", err)
	}

	v.refreshStatus()
}