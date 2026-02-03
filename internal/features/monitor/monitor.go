package monitor

import (
	"log"
	"time"

	"github.com/go-ping/ping"
)

type NetworkMonitor struct {
	Interval time.Duration
	Target   string
}

// New creates a monitor that checks Google DNS (8.8.8.8)
func New(interval time.Duration) *NetworkMonitor {
	return &NetworkMonitor{
		Interval: interval,
		Target:   "8.8.8.8",
	}
}

// Start begins the monitoring loop (Satisfies the Service interface)
func (m *NetworkMonitor) Start() error {
	log.Printf("[MONITOR] Starting Network Health Monitor (Target: %s, Interval: %s)", m.Target, m.Interval)

	ticker := time.NewTicker(m.Interval)

	for range ticker.C {
		stats, err := m.pingTarget()
		if err != nil {
			log.Printf("[MONITOR] Ping Failed: %v", err)
			continue
		}

		// In the future, you will save 'stats' to a database here.
		// For now, we just log if latency is high.
		if stats.AvgRtt > 100*time.Millisecond {
			log.Printf("[MONITOR] High Latency detected: %v", stats.AvgRtt)
		} else if stats.PacketLoss > 0 {
			log.Printf("[MONITOR] Packet Loss detected: %.2f%%", stats.PacketLoss)
		}
	}
	return nil
}

func (m *NetworkMonitor) pingTarget() (*ping.Statistics, error) {
	pinger, err := ping.NewPinger(m.Target)
	if err != nil {
		return nil, err
	}

	// Windows/Linux specific settings
	pinger.SetPrivileged(true) // Needed for ICMP on Linux/Mac without sudo (sometimes)
	
	pinger.Count = 3
	pinger.Timeout = 2 * time.Second
	
	err = pinger.Run() // Blocks until finished
	if err != nil {
		return nil, err
	}

	return pinger.Statistics(), nil
}