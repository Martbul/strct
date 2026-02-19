//? lifecycle orchestration only
package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"sync"
	"time"

	_ "net/http/pprof"

	"github.com/strct-org/strct-agent/internal/config"
	"github.com/strct-org/strct-agent/internal/errs"
	"github.com/strct-org/strct-agent/internal/setup"
	"github.com/strct-org/strct-agent/internal/platform/wifi"
)

const (
	opNew          errs.Op = "agent.New"
	opConnectivity errs.Op = "agent.ensureConnectivity"
	opHotspot      errs.Op = "agent.runSetupWizard"
)

// Service is anything the agent can lifecycle-manage.
type Service interface {
	Start(ctx context.Context) error
}

// Agent orchestrates service lifecycles.
// It does not construct services and does not know about HTTP routes.
type Agent struct {
	cfg      *config.Config
	wifi     wifi.Provider
	services []Service
}

// New checks connectivity, then returns a ready Agent.
// All services are injected by the caller (main).
func New(cfg *config.Config, w wifi.Provider, services []Service) (*Agent, error) {
	a := &Agent{cfg: cfg, wifi: w, services: services}
	if err := a.ensureConnectivity(); err != nil {
		return nil, errs.E(opNew, err)
	}
	return a, nil
}

// Start runs all services concurrently and blocks until ctx is cancelled
// or all services exit.
func (a *Agent) Start(ctx context.Context) {
	var wg sync.WaitGroup
	log.Println("--- Strct Agent Starting ---")
	for _, svc := range a.services {
		wg.Add(1)
		go func(s Service) {
			defer wg.Done()
			if err := s.Start(ctx); err != nil {
				log.Printf("[CRITICAL] Service crashed: %v", err)
			}
		}(svc)
	}
	wg.Wait()
	log.Println("--- Strct Agent Stopped ---")
}

func (a *Agent) ensureConnectivity() error {
	if wifi.HasInternet() {
		log.Println("[INIT] Internet detected")
		return nil
	}
	log.Println("[INIT] No internet — starting setup wizard")
	a.runSetupWizard()
	if !wifi.HasInternet() {
		return errs.E(opConnectivity, errs.KindNetwork, "no internet after setup wizard")
	}
	return nil
}

func (a *Agent) runSetupWizard() {
	if err := a.wifi.StartHotspot(); err != nil {
		log.Printf("[SETUP] Hotspot error: %v", errs.E(opHotspot, errs.KindSystem, err))
	}
	done := make(chan bool, 1)
	go setup.StartCaptivePortal(a.wifi, done, a.cfg.IsDev)
	log.Println("[SETUP] Waiting for WiFi credentials...")
	<-done
	a.wifi.StopHotspot()
	time.Sleep(2 * time.Second)
}

// ---------------------------------------------------------------------------
// Built-in services (small enough to live in this package)
// ---------------------------------------------------------------------------

// ProfilerService exposes pprof on a dedicated port.
type ProfilerService struct {
	Port int
}

func (p *ProfilerService) Start(ctx context.Context) error {
	srv := &http.Server{Addr: fmt.Sprintf("0.0.0.0:%d", p.Port)}
	log.Printf("[PPROF] Listening on http://0.0.0.0:%d/debug/pprof", p.Port)
	go func() {
		<-ctx.Done()
		srv.Close()
	}()
	if err := srv.ListenAndServe(); err != http.ErrServerClosed {
		return err
	}
	return nil
}

// HealthHandler reports agent-level status.
// Registered by main alongside feature routes.
func HealthHandler(w http.ResponseWriter, r *http.Request) {
	type response struct {
		Status    string `json:"status"`
		Internet  bool   `json:"internet_access"`
		Timestamp string `json:"timestamp"`
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(response{
		Status:    "ok",
		Internet:  wifi.HasInternet(),
		Timestamp: time.Now().UTC().Format(time.RFC3339),
	})
}