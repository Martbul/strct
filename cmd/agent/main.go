package main

import (
	"context"
	"flag"
	"log"
	"log/slog"
	"net/http"
	"os/signal"
	"syscall"

	"github.com/strct-org/strct-agent/internal/agent"
	"github.com/strct-org/strct-agent/internal/api"
	"github.com/strct-org/strct-agent/internal/config"
	adblocker "github.com/strct-org/strct-agent/internal/features/adblocker"
	"github.com/strct-org/strct-agent/internal/features/cloud"
	monitor "github.com/strct-org/strct-agent/internal/features/monitor"
	"github.com/strct-org/strct-agent/internal/features/router"
	"github.com/strct-org/strct-agent/internal/features/vpn"
	"github.com/strct-org/strct-agent/internal/logger"
	"github.com/strct-org/strct-agent/internal/platform/tunnel"
	"github.com/strct-org/strct-agent/internal/platform/wifi"
)

// These are overridden at build time via:
//
//	go build -ldflags "-X main.DefaultDomain=strct.org -X main.DefaultVPSIP=157.90.167.157"
var (
	DefaultDomain = "localhost"
	DefaultVPSIP  = "127.0.0.1"
)

func main() {
	devMode := flag.Bool("dev", false, "Run in development mode (mock hardware)")
	flag.Parse()

	logger.Init(*devMode)

	cfg := config.Load(*devMode, DefaultDomain, DefaultVPSIP)
	slog.Info("agent: config loaded",
		"deviceID", cfg.DeviceID,
		"dev", cfg.IsDev,
		"dataDir", cfg.DataDir,
	)


	// --- Construct features ---
	cloudSvc, err := cloud.NewFromConfig(cfg)
	if err != nil {
		log.Fatalf("cloud init failed: %v", err)
	}

	monitorSvc := monitor.NewFromConfig(cfg)
	adblockSvc := adblocker.NewDefault()
	routerSvc := router.NewFromConfig(cfg)
	vpnSvc := vpn.NewFromConfig(cfg)
	tunnelSvc := tunnel.New(cfg)

	apiSvc := buildAPI(cfg, cloudSvc, monitorSvc, adblockSvc, routerSvc, vpnSvc)


a, err := agent.New(cfg, wifi.New(cfg.IsArm64()), []agent.Service{
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
		log.Fatalf("agent init failed: %v", err)
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	a.Start(ctx)
	slog.Info("agent: shutdown complete")
}


// buildAPI assembles the HTTP mux. Each feature registers its own routes.
func buildAPI(
	cfg *config.Config,
	c *cloud.Cloud,
	m *monitor.NetworkMonitor,
	ab *adblocker.AdBlocker,
	rc *router.RouterController,
	v *vpn.VPN,
) *api.Server {
	mux := http.NewServeMux()

	mux.HandleFunc("GET /api/health", agent.HealthHandler)
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