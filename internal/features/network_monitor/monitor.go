package monitor

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"sync"
	"time"

	ping "github.com/prometheus-community/pro-bing"
)

// Config holds the details needed to report back to the VPS
type Config struct {
	DeviceID   string
	BackendURL string
	AuthToken  string
}

type NetworkMonitor struct {
	Config Config
	stats  MonitorStats
	mu     sync.RWMutex
	Target string
}

type MonitorStats struct {
	Latency   *float64  `json:"latency,omitempty"`   // ms
	Loss      *float64  `json:"loss,omitempty"`      // %
	Bandwidth *float64  `json:"bandwidth,omitempty"` // Pointer to Mbps
	Timestamp time.Time `json:"timestamp"`
	IsDown    *bool     `json:"is_down,omitempty"`
}

func New(cfg Config) *NetworkMonitor {
	return &NetworkMonitor{
		Target: "8.8.8.8",
		Config: cfg,
	}
}

// background worker
func (m *NetworkMonitor) Start() error {
	log.Printf("[MONITOR] Starting Network Health Monitor (Target: %s, Interval: 30s)", m.Target)

	// Run immediately on startup
	m.runPing()

	latencyTicker := time.NewTicker(30 * time.Second)
	// bandwidthTicker := time.NewTicker(30 * time.Second)
	bandwidthTicker := time.NewTicker(2 * time.Hour)

	// defer latencyTicker.Stop()
	// defer bandwidthTicker.Stop()

	// for {
	// 	select {
	// 	case <-latencyTicker.C:
	// 		stats, err := m.pingTarget()
	// 		if err != nil {
	// 			log.Printf("[MONITOR] Ping Execution Failed: %v", err)
	// 			continue
	// 		}

	// 		//! send 'stats' to backend.
	// 		// Because Bandwidth is nil, it won't overwrite DB data with 0.
	// 		if stats.IsDown != nil && *stats.IsDown {
	// 			log.Printf("[MONITOR] CRITICAL: Target is DOWN (Loss: %.2f%%)", *stats.Loss)
	// 		} else if stats.Latency != nil && *stats.Latency > 100.0 {
	// 			log.Printf("[MONITOR] High Latency: %.2f ms", *stats.Latency)
	// 		} else {
	// 			log.Printf("[MONITOR] Health OK: %.2f ms", *stats.Latency)
	// 		}

	// 	case <-bandwidthTicker.C:
	// 		stats, err := m.getBandwidth()
	// 		if err != nil {
	// 			log.Printf("[MONITOR] Bandwidth Test Failed: %v", err)
	// 			continue
	// 		}

	// 		//! send 'stats' to backend.
	// 		// Latency/Loss are nil, so they won't mess up the DB.

	// 		if stats.Bandwidth != nil {
	// 			log.Printf("[MONITOR] Bandwidth: %.2f Mbps", *stats.Bandwidth)
	// 		}
	// 	}
	// }

	go func() {
		for {
			select {
			case <-latencyTicker.C:
				m.runPing()
			case <-bandwidthTicker.C:
				m.runBandwidth()
			}
		}
	}()

	return nil

}

func (m *NetworkMonitor) HandleStats(w http.ResponseWriter, r *http.Request) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(m.stats)
}

func (m *NetworkMonitor) runPing() {
	stats, err := m.pingTarget()
	if err != nil {
		log.Printf("[MONITOR] Ping Execution Failed: %v", err)
		return
	}

	// 1. Update Local Cache (for local UI)
	m.mu.Lock()
	m.stats.Latency = stats.Latency
	m.stats.Loss = stats.Loss
	m.stats.IsDown = stats.IsDown
	m.stats.Timestamp = time.Now()
	m.mu.Unlock()

	// 2. Send to VPS Backend (Database)
	// We run this in a goroutine so it doesn't block the next ping
	go m.reportToBackend(*stats)
}


func (m *NetworkMonitor) runBandwidth() {
	stats, err := m.getBandwidth()
	if err != nil {
		log.Printf("[MONITOR] Bandwidth Test Failed: %v", err)
		return
	}

	m.mu.Lock()
	m.stats.Bandwidth = stats.Bandwidth
	m.mu.Unlock()

	go m.reportToBackend(*stats)
}


// reportToBackend sends the data to your separate VPS API
func (m *NetworkMonitor) reportToBackend(stats MonitorStats) {
	stats.Timestamp = time.Now()

	payload, err := json.Marshal(stats)
	if err != nil {
		return
	}

	url := fmt.Sprintf("%s/api/v1/device/agent/%s/network_metrics", m.Config.BackendURL, m.Config.DeviceID)

	req, err := http.NewRequest("POST", url, bytes.NewBuffer(payload))
	if err != nil {
		log.Printf("[MONITOR] Failed to create request: %v", err)
		return
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+m.Config.AuthToken) //! the auth token is for the frp tunnel, not the API auth middleware
	//! maybe auth the users into the device to have access to the token

	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		log.Printf("[MONITOR] Upload failed: %v", err)
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		log.Printf("[MONITOR] API Rejected Data: Status %d", resp.StatusCode)
	}
}


func (m *NetworkMonitor) pingTarget() (*MonitorStats, error) {
	pinger, err := ping.NewPinger(m.Target)
	if err != nil {
		return nil, err
	}

	pinger.SetPrivileged(true)
	pinger.Count = 3
	pinger.Timeout = 2 * time.Second

	err = pinger.Run()
	if err != nil {
		return nil, err
	}

	pStats := pinger.Statistics()

	latVal := float64(pStats.AvgRtt.Microseconds()) / 1000.0
	lossVal := pStats.PacketLoss
	isDownVal := pStats.PacketLoss >= 100.0

	return &MonitorStats{
		Latency:   &latVal,
		Loss:      &lossVal,
		IsDown:    &isDownVal,
		Bandwidth: nil,
	}, nil
}

func (m *NetworkMonitor) getBandwidth() (*MonitorStats, error) {
	testURL := "http://speedtest.tele2.net/10MB.zip"

	start := time.Now()

	client := http.Client{
		Timeout: 30 * time.Second,
	}

	resp, err := client.Get(testURL)
	if err != nil {
		return nil, fmt.Errorf("download start failed: %w", err)
	}
	defer resp.Body.Close()

	written, err := io.Copy(io.Discard, resp.Body)
	if err != nil {
		return nil, fmt.Errorf("download interrupted: %w", err)
	}

	duration := time.Since(start)

	bits := float64(written) * 8
	mbpsVal := (bits / 1_000_000) / duration.Seconds()

	return &MonitorStats{
		Latency:   nil,
		Loss:      nil,
		IsDown:    nil,
		Bandwidth: &mbpsVal,
	}, nil
}