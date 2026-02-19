package main

import (
	"context"
	"flag"
	"log"
	"net/http"
	"os/signal"
	"syscall"

	"github.com/strct-org/strct-agent/internal/agent"
	"github.com/strct-org/strct-agent/internal/api"
	"github.com/strct-org/strct-agent/internal/config"
	adblocker "github.com/strct-org/strct-agent/internal/features/ad_blocker"
	"github.com/strct-org/strct-agent/internal/features/cloud"
	monitor "github.com/strct-org/strct-agent/internal/features/network_monitor"
	"github.com/strct-org/strct-agent/internal/features/router"
	"github.com/strct-org/strct-agent/internal/features/vpn"
	"github.com/strct-org/strct-agent/internal/tunnel"
	"github.com/strct-org/strct-agent/internal/wifi"
)

func main() {
	devMode := flag.Bool("dev", false, "Run in development mode (mock hardware)")
	flag.Parse()

	cfg := config.Load(*devMode)

	// --- Construct features ---
	cloudSvc, err := buildCloud(cfg)
	if err != nil {
		log.Fatalf("[MAIN] Cloud init failed: %v", err)
	}

	monitorSvc  := monitor.NewFromConfig(cfg)
	adblockSvc  := adblocker.NewDefault()
	routerSvc   := router.NewFromConfig(cfg)
	vpnSvc      := vpn.NewFromConfig(cfg)
	tunnelSvc   := tunnel.New(cfg)

	// --- Build API server: each feature registers its own routes ---
	apiSvc := buildAPI(cfg, cloudSvc, monitorSvc, adblockSvc, routerSvc, vpnSvc)

	// --- Wifi provider ---
	wifiProvider := wifi.New(cfg.IsArm64())

	// --- Wire agent (no feature knowledge inside agent.New) ---
	a, err := agent.New(cfg, wifiProvider, []agent.Service{
		cloudSvc,
		monitorSvc,
		adblockSvc,
		routerSvc,
		vpnSvc,
		tunnelSvc,
		apiSvc,
		&agent.ProfilerService{Port: cfg.PprofPort},
	})
	if err != nil {
		log.Fatalf("[MAIN] Agent init failed: %v", err)
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	a.Start(ctx)
	log.Println("Shutdown complete.")
}

// buildCloud initialises the storage layer and returns the cloud service.
func buildCloud(cfg *config.Config) (*cloud.Cloud, error) {
	c := cloud.New(cfg.DataDir, 8080, cfg.IsDev)
	if err := c.InitFileSystem(); err != nil {
		return nil, err
	}
	return c, nil
}

// buildAPI assembles the mux — each feature owns its route registration.
func buildAPI(
	cfg *config.Config,
	c *cloud.Cloud,
	m *monitor.NetworkMonitor,
	ab *adblocker.AdBlocker,
	rc *router.RouterController,
	v *vpn.VPN,
) *api.Server {
	mux := http.NewServeMux()

	mux.HandleFunc("/api/health", agent.HealthHandler)
	c.RegisterRoutes(mux)
	m.RegisterRoutes(mux)
	ab.RegisterRoutes(mux)
	rc.RegisterRoutes(mux)
	v.RegisterRoutes(mux)

	return api.New(api.Config{
		Port:    c.Port,
		DataDir: c.DataDir,
		IsDev:   cfg.IsDev,
	}, mux)
}