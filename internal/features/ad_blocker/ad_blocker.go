package adblocker

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os/exec"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/miekg/dns"
)

const (
	upstreamDNS = "1.1.1.1:53"
	addrDNS     = ":5354"
	maxLogSize  = 50
)

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

func New(cfg AdBlockConfig) *AdBlocker {
	return &AdBlocker{
		Config:     cfg,
		blocklist:  make(map[string]bool),
		enabled:    true,
		trafficMap: make(map[string]*TrafficPoint),
		logs:       make([]BlockLog, 0),
	}
}

func NewDefault() *AdBlocker {
    return New(AdBlockConfig{})
}

// every feature initiaalizs its own routes
func (a *AdBlocker) RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("/api/adblock/stats", a.HandleStats)
	mux.HandleFunc("/api/adblock/toggle", a.HandleToggle)
}

// ! implement canceling loginc with ctx context.Context
func (a *AdBlocker) Start(ctx context.Context) error {
	log.Println("[AD_BLOCKER] Starting Ad Blocker Service")

	// 1. Apply IPTables Rules to redirect traffic from 53 -> 5354
	// This makes devices think they are talking to port 53, but Linux sends it to us.
	go func() {
		log.Println("[AD_BLOCKER] Applying iptables redirection rules...")
		// Redirect UDP
		exec.Command("iptables", "-t", "nat", "-A", "PREROUTING", "-p", "udp", "--dport", "53", "-j", "REDIRECT", "--to-port", "5354").Run()
		// Redirect TCP (some DNS uses TCP)
		exec.Command("iptables", "-t", "nat", "-A", "PREROUTING", "-p", "tcp", "--dport", "53", "-j", "REDIRECT", "--to-port", "5354").Run()
	}()

	// 2. Start Blocklist Updater
	go func() {
		a.updateBlocklist()
		ticker := time.NewTicker(24 * time.Hour)
		for range ticker.C {
			a.updateBlocklist()
		}
	}()

	// 3. Start DNS Server
	a.dnsServer = &dns.Server{
		Addr:    addrDNS,
		Net:     "udp",
		Handler: a, // Use 'a' as the handler (calls a.ServeDNS)
	}

	log.Printf("[AD_BLOCKER] DNS Listener running on %s (Redirected from 53) -> %s", addrDNS, upstreamDNS)

	go func() {
		if err := a.dnsServer.ListenAndServe(); err != nil {
			log.Fatalf("[AD_BLOCKER] Failed to start server: %s\n", err.Error())
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
			log.Printf("[BLOCKED] %s", domain)
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
		log.Printf("[AD_BLOCKER] Forwarding error: %v", err)
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
	log.Println("[AD_BLOCKER] Downloading blocklist...")
	client := http.Client{Timeout: 30 * time.Second}
	resp, err := client.Get("https://raw.githubusercontent.com/StevenBlack/hosts/master/hosts")
	if err != nil {
		log.Printf("[AD_BLOCKER] Update failed: %v", err)
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
	log.Printf("[AD_BLOCKER] Loaded %d domains.", count)
}
