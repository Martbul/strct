// ? lifecycle orchestration only
package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"sync"
	"time"

	_ "net/http/pprof"

	"github.com/strct-org/strct-agent/internal/config"
	"github.com/strct-org/strct-agent/internal/errs"
	"github.com/strct-org/strct-agent/internal/platform/wifi"
	"github.com/strct-org/strct-agent/internal/setup"
)

const (
	opNew               errs.Op = "agent.New"
	opConnectivity      errs.Op = "agent.ensureConnectivity"
	opHotspot           errs.Op = "agent.runSetupWizard"
	networkWaitTimeout          = 30 * time.Second
	networkWaitInterval         = 500 * time.Millisecond
)

type Service interface {
	Start(ctx context.Context) error
}

type Agent struct {
	cfg      *config.Config
	wifi     wifi.Provider
	services []Service
}

func New(cfg *config.Config, w wifi.Provider, services []Service) (*Agent, error) {
	a := &Agent{cfg: cfg, wifi: w, services: services}
	if err := a.ensureConnectivity(); err != nil {
		return nil, errs.E(opNew, err)
	}
	return a, nil
}

func (a *Agent) Start(ctx context.Context) {
	slog.Info("agent: starting services", "count", len(a.services))

	var wg sync.WaitGroup
	for _, svc := range a.services {
		svc := svc
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := svc.Start(ctx); err != nil {
				slog.Error("agent: service failed to start", "err", err)
			}
		}()
	}

	<-ctx.Done()
	slog.Info("agent: shutdown signal received, waiting for services")
	wg.Wait()
}

func (a *Agent) ensureConnectivity() error {
	if wifi.HasInternet() {
		slog.Info("agent: internet detected, skipping setup wizard")
		return nil
	}
	slog.Info("agent: no internet detected, starting setup wizard")
	a.runSetupWizard()
	// Poll for internet instead of sleeping a fixed duration.
	// The interface needs time to get a DHCP lease after nmcli connects.
	if err := waitForInternet(networkWaitTimeout, networkWaitInterval); err != nil {
		return errs.E(opConnectivity, errs.KindNetwork,
			"no internet after setup wizard — check WiFi credentials")
	}

	slog.Info("agent: internet confirmed after setup wizard")
	return nil
}
func waitForInternet(timeout, interval time.Duration) error {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if wifi.HasInternet() {
			return nil
		}
		slog.Debug("agent: waiting for internet",
			"remaining", time.Until(deadline).Round(time.Second),
		)
		time.Sleep(interval)
	}
	return fmt.Errorf("timed out after %s", timeout)
}

func (a *Agent) runSetupWizard() {
	if err := a.wifi.StartHotspot(); err != nil {
		slog.Error("agent: hotspot start failed",
			"err", errs.E(opHotspot, errs.KindSystem, err),
		)
	}

	done := make(chan bool, 1)

	portalCtx, cancelPortal := context.WithCancel(context.Background())
	defer cancelPortal()

	go setup.StartCaptivePortal(portalCtx, a.wifi, done, a.cfg.IsDev)
	slog.Info("agent: captive portal running, waiting for WiFi credentials")
	<-done // blocks until user connects

	// Tell the portal to shut down. Its deferred iptables cleanup runs as
	// ListenAndServe exits, before StopHotspot below.
	cancelPortal()

	if err := a.wifi.StopHotspot(); err != nil {
		slog.Warn("agent: error stopping hotspot", "err", err)
	}

	slog.Info("agent: setup wizard complete, waiting for network to come up")
}

type ProfilerService struct {
	Port int
}

func (p *ProfilerService) Start(ctx context.Context) error {
	addr := fmt.Sprintf("127.0.0.1:%d", p.Port) 
	srv := &http.Server{Addr: addr}
	slog.Info("agent: pprof listening (SSH tunnel required)", "addr", addr)
	go func() {
		<-ctx.Done()
		srv.Close()
	}()
	if err := srv.ListenAndServe(); err != http.ErrServerClosed {
		return err
	}
	return nil
}

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
