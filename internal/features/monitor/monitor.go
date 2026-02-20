package monitor

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"sync"
	"time"

	ping "github.com/prometheus-community/pro-bing"
	"github.com/strct-org/strct-agent/internal/config"
)

type MonitorConfig struct {
	DeviceID   string
	BackendURL string
	AuthToken  string
}

type NetworkMonitor struct {
	Config MonitorConfig
	stats  MonitorStats
	mu     sync.RWMutex
	Target string
	client *http.Client
}

type MonitorStats struct {
	Timestamp time.Time `json:"timestamp"`
	Latency   *float64  `json:"latency,omitempty"`   // ms
	Loss      *float64  `json:"loss,omitempty"`      // %
	Bandwidth *float64  `json:"bandwidth,omitempty"` // Pointer to Mbps
	IsDown    *bool     `json:"is_down,omitempty"`
}

func New(cfg MonitorConfig) *NetworkMonitor {
	return &NetworkMonitor{
		Target: "8.8.8.8",
		Config: cfg,
		client: &http.Client{
			Timeout: 10 * time.Second,
			Transport: &http.Transport{
				MaxIdleConnsPerHost: 2,
				IdleConnTimeout:     90 * time.Second,
			},
		},
	}
}

func NewFromConfig(cfg *config.Config) *NetworkMonitor {
	return New(MonitorConfig{
		DeviceID:   cfg.DeviceID,
		BackendURL: cfg.EffectiveBackendURL(),
		AuthToken:  cfg.AuthToken,
	})
}

func (m *NetworkMonitor) RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/network/stats", m.HandleStats)
	mux.HandleFunc("POST /api/network/speedtest", m.HandleSpeedtest)
}

func (m *NetworkMonitor) Start(ctx context.Context) error {
	slog.Info("monitor: starting", "target", m.Target)

	// Run immediately on start, then on schedule
	m.runPing()
	m.runBandwidth()

	go func() {
		latencyTicker := time.NewTicker(120 * time.Second)
		bandwidthTicker := time.NewTicker(2 * time.Hour)
		defer latencyTicker.Stop()
		defer bandwidthTicker.Stop()

		for {
			select {
			case <-ctx.Done():
				slog.Info("monitor: stopped")
				return
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

func (m *NetworkMonitor) HandleSpeedtest(w http.ResponseWriter, r *http.Request) {
	slog.Info("monitor: Triggered via API")

	go func() {
		m.runPing()
		m.runBandwidth()
	}()

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(map[string]string{"status": "speedtest_initiated"})
}

func (m *NetworkMonitor) runPing() {
	slog.Info("runPing")

	stats, err := m.pingTarget()
	if err != nil {
		slog.Error("monitor: ping failed", "err", err)
		return
	}

	m.mu.Lock()
	m.stats.Latency = stats.Latency
	m.stats.Loss = stats.Loss
	m.stats.IsDown = stats.IsDown
	m.stats.Timestamp = time.Now()
	m.mu.Unlock()

	go m.reportToBackend(*stats)
}

func (m *NetworkMonitor) runBandwidth() {
	slog.Info("runBandwidth")

	stats, err := m.getBandwidth()
	if err != nil {
		slog.Error("monitor: bandwidth failed", "err", err)

		return
	}

	m.mu.Lock()
	m.stats.Bandwidth = stats.Bandwidth
	m.mu.Unlock()

	go m.reportToBackend(*stats)

}

func (m *NetworkMonitor) reportToBackend(stats MonitorStats) {
	stats.Timestamp = time.Now()

	payload, err := json.Marshal(stats)
	if err != nil {
		slog.Error("monitor: failed to marshal stats", "err", err)
		return
	}

	url := fmt.Sprintf("%s/api/v1/device/agent/%s/network_metrics", m.Config.BackendURL, m.Config.DeviceID)

	req, err := http.NewRequest(http.MethodPost, url, bytes.NewBuffer(payload))
	if err != nil {
		slog.Error("monitor: failed to build request", "url", url, "err", err)
		return
	}

	req.Header.Set("Content-Type", "application/json")
	// req.Header.Set("Authorization", "Bearer "+m.Config.AuthToken) //! the auth token is for the frp tunnel, not the API auth middleware
	//! maybe auth the users into the device to have access to the token

	resp, err := m.client.Do(req)
	if err != nil {
		slog.Error("monitor: report upload failed", "err", err)
		return
	}
	defer resp.Body.Close()
	// Drain body so the connection is returned to the pool immediately.
	// Without this, the transport holds the connection open until GC.
	io.Copy(io.Discard, resp.Body)

	if resp.StatusCode >= 400 {
		slog.Warn("monitor: backend rejected report", "status", resp.StatusCode)
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
		Timeout: 50 * time.Second,
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
