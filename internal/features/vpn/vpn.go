package vpn

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"os/exec"
	"strings"
	"sync"
	"time"

	"github.com/strct-org/strct-agent/internal/config"
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
	return &VPN{
		Config: cfg,
		State:  VPNState{},
	}
}
func NewFromConfig(cfg *config.Config) *VPN {
	return New(Config{
		DeviceID: cfg.DeviceID,
		AuthKey:  cfg.TailScaleAuthToken,
	})
}

func (v *VPN) RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/vpn/status", v.HandleGetStatus)
	mux.HandleFunc("POST /api/vpn/toggle", v.HandleToggleExitNode)
}

func (v *VPN) Start(ctx context.Context) error {
	slog.Info("vpn: starting")

	// refreshStatus acquires v.mu internally — that's correct.
	// But reading v.State AFTER refreshStatus returns is a race:
	// the lock has been released and another goroutine could write State.
	v.refreshStatus()

	v.mu.RLock()
	needsProvision := v.State.IsInstalled && (!v.State.IsRunning || v.State.Account == "")
	slog.Info("vpn: initial state",
		"installed", v.State.IsInstalled,
		"running", v.State.IsRunning,
		"account", v.State.Account,
	)
	v.mu.RUnlock()

	if needsProvision {
		slog.Info("vpn: not provisioned, triggering auto-provision")
		go v.autoProvision()
	}

	go func() {
		ticker := time.NewTicker(15 * time.Second)
		defer ticker.Stop()

		for {
			select {
			case <-ctx.Done():
				slog.Info("vpn: status refresh stopped")
				return
			case <-ticker.C:
				v.refreshStatus()
			}
		}
	}()

	return nil
}

func (v *VPN) HandleGetStatus(w http.ResponseWriter, r *http.Request) {
	v.mu.RLock()
	defer v.mu.RUnlock()

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(v.State)
}

func (v *VPN) HandleToggleExitNode(w http.ResponseWriter, r *http.Request) {
	type ToggleRequest struct {
		Enable bool `json:"enable"`
	}

	var req ToggleRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		slog.Error("vpn: invalid toggle request", "err", err)
		http.Error(w, "Invalid JSON", http.StatusBadRequest)
		return
	}

	slog.Info("vpn: received toggle exit node request", "enable", req.Enable)

	// Respond immediately, process in background
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(map[string]string{"status": "processing"})

	go v.setExitNode(req.Enable)
}

func (v *VPN) autoProvision() {
	if v.Config.AuthKey == "" {
		slog.Error("vpn: auto-provisioning failed, no AuthKey in config")
		return
	}

	slog.Info("vpn: Attempting Auto-Provisioning with Configured Key...")
	v.mu.Lock()
	v.State.ErrorMessage = "Provisioning device..."
	v.mu.Unlock()

	slog.Info("vpn: enabling IP forwarding")
	if out, err := exec.Command("sysctl", "-w", "net.ipv4.ip_forward=1").CombinedOutput(); err != nil {
		slog.Info("vpn: Attempting to set ipv6 forward as well, out:", out)
	}
	if out, err := exec.Command("sysctl", "-w", "net.ipv6.conf.all.forwarding=1").CombinedOutput(); err != nil {
		slog.Info("vpn: Continuing without ipv6 forwarding, out:", out)
	}

	// Run tailscale up
	// We mask the key in logs for security
	maskedKey := "tskey-..." + v.Config.AuthKey[len(v.Config.AuthKey)-5:]
	slog.Info("vpn: Running Tailscale Up for Auto-Provisioning, key masked in logs", "masked_key", maskedKey)

	cmd := exec.Command("tailscale", "up",
		"--authkey="+v.Config.AuthKey,
		"--accept-routes", // Accept routes from the mesh
		"--reset",         // Force reset to ensure key is accepted if state is weird
	)

	output, err := cmd.CombinedOutput()
	if err != nil {
		slog.Error("vpn: Provisioning Failed: Command execution error", "err", err)
		slog.Error("vpn: Provisioning Failed: Command output", "output", string(output))

		v.mu.Lock()
		v.State.ErrorMessage = fmt.Sprintf("Provisioning failed: %s", strings.TrimSpace(string(output)))
		v.mu.Unlock()
	} else {
		slog.Info("vpn: Provisioning Success. Tailscale is up.")
		v.refreshStatus()
	}
}

func (v *VPN) setExitNode(enable bool) {
	slog.Info("vpn: setting exit node", "enable", enable)

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

	slog.Info("vpn: running tailscale set to toggle exit node", "arg", arg)
	cmd := exec.Command("tailscale", "set", arg)

	output, err := cmd.CombinedOutput()
	if err != nil {
		slog.Error("vpn: Failed to toggle Exit Node", "err", err)
		slog.Error("vpn: Failed to toggle Exit Node, command output", "output", string(output))
	} else {
		slog.Info("vpn: Exit Node toggle success, command output", "output", string(output))
	}

	// Trigger immediate status refresh
	v.refreshStatus()
}

func (v *VPN) refreshStatus() {
	v.mu.Lock()
	defer v.mu.Unlock()

	_, err := exec.LookPath("tailscale")
	v.State.IsInstalled = err == nil
	if !v.State.IsInstalled {
		if v.State.IsRunning { // Only log if state changed
			slog.Error("vpn: Tailscale binary not found in PATH")
		}
		v.State.IsRunning = false
		return
	}

	cmd := exec.Command("tailscale", "status", "--json")
	out, err := cmd.Output()
	if err != nil {
		v.State.IsRunning = false
		return
	}

	v.State.IsRunning = true
	v.State.ErrorMessage = ""

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
		for _, user := range status.User {
			if strings.Contains(user.LoginName, "@") {
				v.State.Account = user.LoginName
				break
			}
		}
	} else {
		slog.Error("vpn: Failed to parse tailscale status JSON", "err", err)
	}

	prefsCmd := exec.Command("tailscale", "debug", "prefs")
	prefsOut, err := prefsCmd.Output()
	if err != nil {
		slog.Error("vpn: Failed to get debug prefs", "err", err)
	} else {
		prefsStr := string(prefsOut)
		hasV4 := strings.Contains(prefsStr, "0.0.0.0/0")
		hasV6 := strings.Contains(prefsStr, "::/0")

		v.State.IsExitNode = hasV4 || hasV6

		slog.Info("vpn: status refreshed", "ip", v.State.TailscaleIP, "exit_node", v.State.IsExitNode, "account", v.State.Account)
	}
}
