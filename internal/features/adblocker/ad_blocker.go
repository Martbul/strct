// package adblocker

// import (
// 	"bufio"
// 	"context"
// 	"encoding/json"
// 	"fmt"
// 	"log/slog"
// 	"net/http"
// 	"sort"
// 	"strings"
// 	"sync"
// 	"time"

// 	"github.com/miekg/dns"
// 	"github.com/strct-org/strct-agent/internal/platform/executil"
// )

// const (
// 	upstreamDNS = "1.1.1.1:53"
// 	addrDNS     = ":5354"
// 	maxLogSize  = 50
// )

// type commander interface {
// 	Run(name string, args ...string) error
// }

// type AdBlocker struct {
// 	Config         AdBlockConfig
// 	blocklist      map[string]bool
// 	mu             sync.RWMutex
// 	enabled        bool
// 	totalQueries   int64
// 	blockedQueries int64
// 	logs           []BlockLog
// 	trafficMap     map[string]*TrafficPoint
// 	dnsServer      *dns.Server
// 	cmd            commander
// }

// type AdBlockConfig struct{}

// type AdBlockStats struct {
// 	TotalQueries   int64          `json:"total_queries"`
// 	BlockedQueries int64          `json:"blocked_queries"`
// 	BlockRatio     float64        `json:"block_ratio"`
// 	IsEnabled      bool           `json:"is_enabled"`
// 	ChartData      []TrafficPoint `json:"chart_data"`
// 	RecentLogs     []BlockLog     `json:"recent_logs"`
// }

// type TrafficPoint struct {
// 	Time    string `json:"time"`
// 	Total   int    `json:"total"`
// 	Blocked int    `json:"blocked"`
// }

// type BlockLog struct {
// 	Domain    string `json:"domain"`
// 	Time      string `json:"time"`
// 	Timestamp int64  `json:"-"`
// }

// func New(cmd commander) *AdBlocker {
// 	return &AdBlocker{
// 		cmd:        cmd,
// 		blocklist:  make(map[string]bool),
// 		enabled:    true,
// 		trafficMap: make(map[string]*TrafficPoint),
// 		logs:       make([]BlockLog, 0),
// 	}
// }

// // func New(cfg AdBlockConfig) *AdBlocker {
// // 	return &AdBlocker{
// // 		Config:     cfg,
// // 		blocklist:  make(map[string]bool),
// // 		enabled:    true,
// // 		trafficMap: make(map[string]*TrafficPoint),
// // 		logs:       make([]BlockLog, 0),
// // 	}
// // }

// // NewDefault is what main.go calls — injects the real OS runner.
// func NewDefault() *AdBlocker {
// 	return New(executil.Real{})
// }

// // func NewDefault() *AdBlocker {
// // 	return New(AdBlockConfig{})
// // }

// // every feature initiaalizs its own routes
// func (a *AdBlocker) RegisterRoutes(mux *http.ServeMux) {
// 	mux.HandleFunc("GET /api/adblock/stats", a.HandleStats)
// 	mux.HandleFunc("POST /api/adblock/toggle", a.HandleToggle)
// }

// func (a *AdBlocker) Start(ctx context.Context) error {
// 	slog.Info("adblocker: starting")

// 	// go func() {
// 	// 	// Redirect UDP
// 	// 	exec.Command("iptables", "-t", "nat", "-A", "PREROUTING", "-p", "udp", "--dport", "53", "-j", "REDIRECT", "--to-port", "5354").Run()
// 	// 	// Redirect TCP (some DNS uses TCP)
// 	// 	exec.Command("iptables", "-t", "nat", "-A", "PREROUTING", "-p", "tcp", "--dport", "53", "-j", "REDIRECT", "--to-port", "5354").Run()
// 	// }()

// 	// 1. Apply IPTables Rules to redirect traffic from 53 -> 5354
// 	go func() {
// 		select {
// 		case <-ctx.Done():
// 			return
// 		default:
// 		}
// 		slog.Info("adblocker: applying iptables redirection rules")
// 		a.cmd.Run("iptables", "-t", "nat", "-A", "PREROUTING", "-p", "udp", "--dport", "53", "-j", "REDIRECT", "--to-port", "5354")
// 		a.cmd.Run("iptables", "-t", "nat", "-A", "PREROUTING", "-p", "tcp", "--dport", "53", "-j", "REDIRECT", "--to-port", "5354")
// 	}()

// 	// Blocklist Updater
// 	go func() {
// 		a.updateBlocklist()
// 		ticker := time.NewTicker(24 * time.Hour)
// 		defer ticker.Stop()

// 		for {
// 			select {
// 			case <-ctx.Done():
// 				slog.Info("adblocker: blocklist updater stopped")
// 				return
// 			case <-ticker.C:
// 				a.updateBlocklist()
// 			}
// 		}
// 	}()

// 	// DNS Server
// 	a.dnsServer = &dns.Server{
// 		Addr:    addrDNS,
// 		Net:     "udp",
// 		Handler: a,
// 	}

// 	// Shutdown watcher: when ctx is cancelled, gracefully stop the DNS server.
// 	go func() {
// 		<-ctx.Done()
// 		slog.Info("adblocker: shutting down DNS server")
// 		if err := a.dnsServer.Shutdown(); err != nil {
// 			slog.Error("adblocker: DNS server shutdown error", "err", err)
// 		}
// 		// Clean up iptables rules so port 53 works normally after exit.
// 		a.cmd.Run("iptables", "-t", "nat", "-D", "PREROUTING", "-p", "udp", "--dport", "53", "-j", "REDIRECT", "--to-port", "5354")
// 		a.cmd.Run("iptables", "-t", "nat", "-D", "PREROUTING", "-p", "tcp", "--dport", "53", "-j", "REDIRECT", "--to-port", "5354")
// 	}()

// 	go func() {
// 		slog.Info("adblocker: DNS listener starting", "addr", addrDNS, "upstream", upstreamDNS)
// 		if err := a.dnsServer.ListenAndServe(); err != nil {
// 			// dns.Server.Shutdown() causes ListenAndServe to return an error.
// 			// Only log if it wasn't an intentional shutdown.
// 			if ctx.Err() == nil {
// 				slog.Error("adblocker: DNS server crashed", "err", err)
// 			}
// 		}
// 	}()

// 	return nil
// }

// func (a *AdBlocker) ServeDNS(w dns.ResponseWriter, r *dns.Msg) {
// 	m := new(dns.Msg)
// 	m.SetReply(r)
// 	m.Compress = false

// 	a.mu.Lock()

// 	// If disabled, just forward immediately
// 	if !a.enabled {
// 		a.mu.Unlock()
// 		a.forwardDNS(w, r, m)
// 		return
// 	}

// 	for _, q := range r.Question {
// 		domain := strings.TrimSuffix(q.Name, ".")
// 		a.totalQueries++

// 		// Track traffic for charts
// 		now := time.Now().Format("15:00") // Group by hour:minute
// 		if _, ok := a.trafficMap[now]; !ok {
// 			a.trafficMap[now] = &TrafficPoint{Time: now}
// 		}
// 		a.trafficMap[now].Total++

// 		// Check Blocklist
// 		if a.blocklist[domain] {
// 			slog.Info("adblocker: blocked query", "domain", domain)
// 			a.blockedQueries++
// 			a.trafficMap[now].Blocked++

// 			// Add to logs
// 			a.logs = append([]BlockLog{{
// 				Domain:    domain,
// 				Time:      time.Now().Format("15:04:05"),
// 				Timestamp: time.Now().Unix(),
// 			}}, a.logs...)

// 			if len(a.logs) > maxLogSize {
// 				a.logs = a.logs[:maxLogSize]
// 			}

// 			// Respond with 0.0.0.0 (Block)
// 			rr, _ := dns.NewRR(fmt.Sprintf("%s A 0.0.0.0", q.Name))
// 			m.Answer = append(m.Answer, rr)
// 		} else {
// 			// Not blocked, we need to forward
// 			a.mu.Unlock() // Unlock before network call
// 			a.forwardDNS(w, r, m)
// 			return // forwardDNS handles the write
// 		}
// 	}

// 	a.mu.Unlock()
// 	w.WriteMsg(m)
// }

// // Helper to forward legitimate traffic
// func (a *AdBlocker) forwardDNS(w dns.ResponseWriter, r *dns.Msg, m *dns.Msg) {
// 	resp, err := dns.Exchange(r, upstreamDNS)
// 	if err == nil {
// 		m.Answer = resp.Answer
// 		m.Ns = resp.Ns
// 		m.Extra = resp.Extra
// 	} else {
// 		slog.Error("adblocker: DNS forwarding error", "err", err)
// 	}
// 	w.WriteMsg(m)
// }

// // 4. API Handlers (Stats & Toggle)
// func (a *AdBlocker) HandleStats(w http.ResponseWriter, r *http.Request) {
// 	a.mu.RLock()
// 	defer a.mu.RUnlock()

// 	var ratio float64
// 	if a.totalQueries > 0 {
// 		ratio = (float64(a.blockedQueries) / float64(a.totalQueries)) * 100
// 	}

// 	var chartData []TrafficPoint
// 	for _, v := range a.trafficMap {
// 		chartData = append(chartData, *v)
// 	}
// 	sort.Slice(chartData, func(i, j int) bool {
// 		return chartData[i].Time < chartData[j].Time
// 	})

// 	json.NewEncoder(w).Encode(AdBlockStats{
// 		TotalQueries:   a.totalQueries,
// 		BlockedQueries: a.blockedQueries,
// 		BlockRatio:     ratio,
// 		IsEnabled:      a.enabled,
// 		ChartData:      chartData,
// 		RecentLogs:     a.logs,
// 	})
// }

// func (a *AdBlocker) HandleToggle(w http.ResponseWriter, r *http.Request) {
// 	a.mu.Lock()
// 	a.enabled = !a.enabled
// 	status := a.enabled
// 	a.mu.Unlock()
// 	json.NewEncoder(w).Encode(map[string]bool{"is_enabled": status})
// }

// // 5. Blocklist Updater
// func (a *AdBlocker) updateBlocklist() {
// 	slog.Info("adblocker: updating blocklist from source", "url", "https://raw.githubusercontent.com/StevenBlack/hosts/master/hosts")
// 	client := http.Client{Timeout: 30 * time.Second}
// 	resp, err := client.Get("https://raw.githubusercontent.com/StevenBlack/hosts/master/hosts")
// 	if err != nil {
// 		slog.Error("adblocker: blocklist update failed", "err", err)
// 		return
// 	}
// 	defer resp.Body.Close()

// 	scanner := bufio.NewScanner(resp.Body)
// 	newList := make(map[string]bool)
// 	count := 0
// 	for scanner.Scan() {
// 		line := scanner.Text()
// 		if strings.HasPrefix(line, "#") || line == "" {
// 			continue
// 		}
// 		fields := strings.Fields(line)
// 		if len(fields) >= 2 {
// 			newList[fields[1]] = true
// 			count++
// 		}
// 	}

// 	a.mu.Lock()
// 	a.blocklist = newList
// 	a.mu.Unlock()
// 	slog.Info("adblocker: blocklist updated", "domains_loaded", count)
// }
// Package adblock manages DNS-level ad and tracker blocking via dnsmasq.
//
// This package is completely independent of wifi and vpn — it reads
// wifi.Status to know which dnsmasq instance to reload, then injects
// address= directives into /etc/dnsmasq.d/adblock.conf.
//
// How it works:
//   1. Downloads the StevenBlack unified hosts list (~100k domains)
//   2. Converts each "0.0.0.0 domain.com" line → "address=/domain.com/0.0.0.0"
//   3. Writes to /etc/dnsmasq.d/adblock.conf
//   4. Sends SIGHUP to dnsmasq (reload without restart — no DHCP lease loss)
//
// Every device connected to the AP gets blocking automatically,
// including TVs, consoles, and anything that can't run a browser extension.
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
	"strings"
	"sync"
	"time"

	"github.com/strct-org/strct-agent/internal/config"
	"github.com/strct-org/strct-agent/internal/platform/executil"
)

// blocklist source — StevenBlack unified hosts (ads + trackers + malware)
// https://github.com/StevenBlack/hosts
const blocklistURL = "https://raw.githubusercontent.com/StevenBlack/hosts/master/hosts"

const adblockConfPath = "/etc/dnsmasq.d/adblock.conf"

// ─── Types ────────────────────────────────────────────────────────────────────

type AdBlockConfig struct {
	Enabled bool `json:"enabled"`

	// UpdateSchedule controls how often the blocklist is refreshed.
	// "daily" | "weekly" | "manual"
	UpdateSchedule string `json:"update_schedule"`
}

type Status struct {
	Enabled     bool      `json:"enabled"`
	EntryCount  int       `json:"entry_count"`  // number of blocked domains
	LastUpdated time.Time `json:"last_updated"`
	UpdateError string    `json:"update_error,omitempty"`
	Updating    bool      `json:"updating"`
}

// ─── Service ──────────────────────────────────────────────────────────────────

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
			Timeout: 60 * time.Second, // blocklist download can be slow on first run
			Transport: &http.Transport{
				MaxIdleConnsPerHost: 1,
				IdleConnTimeout:     90 * time.Second,
			},
		},
	}
}

func NewFromConfig(cfg *config.Config) *AdBlock {
	return New(*cfg, executil.Real{})
}

func (s *AdBlock) RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/adblock/config",  s.handleGetConfig)
	mux.HandleFunc("POST /api/adblock/config", s.handleSetConfig)
	mux.HandleFunc("GET /api/adblock/status",  s.handleGetStatus)
	mux.HandleFunc("POST /api/adblock/update", s.handleUpdate) // manual refresh
}

func (s *AdBlock) Start(ctx context.Context) error {
	slog.Info("adblock: service started")

	// Load entry count from existing conf file (survives restarts)
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

// ─── HTTP handlers ────────────────────────────────────────────────────────────

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

	// Atomic replace: rename temp file into place so dnsmasq never reads a partial file
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
	slog.Error("adblock: "+msg)
	s.mu.Lock()
	s.status.UpdateError = msg
	s.mu.Unlock()
}

// ─── Helpers ──────────────────────────────────────────────────────────────────

// countExistingEntries reads the current adblock.conf and counts address= lines.
// Used on startup to restore status.EntryCount without re-downloading.
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