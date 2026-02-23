// reads wifi.Status to know which dnsmasq instance to reload, then injects
// address= directives into /etc/dnsmasq.d/adblock.conf.
//
// How it works:
//  1. Downloads the StevenBlack unified hosts list (~100k domains)
//  2. Converts each "0.0.0.0 domain.com" line → "address=/domain.com/0.0.0.0"
//  3. Writes to /etc/dnsmasq.d/adblock.conf
//  4. Sends SIGHUP to dnsmasq (reload without restart — no DHCP lease loss)
//
// Blocklist updates are scheduled daily and can be triggered manually via
// POST /api/adblock/update.
package adblock

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/strct-org/strct-agent/internal/config"
	"github.com/strct-org/strct-agent/internal/platform/executil"
)

const blocklistURL = "https://raw.githubusercontent.com/StevenBlack/hosts/master/hosts"

const adblockConfPath = "/etc/dnsmasq.d/adblock.conf"


type AdBlockConfig struct {
	Enabled bool `json:"enabled"`

	UpdateSchedule string `json:"update_schedule"`
}

type Status struct {
	Enabled     bool      `json:"enabled"`
	EntryCount  int       `json:"entry_count"` // number of blocked domains
	LastUpdated time.Time `json:"last_updated"`
	UpdateError string    `json:"update_error,omitempty"`
	Updating    bool      `json:"updating"`
}


type AdBlock struct {
	cfg    config.Config
	state  AdBlockConfig
	status Status
	mu     sync.RWMutex
	cmd    executil.Runner
	client *http.Client
}

func New(cfg config.Config, cmd executil.Runner) *AdBlock {
	return &AdBlock{
		cfg: cfg,
		cmd: cmd,
		state: AdBlockConfig{
			Enabled:        false,
			UpdateSchedule: "daily",
		},
		client: &http.Client{
			Timeout: 60 * time.Second,
			Transport: &http.Transport{
				MaxIdleConnsPerHost: 1,
				IdleConnTimeout:     90 * time.Second,
			},
		},
	}
}

func NewFromConfig(cfg *config.Config) *AdBlock {
	var cmd executil.Runner
	if cfg.IsDev {
		cmd = executil.NewDevRunner()
	} else {
		cmd = executil.Real{}
	}
	return New(*cfg, cmd)
}

func (s *AdBlock) RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/adblock/config", s.handleGetConfig)
	mux.HandleFunc("POST /api/adblock/config", s.handleSetConfig)
	mux.HandleFunc("GET /api/adblock/status", s.handleGetStatus)
	mux.HandleFunc("POST /api/adblock/update", s.handleUpdate) // manual refresh
}

func (s *AdBlock) Start(ctx context.Context) error {
	slog.Info("adblock: service started")

	if count := countExistingEntries(); count > 0 {
		s.mu.Lock()
		s.status.EntryCount = count
		s.mu.Unlock()
	}

	go func() {
		for {
			s.mu.RLock()
			enabled := s.state.Enabled
			schedule := s.state.UpdateSchedule
			s.mu.RUnlock()

			interval := 24 * time.Hour
			if schedule == "weekly" {
				interval = 7 * 24 * time.Hour
			}

			select {
			case <-ctx.Done():
				if enabled {
					s.disable()
				}
				return
			case <-time.After(interval):
				if enabled {
					slog.Info("adblock: scheduled blocklist update")
					s.downloadAndApply()
				}
			}
		}
	}()

	return nil
}


func (s *AdBlock) handleGetConfig(w http.ResponseWriter, r *http.Request) {
	s.mu.RLock()
	state := s.state
	s.mu.RUnlock()
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(state)
}

func (s *AdBlock) handleSetConfig(w http.ResponseWriter, r *http.Request) {
	var req AdBlockConfig
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid JSON", http.StatusBadRequest)
		return
	}

	s.mu.RLock()
	wasEnabled := s.state.Enabled
	s.mu.RUnlock()

	s.mu.Lock()
	s.state = req
	s.mu.Unlock()

	go func() {
		if req.Enabled && !wasEnabled {
			// Just enabled — download blocklist immediately
			s.downloadAndApply()
		} else if !req.Enabled && wasEnabled {
			// Just disabled — remove blocklist and reload dnsmasq
			s.disable()
		}
	}()

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"status": "applying"})
}

func (s *AdBlock) handleGetStatus(w http.ResponseWriter, r *http.Request) {
	s.mu.RLock()
	st := s.status
	s.mu.RUnlock()
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(st)
}

func (s *AdBlock) handleUpdate(w http.ResponseWriter, r *http.Request) {
	s.mu.RLock()
	enabled := s.state.Enabled
	s.mu.RUnlock()

	if !enabled {
		http.Error(w, "ad blocking is not enabled", http.StatusBadRequest)
		return
	}

	go s.downloadAndApply()

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"status": "updating"})
}

// ─── Core logic ───────────────────────────────────────────────────────────────

// downloadAndApply fetches the StevenBlack hosts list and applies it to dnsmasq.
//
// Conversion:
//
//	hosts file:    0.0.0.0 doubleclick.net
//	dnsmasq conf:  address=/doubleclick.net/0.0.0.0
//
// The address= directive makes dnsmasq return 0.0.0.0 for any DNS query
// matching the domain — the browser/app gets "connection refused" immediately
// instead of loading the ad server.
//
// dnsmasq is reloaded with SIGHUP rather than a full restart, so existing
// DHCP leases are preserved and connected devices aren't interrupted.
func (s *AdBlock) downloadAndApply() {
	s.mu.Lock()
	if s.status.Updating {
		s.mu.Unlock()
		return // already running
	}
	s.status.Updating = true
	s.mu.Unlock()

	defer func() {
		s.mu.Lock()
		s.status.Updating = false
		s.mu.Unlock()
	}()

	slog.Info("adblock: downloading StevenBlack/hosts blocklist", "url", blocklistURL)

	resp, err := s.client.Get(blocklistURL)
	if err != nil {
		s.setError(fmt.Sprintf("download failed: %v", err))
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		s.setError(fmt.Sprintf("download returned %d", resp.StatusCode))
		return
	}

	// Stream-parse the hosts file to avoid loading the whole ~3MB into memory at once
	count, err := s.writeAdblockConf(resp.Body)
	if err != nil {
		s.setError(fmt.Sprintf("write adblock.conf: %v", err))
		return
	}

	// Reload dnsmasq: SIGHUP triggers a config reload without restarting.
	// The daemon re-reads all files in /etc/dnsmasq.d/ including adblock.conf.
	// Existing DHCP leases are NOT affected.
	if err := s.cmd.Run("systemctl", "kill", "-s", "HUP", "dnsmasq"); err != nil {
		slog.Warn("adblock: dnsmasq HUP failed, trying restart", "err", err)
		s.cmd.Run("systemctl", "restart", "dnsmasq") //nolint:errcheck
	}

	s.mu.Lock()
	s.status.Enabled = true
	s.status.EntryCount = count
	s.status.LastUpdated = time.Now()
	s.status.UpdateError = ""
	s.mu.Unlock()

	slog.Info("adblock: blocklist applied", "domains_blocked", count)
}

// writeAdblockConf streams the hosts file and writes dnsmasq address= directives.
// Returns the number of entries written.
func (s *AdBlock) writeAdblockConf(body io.Reader) (int, error) {
	f, err := os.CreateTemp("", "adblock-*.conf")
	if err != nil {
		return 0, err
	}
	tmpPath := f.Name()
	defer func() {
		f.Close()
		// Clean up temp file if we didn't rename it
		os.Remove(tmpPath) //nolint:errcheck
	}()

	w := bufio.NewWriterSize(f, 256*1024) // 256KB write buffer for performance
	fmt.Fprintf(w, "# Ad block — generated by strct-agent from StevenBlack/hosts\n")
	fmt.Fprintf(w, "# Updated: %s\n", time.Now().Format(time.RFC3339))

	scanner := bufio.NewScanner(body)
	scanner.Buffer(make([]byte, 64*1024), 64*1024)

	count := 0
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())

		// Skip comments and empty lines
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}

		// hosts format: "0.0.0.0 domain.com [# optional comment]"
		fields := strings.Fields(line)
		if len(fields) < 2 || fields[0] != "0.0.0.0" {
			continue
		}

		domain := fields[1]
		// Skip meta-entries
		if domain == "0.0.0.0" || domain == "localhost" || domain == "local" || domain == "localhost.localdomain" {
			continue
		}

		fmt.Fprintf(w, "address=/%s/0.0.0.0\n", domain)
		count++
	}
	if err := scanner.Err(); err != nil {
		return 0, fmt.Errorf("read hosts: %w", err)
	}

	if err := w.Flush(); err != nil {
		return 0, fmt.Errorf("flush: %w", err)
	}
	if err := f.Close(); err != nil {
		return 0, err
	}

	// Ensure the dnsmasq drop-in directory exists
	if err := os.MkdirAll(filepath.Dir(adblockConfPath), 0755); err != nil {
		return 0, fmt.Errorf("mkdir %s: %w", filepath.Dir(adblockConfPath), err)
	}

	// Atomic replace
	if err := os.Rename(tmpPath, adblockConfPath); err != nil {
		return 0, fmt.Errorf("rename to %s: %w", adblockConfPath, err)
	}

	return count, nil
}

// disable removes the adblock conf file and reloads dnsmasq.
func (s *AdBlock) disable() {
	slog.Info("adblock: disabling")
	os.Remove(adblockConfPath) //nolint:errcheck

	if err := s.cmd.Run("systemctl", "kill", "-s", "HUP", "dnsmasq"); err != nil {
		s.cmd.Run("systemctl", "restart", "dnsmasq") //nolint:errcheck
	}

	s.mu.Lock()
	s.status = Status{Enabled: false}
	s.mu.Unlock()

	slog.Info("adblock: disabled, dnsmasq reloaded")
}

func (s *AdBlock) setError(msg string) {
	slog.Error("adblock: " + msg)
	s.mu.Lock()
	s.status.UpdateError = msg
	s.mu.Unlock()
}

func countExistingEntries() int {
	f, err := os.Open(adblockConfPath)
	if err != nil {
		return 0
	}
	defer f.Close()

	count := 0
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		if strings.HasPrefix(scanner.Text(), "address=") {
			count++
		}
	}
	return count
}
