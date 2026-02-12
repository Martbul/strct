package agent

import (
	"log"
	"net/http"
	"sync"
	"time"

	"github.com/strct-org/strct-agent/internal/api"
	"github.com/strct-org/strct-agent/internal/config"
	"github.com/strct-org/strct-agent/internal/errs"
	"github.com/strct-org/strct-agent/internal/features/cloud"
	monitor "github.com/strct-org/strct-agent/internal/features/network_monitor"
	"github.com/strct-org/strct-agent/internal/network/dns"
	"github.com/strct-org/strct-agent/internal/network/tunnel"
	"github.com/strct-org/strct-agent/internal/platform/wifi"
	"github.com/strct-org/strct-agent/internal/setup"
)

const (
	OpAgentInit    errs.Op = "agent.Initialize"
	OpSetupCloud   errs.Op = "agent.setupCloud"
	OpCheckConn    errs.Op = "agent.ensureConnectivity"
	OpStartHotspot errs.Op = "agent.runSetupWizard"
)

type Agent struct {
	Wifi      wifi.Provider
	Runners   []Runner
	Config    *config.Config
}
type HTTPFeature interface {
	GetRoutes() map[string]http.HandlerFunc
}

type Runner interface {
	Start() error
}

type APIService struct {
	Config api.Config
	Routes map[string]http.HandlerFunc
}

func (s *APIService) Start() error {
	return api.Start(s.Config, s.Routes)
}

func New(cfg *config.Config) *Agent {
	return &Agent{
		Config: cfg,
		Wifi:   loadWifiManager(cfg),
	}
}

func loadWifiManager(cfg *config.Config) wifi.Provider {
	var wifiMgr wifi.Provider
	if cfg.IsArm64() {
		wifiMgr = &wifi.RealWiFi{Interface: "wlan0"}
	} else {
		wifiMgr = &wifi.MockWiFi{}
	}
	return wifiMgr
}

func (a *Agent) Initialize() error {
	if err := a.ensureConnectivity(); err != nil {
		return errs.E(OpAgentInit, err)
	}

	cloud, err := a.setupCloud()
	if err != nil {
		return errs.E(OpAgentInit, err)
	}
	monitor := a.setupMonitor()

	apiSvc := a.assembleAPIServer(cloud, monitor)
	tunnelSvc := tunnel.New(a.Config)
	dnsSvc := dns.NewAdBlocker(":63")

	a.Runners = []Runner{
		monitor,
		tunnelSvc,
		dnsSvc,
		apiSvc,
	}

	return nil
}

func (a *Agent) setupCloud() (*cloud.Cloud, error) {
	c := cloud.New(a.Config.DataDir, 8080, a.Config.IsDev)
	if err := c.InitFileSystem(); err != nil {
		return nil, errs.E(OpSetupCloud, errs.KindIO, err, "failed to initialize cloud storage")
	}
	return c, nil
}

func (a *Agent) setupMonitor() *monitor.NetworkMonitor {
	backend := a.Config.BackendURL //! setup the Backend URL in env
	if backend == "" {
		backend = "https://dev.api.strct.org" //! using curently only dev mode
	}

	return monitor.New(monitor.Config{
		DeviceID:   a.Config.DeviceID,
		BackendURL: backend,
		AuthToken:  a.Config.AuthToken,
	})
}

func (a *Agent) assembleAPIServer(cloud *cloud.Cloud, monitorFeat *monitor.NetworkMonitor) *APIService {
	routes := cloud.GetRoutes()

	routes["/api/network/stats"] = monitorFeat.HandleStats
	routes["/api/network/speedtest"] = monitorFeat.HandleSpeedtest
	routes["/api/health"] = monitorFeat.HandleHealth 

	return &APIService{
		Config: api.Config{
			Port:    cloud.Port,
			DataDir: cloud.DataDir,
			IsDev:   cloud.IsDev,
		},
		Routes: routes,
	}
}

func (a *Agent) ensureConnectivity() error {
	if wifi.HasInternet() {
		log.Println("[INIT] Internet detected. Skipping setup.")
		return nil
	}

	log.Println("[INIT] No Internet detected. Starting Setup Wizard...")
	a.runSetupWizard()

	if !wifi.HasInternet() {
		return errs.E(OpCheckConn, errs.KindNetwork, "still no internet after setup wizard")
	}
	return nil
}

func (a *Agent) Start() {
	var wg sync.WaitGroup
	log.Println("--- Strct Agent Starting ---")

	for _, runner := range a.Runners {
		wg.Add(1)
		go func(r Runner) {
			defer wg.Done()
			if err := r.Start(); err != nil {
				log.Printf("[CRITICAL] Component crashed: %v", err)
			}
		}(runner)
	}
	wg.Wait()
}

func (a *Agent) runSetupWizard() {
	err := a.Wifi.StartHotspot()
	if err != nil {
		log.Printf("[SETUP] Failed to create hotspot: %v", errs.E(OpStartHotspot, errs.KindSystem, err))
	}

	done := make(chan bool)

	go setup.StartCaptivePortal(a.Wifi, done, a.Config.IsDev)

	log.Println("[SETUP] Waiting for user credentials...")
	<-done

	a.Wifi.StopHotspot()
	time.Sleep(2 * time.Second)
}
