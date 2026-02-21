// package vpn

// import (
// 	"context"
// 	"encoding/json"
// 	"fmt"
// 	"log/slog"
// 	"net/http"
// 	"os/exec"
// 	"strings"
// 	"sync"
// 	"time"

// 	"github.com/strct-org/strct-agent/internal/config"
// )

// type Config struct {
// 	DeviceID string
// 	AuthKey  string // Pre-injected key from your distribution config
// }

// type VPNState struct {
// 	IsInstalled  bool   `json:"is_installed"`
// 	IsRunning    bool   `json:"is_running"`
// 	IsExitNode   bool   `json:"is_exit_node"`
// 	TailscaleIP  string `json:"tailscale_ip"`
// 	Account      string `json:"account"`
// 	ErrorMessage string `json:"error_message,omitempty"`
// }

// type VPN struct {
// 	Config Config
// 	State  VPNState
// 	mu     sync.RWMutex
// }

// func New(cfg Config) *VPN {
// 	return &VPN{
// 		Config: cfg,
// 		State:  VPNState{},
// 	}
// }
// func NewFromConfig(cfg *config.Config) *VPN {
// 	return New(Config{
// 		DeviceID: cfg.DeviceID,
// 		AuthKey:  cfg.TailScaleAuthToken,
// 	})
// }

// func (v *VPN) RegisterRoutes(mux *http.ServeMux) {
// 	mux.HandleFunc("GET /api/vpn/status", v.HandleGetStatus)
// 	mux.HandleFunc("POST /api/vpn/toggle", v.HandleToggleExitNode)
// }

// func (v *VPN) Start(ctx context.Context) error {
// 	slog.Info("vpn: starting")

// 	// refreshStatus acquires v.mu internally — that's correct.
// 	// But reading v.State AFTER refreshStatus returns is a race:
// 	// the lock has been released and another goroutine could write State.
// 	v.refreshStatus()

// 	v.mu.RLock()
// 	needsProvision := v.State.IsInstalled && (!v.State.IsRunning || v.State.Account == "")
// 	slog.Info("vpn: initial state",
// 		"installed", v.State.IsInstalled,
// 		"running", v.State.IsRunning,
// 		"account", v.State.Account,
// 	)
// 	v.mu.RUnlock()

// 	if needsProvision {
// 		slog.Info("vpn: not provisioned, triggering auto-provision")
// 		go v.autoProvision()
// 	}

// 	go func() {
// 		ticker := time.NewTicker(15 * time.Second)
// 		defer ticker.Stop()

// 		for {
// 			select {
// 			case <-ctx.Done():
// 				slog.Info("vpn: status refresh stopped")
// 				return
// 			case <-ticker.C:
// 				v.refreshStatus()
// 			}
// 		}
// 	}()

// 	return nil
// }

// func (v *VPN) HandleGetStatus(w http.ResponseWriter, r *http.Request) {
// 	v.mu.RLock()
// 	defer v.mu.RUnlock()

// 	w.Header().Set("Content-Type", "application/json")
// 	json.NewEncoder(w).Encode(v.State)
// }

// func (v *VPN) HandleToggleExitNode(w http.ResponseWriter, r *http.Request) {
// 	type ToggleRequest struct {
// 		Enable bool `json:"enable"`
// 	}

// 	var req ToggleRequest
// 	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
// 		slog.Error("vpn: invalid toggle request", "err", err)
// 		http.Error(w, "Invalid JSON", http.StatusBadRequest)
// 		return
// 	}

// 	slog.Info("vpn: received toggle exit node request", "enable", req.Enable)

// 	// Respond immediately, process in background
// 	w.WriteHeader(http.StatusOK)
// 	json.NewEncoder(w).Encode(map[string]string{"status": "processing"})

// 	go v.setExitNode(req.Enable)
// }

// func (v *VPN) autoProvision() {
// 	if v.Config.AuthKey == "" {
// 		slog.Error("vpn: auto-provisioning failed, no AuthKey in config")
// 		return
// 	}

// 	slog.Info("vpn: Attempting Auto-Provisioning with Configured Key...")
// 	v.mu.Lock()
// 	v.State.ErrorMessage = "Provisioning device..."
// 	v.mu.Unlock()

// 	slog.Info("vpn: enabling IP forwarding")
// 	if out, err := exec.Command("sysctl", "-w", "net.ipv4.ip_forward=1").CombinedOutput(); err != nil {
// 		slog.Info("vpn: Attempting to set ipv6 forward as well", "out", string(out))
// 	}
// 	if out, err := exec.Command("sysctl", "-w", "net.ipv6.conf.all.forwarding=1").CombinedOutput(); err != nil {
// 		slog.Info("vpn: Continuing without ipv6 forwarding", "out", string(out))
// 	}

// 	// Run tailscale up
// 	// We mask the key in logs for security
// 	maskedKey := "tskey-..." + v.Config.AuthKey[len(v.Config.AuthKey)-5:]
// 	slog.Info("vpn: Running Tailscale Up for Auto-Provisioning, key masked in logs", "masked_key", maskedKey)

// 	cmd := exec.Command("tailscale", "up",
// 		"--authkey="+v.Config.AuthKey,
// 		"--accept-routes", // Accept routes from the mesh
// 		"--reset",         // Force reset to ensure key is accepted if state is weird
// 	)

// 	output, err := cmd.CombinedOutput()
// 	if err != nil {
// 		slog.Error("vpn: Provisioning Failed: Command execution error", "err", err)
// 		slog.Error("vpn: Provisioning Failed: Command output", "output", string(output))

// 		v.mu.Lock()
// 		v.State.ErrorMessage = fmt.Sprintf("Provisioning failed: %s", strings.TrimSpace(string(output)))
// 		v.mu.Unlock()
// 	} else {
// 		slog.Info("vpn: Provisioning Success. Tailscale is up.")
// 		v.refreshStatus()
// 	}
// }

// func (v *VPN) setExitNode(enable bool) {
// 	slog.Info("vpn: setting exit node", "enable", enable)

// 	// Ensure forwarding is on before enabling
// 	if enable {
// 		exec.Command("sysctl", "-w", "net.ipv4.ip_forward=1").Run()
// 	}

// 	// Use `tailscale set` instead of `up`. It's faster and cleaner for runtime toggles.
// 	var arg string
// 	if enable {
// 		arg = "--advertise-exit-node"
// 	} else {
// 		// New CLI supports this to turn it off
// 		arg = "--advertise-exit-node=false"
// 	}

// 	slog.Info("vpn: running tailscale set to toggle exit node", "arg", arg)
// 	cmd := exec.Command("tailscale", "set", arg)

// 	output, err := cmd.CombinedOutput()
// 	if err != nil {
// 		slog.Error("vpn: Failed to toggle Exit Node", "err", err)
// 		slog.Error("vpn: Failed to toggle Exit Node, command output", "output", string(output))
// 	} else {
// 		slog.Info("vpn: Exit Node toggle success, command output", "output", string(output))
// 	}

// 	// Trigger immediate status refresh
// 	v.refreshStatus()
// }

// func (v *VPN) refreshStatus() {
// 	v.mu.Lock()
// 	defer v.mu.Unlock()

// 	_, err := exec.LookPath("tailscale")
// 	v.State.IsInstalled = err == nil
// 	if !v.State.IsInstalled {
// 		if v.State.IsRunning { // Only log if state changed
// 			slog.Error("vpn: Tailscale binary not found in PATH")
// 		}
// 		v.State.IsRunning = false
// 		return
// 	}

// 	cmd := exec.Command("tailscale", "status", "--json")
// 	out, err := cmd.Output()
// 	if err != nil {
// 		v.State.IsRunning = false
// 		return
// 	}

// 	v.State.IsRunning = true
// 	v.State.ErrorMessage = ""

// 	var status struct {
// 		Self struct {
// 			TailscaleIPs []string `json:"TailscaleIPs"`
// 			UserID       int      `json:"UserID"`
// 		} `json:"Self"`
// 		User map[string]struct {
// 			LoginName string `json:"LoginName"`
// 		} `json:"User"`
// 	}

// 	if err := json.Unmarshal(out, &status); err == nil {
// 		if len(status.Self.TailscaleIPs) > 0 {
// 			v.State.TailscaleIP = status.Self.TailscaleIPs[0]
// 		}
// 		for _, user := range status.User {
// 			if strings.Contains(user.LoginName, "@") {
// 				v.State.Account = user.LoginName
// 				break
// 			}
// 		}
// 	} else {
// 		slog.Error("vpn: Failed to parse tailscale status JSON", "err", err)
// 	}

// 	prefsCmd := exec.Command("tailscale", "debug", "prefs")
// 	prefsOut, err := prefsCmd.Output()
// 	if err != nil {
// 		slog.Error("vpn: Failed to get debug prefs", "err", err)
// 	} else {
// 		prefsStr := string(prefsOut)
// 		hasV4 := strings.Contains(prefsStr, "0.0.0.0/0")
// 		hasV6 := strings.Contains(prefsStr, "::/0")

// 		v.State.IsExitNode = hasV4 || hasV6

// 		slog.Info("vpn: status refreshed", "ip", v.State.TailscaleIP, "exit_node", v.State.IsExitNode, "account", v.State.Account)
// 	}
// }
// Package vpn manages Tailscale subnet routing for whole-network VPN.
//
// This package is completely independent of wifi — it reads wifi.Status
// to know which subnet is active, then runs tailscale on top of it.
//
// What it does:
//   - tailscale up --advertise-routes=SUBNET/24 --advertise-exit-node
//   - Every device on the Orange Pi's AP gets VPN without any client install
//   - The Orange Pi can also act as an exit node for remote Tailscale peers
//
// Prerequisites on the device:
//   - tailscaled installed and enabled (see 2_install_deps.sh)
//   - Auth key stored in config (TAILSCALE_AUTH_KEY env var)
//   - Route approved at tailscale.com/admin after first run
package vpn

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/strct-org/strct-agent/internal/config"
	"github.com/strct-org/strct-agent/internal/features/wifi"
	"github.com/strct-org/strct-agent/internal/platform/executil"
)

// ─── Types ────────────────────────────────────────────────────────────────────

type VPNConfig struct {
	Enabled bool `json:"enabled"`

	// AuthKey is a Tailscale pre-auth key (tskey-auth-xxx).
	// Generate one at tailscale.com/admin/settings/keys.
	// If empty, `tailscale up` will print a URL for manual browser auth.
	AuthKey string `json:"auth_key,omitempty"`

	// AdvertiseExitNode makes the Orange Pi a VPN exit node.
	// Remote Tailscale peers can route ALL their internet traffic through here.
	AdvertiseExitNode bool `json:"advertise_exit_node"`
}

type Status struct {
	Enabled        bool   `json:"enabled"`
	TailscaleUp    bool   `json:"tailscale_up"`
	AdvertisedSubnet string `json:"advertised_subnet,omitempty"` // e.g. "192.168.100.0/24"
	TailscaleIP    string `json:"tailscale_ip,omitempty"`      // Orange Pi's Tailscale IP (100.x.x.x)
	PeerCount      int    `json:"peer_count"`
	ExitNodeActive bool   `json:"exit_node_active"`
	Error          string `json:"error,omitempty"`
}

// ─── Service ──────────────────────────────────────────────────────────────────

// wifiStatusReader is the narrow interface vpn needs from the wifi package.
// Depends on wifi.Status, not the full wifi.Service — keeps coupling minimal.
type wifiStatusReader interface {
	Status() wifi.Status
}

type VPN struct {
	cfg     config.Config
	state   VPNConfig
	status  Status
	mu      sync.RWMutex
	cmd     executil.Runner
	wifiSvc wifiStatusReader
}

func New(cfg config.Config, cmd executil.Runner, wifiSvc wifiStatusReader) *VPN {
	return &VPN{
		cfg:     cfg,
		cmd:     cmd,
		wifiSvc: wifiSvc,
		state: VPNConfig{
			Enabled:           false,
			AdvertiseExitNode: true,
		},
	}
}

func NewFromConfig(cfg *config.Config, wifiSvc wifiStatusReader) *VPN {
    var cmd executil.Runner
    if cfg.IsDev {
        cmd = executil.NewDevRunner()
    } else {
        cmd = executil.Real{}
    }
    return New(*cfg, cmd, wifiSvc)
}

func (s *VPN) RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/vpn/config",  s.handleGetConfig)
	mux.HandleFunc("POST /api/vpn/config", s.handleSetConfig)
	mux.HandleFunc("GET /api/vpn/status",  s.handleGetStatus)
	mux.HandleFunc("POST /api/vpn/stop",   s.handleStop)
}

func (s *VPN) Start(ctx context.Context) error {
	slog.Info("vpn: service started")

	go func() {
		ticker := time.NewTicker(60 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				s.stop()
				return
			case <-ticker.C:
				s.refreshStatus()
			}
		}
	}()

	return nil
}

// ─── HTTP handlers ────────────────────────────────────────────────────────────

func (s *VPN) handleGetConfig(w http.ResponseWriter, r *http.Request) {
	s.mu.RLock()
	state := s.state
	s.mu.RUnlock()
	// Never send the auth key back to the client
	state.AuthKey = maskAuthKey(state.AuthKey)
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(state)
}

func (s *VPN) handleSetConfig(w http.ResponseWriter, r *http.Request) {
	var req VPNConfig
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid JSON", http.StatusBadRequest)
		return
	}

	s.mu.Lock()
	// Preserve existing auth key if client sends the masked placeholder
	if req.AuthKey == "tskey-***" || req.AuthKey == "" {
		req.AuthKey = s.state.AuthKey
	}
	s.state = req
	s.mu.Unlock()

	go func() {
		if req.Enabled {
			if err := s.apply(); err != nil {
				slog.Error("vpn: apply failed", "err", err)
				s.mu.Lock()
				s.status.Error = err.Error()
				s.mu.Unlock()
			}
		} else {
			s.stop()
		}
	}()

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"status": "applying"})
}

func (s *VPN) handleGetStatus(w http.ResponseWriter, r *http.Request) {
	s.mu.RLock()
	st := s.status
	s.mu.RUnlock()
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(st)
}

func (s *VPN) handleStop(w http.ResponseWriter, r *http.Request) {
	go s.stop()
	w.WriteHeader(http.StatusOK)
}

// ─── Core logic ───────────────────────────────────────────────────────────────

// apply starts Tailscale and advertises the wifi subnet as a subnet router.
//
// Full command sequence:
//
//	systemctl start tailscaled
//	tailscale up \
//	  --authkey=tskey-auth-xxx \           (skipped if empty → browser login URL printed)
//	  --advertise-routes=192.168.100.0/24 \ (the wifi AP subnet)
//	  --advertise-exit-node \              (if AdvertiseExitNode=true)
//	  --accept-routes
//
// After this, the user must go to tailscale.com/admin → Machines →
// Orange Pi → Edit route settings → approve the subnet route.
// This is a one-time step.
func (s *VPN) apply() error {
	wifiStatus := s.wifiSvc.Status()
	if !wifiStatus.Active {
		return fmt.Errorf("wifi must be active before enabling VPN — start Router or Extender mode first")
	}

	subnet := wifiStatus.SubnetBase + ".0/24"

	s.mu.RLock()
	cfg := s.state
	s.mu.RUnlock()

	slog.Info("vpn: starting Tailscale subnet router", "subnet", subnet)

	// Start the Tailscale daemon if not already running
	s.cmd.Run("systemctl", "start", "tailscaled") //nolint:errcheck
	time.Sleep(2 * time.Second)                    // give tailscaled time to bind its socket

	args := []string{"up",
		"--advertise-routes=" + subnet,
		"--accept-routes",
	}
	if cfg.AdvertiseExitNode {
		args = append(args, "--advertise-exit-node")
	}
	if cfg.AuthKey != "" {
		args = append(args, "--authkey="+cfg.AuthKey)
	}

	if err := s.cmd.Run("tailscale", args...); err != nil {
		return fmt.Errorf("tailscale up: %w", err)
	}

	s.refreshStatus()

	slog.Info("vpn: Tailscale active",
		"subnet", subnet,
		"exit_node", cfg.AdvertiseExitNode,
		"next_step", "approve route at tailscale.com/admin → Machines → Edit route settings",
	)
	return nil
}

// stop gracefully disconnects Tailscale.
//
//	tailscale down  — disconnects from the tailnet but keeps tailscaled running
func (s *VPN) stop() {
	slog.Info("vpn: stopping Tailscale")
	s.cmd.Run("tailscale", "down") //nolint:errcheck

	s.mu.Lock()
	s.status = Status{Enabled: false}
	s.mu.Unlock()
}

// refreshStatus calls `tailscale status --json` to get live state.
func (s *VPN) refreshStatus() {
	out, err := s.cmd.CombinedOutput("tailscale", "status", "--json")
	if err != nil {
		s.mu.Lock()
		s.status.TailscaleUp = false
		s.mu.Unlock()
		return
	}

	var ts struct {
		BackendState string `json:"BackendState"` // "Running" when connected
		Self         struct {
			TailscaleIPs []string `json:"TailscaleIPs"`
		} `json:"Self"`
		Peer map[string]struct{} `json:"Peer"`
	}
	if err := json.Unmarshal(out, &ts); err != nil {
		return
	}

	wifiStatus := s.wifiSvc.Status()
	subnet := ""
	if wifiStatus.Active {
		subnet = wifiStatus.SubnetBase + ".0/24"
	}

	tailscaleIP := ""
	if len(ts.Self.TailscaleIPs) > 0 {
		tailscaleIP = ts.Self.TailscaleIPs[0]
	}

	s.mu.Lock()
	s.status = Status{
		Enabled:          s.state.Enabled,
		TailscaleUp:      ts.BackendState == "Running",
		AdvertisedSubnet: subnet,
		TailscaleIP:      tailscaleIP,
		PeerCount:        len(ts.Peer),
		ExitNodeActive:   s.state.AdvertiseExitNode,
	}
	s.mu.Unlock()
}

// ─── Helpers ──────────────────────────────────────────────────────────────────

// maskAuthKey replaces the secret portion of a Tailscale auth key with ***
// so it's safe to return in API responses.
func maskAuthKey(key string) string {
	if key == "" {
		return ""
	}
	parts := strings.SplitN(key, "-", 3)
	if len(parts) < 3 {
		return "tskey-***"
	}
	return parts[0] + "-" + parts[1] + "-***"
}