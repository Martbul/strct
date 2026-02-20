package adblocker

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/miekg/dns"
	"github.com/strct-org/strct-agent/internal/platform/executil"
)

const (
	upstreamDNS = "1.1.1.1:53"
	addrDNS     = ":5354"
	maxLogSize  = 50
)

type commander interface {
	Run(name string, args ...string) error
}

type AdBlocker struct {
	Config         AdBlockConfig
	blocklist      map[string]bool
	mu             sync.RWMutex
	enabled        bool
	totalQueries   int64
	blockedQueries int64
	logs           []BlockLog
	trafficMap     map[string]*TrafficPoint
	dnsServer      *dns.Server
	cmd            commander
}

type AdBlockConfig struct{}

type AdBlockStats struct {
	TotalQueries   int64          `json:"total_queries"`
	BlockedQueries int64          `json:"blocked_queries"`
	BlockRatio     float64        `json:"block_ratio"`
	IsEnabled      bool           `json:"is_enabled"`
	ChartData      []TrafficPoint `json:"chart_data"`
	RecentLogs     []BlockLog     `json:"recent_logs"`
}

type TrafficPoint struct {
	Time    string `json:"time"`
	Total   int    `json:"total"`
	Blocked int    `json:"blocked"`
}

type BlockLog struct {
	Domain    string `json:"domain"`
	Time      string `json:"time"`
	Timestamp int64  `json:"-"`
}

func New(cmd commander) *AdBlocker {
	return &AdBlocker{
		cmd:        cmd,
		blocklist:  make(map[string]bool),
		enabled:    true,
		trafficMap: make(map[string]*TrafficPoint),
		logs:       make([]BlockLog, 0),
	}
}

// func New(cfg AdBlockConfig) *AdBlocker {
// 	return &AdBlocker{
// 		Config:     cfg,
// 		blocklist:  make(map[string]bool),
// 		enabled:    true,
// 		trafficMap: make(map[string]*TrafficPoint),
// 		logs:       make([]BlockLog, 0),
// 	}
// }

// NewDefault is what main.go calls — injects the real OS runner.
func NewDefault() *AdBlocker {
	return New(executil.Real{})
}

// func NewDefault() *AdBlocker {
// 	return New(AdBlockConfig{})
// }

// every feature initiaalizs its own routes
func (a *AdBlocker) RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/adblock/stats", a.HandleStats)
	mux.HandleFunc("POST /api/adblock/toggle", a.HandleToggle)
}

func (a *AdBlocker) Start(ctx context.Context) error {
	slog.Info("adblocker: starting")

	// go func() {
	// 	// Redirect UDP
	// 	exec.Command("iptables", "-t", "nat", "-A", "PREROUTING", "-p", "udp", "--dport", "53", "-j", "REDIRECT", "--to-port", "5354").Run()
	// 	// Redirect TCP (some DNS uses TCP)
	// 	exec.Command("iptables", "-t", "nat", "-A", "PREROUTING", "-p", "tcp", "--dport", "53", "-j", "REDIRECT", "--to-port", "5354").Run()
	// }()

	// 1. Apply IPTables Rules to redirect traffic from 53 -> 5354
	go func() {
		select {
		case <-ctx.Done():
			return
		default:
		}
		slog.Info("adblocker: applying iptables redirection rules")
		a.cmd.Run("iptables", "-t", "nat", "-A", "PREROUTING", "-p", "udp", "--dport", "53", "-j", "REDIRECT", "--to-port", "5354")
		a.cmd.Run("iptables", "-t", "nat", "-A", "PREROUTING", "-p", "tcp", "--dport", "53", "-j", "REDIRECT", "--to-port", "5354")
	}()

	// Blocklist Updater
	go func() {
		a.updateBlocklist()
		ticker := time.NewTicker(24 * time.Hour)
		defer ticker.Stop()

		for {
			select {
			case <-ctx.Done():
				slog.Info("adblocker: blocklist updater stopped")
				return
			case <-ticker.C:
				a.updateBlocklist()
			}
		}
	}()

	// DNS Server
	a.dnsServer = &dns.Server{
		Addr:    addrDNS,
		Net:     "udp",
		Handler: a,
	}

	// Shutdown watcher: when ctx is cancelled, gracefully stop the DNS server.
	go func() {
		<-ctx.Done()
		slog.Info("adblocker: shutting down DNS server")
		if err := a.dnsServer.Shutdown(); err != nil {
			slog.Error("adblocker: DNS server shutdown error", "err", err)
		}
		// Clean up iptables rules so port 53 works normally after exit.
		a.cmd.Run("iptables", "-t", "nat", "-D", "PREROUTING", "-p", "udp", "--dport", "53", "-j", "REDIRECT", "--to-port", "5354")
		a.cmd.Run("iptables", "-t", "nat", "-D", "PREROUTING", "-p", "tcp", "--dport", "53", "-j", "REDIRECT", "--to-port", "5354")
	}()

	go func() {
		slog.Info("adblocker: DNS listener starting", "addr", addrDNS, "upstream", upstreamDNS)
		if err := a.dnsServer.ListenAndServe(); err != nil {
			// dns.Server.Shutdown() causes ListenAndServe to return an error.
			// Only log if it wasn't an intentional shutdown.
			if ctx.Err() == nil {
				slog.Error("adblocker: DNS server crashed", "err", err)
			}
		}
	}()

	return nil
}

func (a *AdBlocker) ServeDNS(w dns.ResponseWriter, r *dns.Msg) {
	m := new(dns.Msg)
	m.SetReply(r)
	m.Compress = false

	a.mu.Lock()

	// If disabled, just forward immediately
	if !a.enabled {
		a.mu.Unlock()
		a.forwardDNS(w, r, m)
		return
	}

	for _, q := range r.Question {
		domain := strings.TrimSuffix(q.Name, ".")
		a.totalQueries++

		// Track traffic for charts
		now := time.Now().Format("15:00") // Group by hour:minute
		if _, ok := a.trafficMap[now]; !ok {
			a.trafficMap[now] = &TrafficPoint{Time: now}
		}
		a.trafficMap[now].Total++

		// Check Blocklist
		if a.blocklist[domain] {
			slog.Info("adblocker: blocked query", "domain", domain)
			a.blockedQueries++
			a.trafficMap[now].Blocked++

			// Add to logs
			a.logs = append([]BlockLog{{
				Domain:    domain,
				Time:      time.Now().Format("15:04:05"),
				Timestamp: time.Now().Unix(),
			}}, a.logs...)

			if len(a.logs) > maxLogSize {
				a.logs = a.logs[:maxLogSize]
			}

			// Respond with 0.0.0.0 (Block)
			rr, _ := dns.NewRR(fmt.Sprintf("%s A 0.0.0.0", q.Name))
			m.Answer = append(m.Answer, rr)
		} else {
			// Not blocked, we need to forward
			a.mu.Unlock() // Unlock before network call
			a.forwardDNS(w, r, m)
			return // forwardDNS handles the write
		}
	}

	a.mu.Unlock()
	w.WriteMsg(m)
}

// Helper to forward legitimate traffic
func (a *AdBlocker) forwardDNS(w dns.ResponseWriter, r *dns.Msg, m *dns.Msg) {
	resp, err := dns.Exchange(r, upstreamDNS)
	if err == nil {
		m.Answer = resp.Answer
		m.Ns = resp.Ns
		m.Extra = resp.Extra
	} else {
		slog.Error("adblocker: DNS forwarding error", "err", err)
	}
	w.WriteMsg(m)
}

// 4. API Handlers (Stats & Toggle)
func (a *AdBlocker) HandleStats(w http.ResponseWriter, r *http.Request) {
	a.mu.RLock()
	defer a.mu.RUnlock()

	var ratio float64
	if a.totalQueries > 0 {
		ratio = (float64(a.blockedQueries) / float64(a.totalQueries)) * 100
	}

	var chartData []TrafficPoint
	for _, v := range a.trafficMap {
		chartData = append(chartData, *v)
	}
	sort.Slice(chartData, func(i, j int) bool {
		return chartData[i].Time < chartData[j].Time
	})

	json.NewEncoder(w).Encode(AdBlockStats{
		TotalQueries:   a.totalQueries,
		BlockedQueries: a.blockedQueries,
		BlockRatio:     ratio,
		IsEnabled:      a.enabled,
		ChartData:      chartData,
		RecentLogs:     a.logs,
	})
}

func (a *AdBlocker) HandleToggle(w http.ResponseWriter, r *http.Request) {
	a.mu.Lock()
	a.enabled = !a.enabled
	status := a.enabled
	a.mu.Unlock()
	json.NewEncoder(w).Encode(map[string]bool{"is_enabled": status})
}

// 5. Blocklist Updater
func (a *AdBlocker) updateBlocklist() {
	slog.Info("adblocker: updating blocklist from source", "url", "https://raw.githubusercontent.com/StevenBlack/hosts/master/hosts")
	client := http.Client{Timeout: 30 * time.Second}
	resp, err := client.Get("https://raw.githubusercontent.com/StevenBlack/hosts/master/hosts")
	if err != nil {
		slog.Error("adblocker: blocklist update failed", "err", err)
		return
	}
	defer resp.Body.Close()

	scanner := bufio.NewScanner(resp.Body)
	newList := make(map[string]bool)
	count := 0
	for scanner.Scan() {
		line := scanner.Text()
		if strings.HasPrefix(line, "#") || line == "" {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) >= 2 {
			newList[fields[1]] = true
			count++
		}
	}

	a.mu.Lock()
	a.blocklist = newList
	a.mu.Unlock()
	slog.Info("adblocker: blocklist updated", "domains_loaded", count)
}
