package vpn

import (
	"context"
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
	AuthKey  string // Pre-injected key from your distribution config
}

type VPNState struct {
	IsInstalled  bool   `json:"is_installed"`
	IsRunning    bool   `json:"is_running"`
	IsExitNode   bool   `json:"is_exit_node"`
	TailscaleIP  string `json:"tailscale_ip"`
	Account      string `json:"account"`
	ErrorMessage string `json:"error_message,omitempty"`
}

type VPN struct {
	Config Config
	State  VPNState
	mu     sync.RWMutex
}

func New(cfg Config) *VPN {
	log.Printf("[VPN-DEBUG] Initializing VPN module for DeviceID: %s", cfg.DeviceID)
	return &VPN{
		Config: cfg,
		State:  VPNState{},
	}
}

//! implement canceling loginc with ctx context.Context
func (v *VPN) Start(ctx context.Context) error {
	log.Printf("[VPN] Starting VPN Controller Service...")

	// 1. Initial Check
	v.refreshStatus()

	// 2. Auto-Provisioning
	// If installed but not logged in, use the pre-configured AuthKey
	if v.State.IsInstalled && (!v.State.IsRunning || v.State.Account == "") {
		log.Println("[VPN-DEBUG] State detected as installed but not logged in. Triggering auto-provision.")
		go v.autoProvision()
	} else {
		log.Printf("[VPN-DEBUG] Start state: Installed=%v, Running=%v, Account=%s",
			v.State.IsInstalled, v.State.IsRunning, v.State.Account)
	}

	// 3. Periodic refresh loop
	ticker := time.NewTicker(15 * time.Second)
	go func() {
		for range ticker.C {
			v.refreshStatus()
		}
	}()

	return nil
}

func (v *VPN) HandleGetStatus(w http.ResponseWriter, r *http.Request) {
	v.mu.RLock()
	defer v.mu.RUnlock()

	// Log only if verbose debugging is needed, otherwise this spams
	// log.Printf("[VPN-DEBUG] Status requested by %s", r.RemoteAddr)

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(v.State)
}

func (v *VPN) HandleToggleExitNode(w http.ResponseWriter, r *http.Request) {
	type ToggleRequest struct {
		Enable bool `json:"enable"`
	}

	var req ToggleRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		log.Printf("[VPN-ERROR] Failed to decode ToggleExitNode request: %v", err)
		http.Error(w, "Invalid JSON", http.StatusBadRequest)
		return
	}

	log.Printf("[VPN] Received request to set Exit Node: %v", req.Enable)

	// Respond immediately, process in background
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(map[string]string{"status": "processing"})

	go v.setExitNode(req.Enable)
}


func (v *VPN) autoProvision() {
	if v.Config.AuthKey == "" {
		log.Println("[VPN-ERROR] No AuthKey provided in config. Skipping auto-provision.")
		return
	}

	log.Println("[VPN] Attempting Auto-Provisioning with Configured Key...")
	v.mu.Lock()
	v.State.ErrorMessage = "Provisioning device..."
	v.mu.Unlock()

	// Enable IP forwarding (Critical for Orange Pi)
	log.Println("[VPN-DEBUG] Enabling IPv4/IPv6 forwarding via sysctl...")
	if out, err := exec.Command("sysctl", "-w", "net.ipv4.ip_forward=1").CombinedOutput(); err != nil {
		log.Printf("[VPN-ERROR] Failed to set ipv4 forward: %v | %s", err, string(out))
	}
	if out, err := exec.Command("sysctl", "-w", "net.ipv6.conf.all.forwarding=1").CombinedOutput(); err != nil {
		log.Printf("[VPN-ERROR] Failed to set ipv6 forward: %v | %s", err, string(out))
	}

	// Run tailscale up
	// We mask the key in logs for security
	maskedKey := "tskey-..." + v.Config.AuthKey[len(v.Config.AuthKey)-5:]
	log.Printf("[VPN-DEBUG] Running: tailscale up --authkey=%s --accept-routes", maskedKey)

	cmd := exec.Command("tailscale", "up",
		"--authkey="+v.Config.AuthKey,
		"--accept-routes", // Accept routes from the mesh
		"--reset",         // Force reset to ensure key is accepted if state is weird
	)

	output, err := cmd.CombinedOutput()
	if err != nil {
		log.Printf("[VPN-FATAL] Provisioning Failed: %v", err)
		log.Printf("[VPN-FATAL] Tailscale Output: %s", string(output))

		v.mu.Lock()
		v.State.ErrorMessage = fmt.Sprintf("Provisioning failed: %s", strings.TrimSpace(string(output)))
		v.mu.Unlock()
	} else {
		log.Println("[VPN] Provisioning Success. Tailscale is up.")
		v.refreshStatus()
	}
}

func (v *VPN) setExitNode(enable bool) {
	log.Printf("[VPN-DEBUG] Executing setExitNode(%v)", enable)

	// Ensure forwarding is on before enabling
	if enable {
		exec.Command("sysctl", "-w", "net.ipv4.ip_forward=1").Run()
	}

	// Use `tailscale set` instead of `up`. It's faster and cleaner for runtime toggles.
	var arg string
	if enable {
		arg = "--advertise-exit-node"
	} else {
		// New CLI supports this to turn it off
		arg = "--advertise-exit-node=false"
	}

	log.Printf("[VPN-DEBUG] Running: tailscale set %s", arg)
	cmd := exec.Command("tailscale", "set", arg)

	output, err := cmd.CombinedOutput()
	if err != nil {
		log.Printf("[VPN-ERROR] Failed to toggle Exit Node: %v", err)
		log.Printf("[VPN-ERROR] Command Output: %s", string(output))
	} else {
		log.Printf("[VPN-DEBUG] Exit Node toggle success. Output: %s", string(output))
	}

	// Trigger immediate status refresh
	v.refreshStatus()
}

func (v *VPN) refreshStatus() {
	v.mu.Lock()
	defer v.mu.Unlock()

	// Check if binary exists
	_, err := exec.LookPath("tailscale")
	v.State.IsInstalled = err == nil
	if !v.State.IsInstalled {
		if v.State.IsRunning { // Only log if state changed
			log.Println("[VPN-ERROR] Tailscale binary not found in PATH")
		}
		v.State.IsRunning = false
		return
	}

	// Get JSON status
	cmd := exec.Command("tailscale", "status", "--json")
	out, err := cmd.Output()
	if err != nil {
		// This usually happens if tailscaled is stopped or we are logged out
		// log.Printf("[VPN-DEBUG] 'tailscale status' failed (likely stopped/logged out): %v", err)
		v.State.IsRunning = false
		return
	}

	v.State.IsRunning = true
	v.State.ErrorMessage = ""

	// Parse Status
	// We map only what we need to avoid struct complexity errors
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
		// Try to find the user account
		for _, user := range status.User {
			// Simple heuristic: contains @
			if strings.Contains(user.LoginName, "@") {
				v.State.Account = user.LoginName
				break
			}
		}
	} else {
		log.Printf("[VPN-ERROR] Failed to parse tailscale status JSON: %v", err)
	}

	// Check exit node status via `tailscale debug prefs`
	// This is more reliable than parsing the full JSON map for advertised routes
	prefsCmd := exec.Command("tailscale", "debug", "prefs")
	prefsOut, err := prefsCmd.Output()
	if err != nil {
		log.Printf("[VPN-ERROR] Failed to get debug prefs: %v", err)
	} else {
		prefsStr := string(prefsOut)
		// debug prefs output format is usually: "AdvertisedRoutes: [0.0.0.0/0 ::/0]" or similar
		hasV4 := strings.Contains(prefsStr, "0.0.0.0/0")
		hasV6 := strings.Contains(prefsStr, "::/0")

		v.State.IsExitNode = hasV4 || hasV6

		// Optional: Periodic Verbose log of state
		log.Printf("[VPN-DEBUG] Status Refreshed: IP=%s, ExitNode=%v, Account=%s",
			v.State.TailscaleIP, v.State.IsExitNode, v.State.Account)
	}
}
