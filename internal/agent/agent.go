package agent

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"sync"
	"time"

	_ "net/http/pprof"

	"github.com/strct-org/strct-agent/internal/api"
	"github.com/strct-org/strct-agent/internal/config"
	"github.com/strct-org/strct-agent/internal/errs"
	adblocker "github.com/strct-org/strct-agent/internal/features/ad_blocker"
	"github.com/strct-org/strct-agent/internal/features/cloud"
	monitor "github.com/strct-org/strct-agent/internal/features/network_monitor"
	"github.com/strct-org/strct-agent/internal/features/router"
	"github.com/strct-org/strct-agent/internal/features/vpn"
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
	Wifi    wifi.Provider
	Runners []Runner
	Config  *config.Config
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

type ProfilerService struct {
	Port int
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
	adBlocker := a.setupAdBlocker()
	routerController := a.setupRouterController()
	vpn := a.setupRouterVPN()

	apiSvc := a.assembleAPIServer(cloud, monitor, adBlocker, routerController, vpn)
	tunnelSvc := tunnel.New(a.Config)
	profilerSvc := &ProfilerService{
		Port: a.Config.PprofPort,
	}

	a.Runners = []Runner{
		monitor,
		tunnelSvc,
		apiSvc,
		profilerSvc,
		adBlocker,
		routerController,
		vpn,
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

func (a *Agent) setupAdBlocker() *adblocker.AdBlocker {
	return adblocker.New(adblocker.AdBlockConfig{})
}

func (a *Agent) setupRouterController() *router.RouterController {
	backend := a.Config.BackendURL //! setup the Backend URL in env
	if backend == "" {
		backend = "https://dev.api.strct.org" //! using curently only dev mode
	}
	return router.New(router.Config{
		DeviceID:   a.Config.DeviceID,
		BackendURL: backend,
	})
}

func (a *Agent) setupRouterVPN() *vpn.VPN {
	return vpn.New(vpn.Config{DeviceID: a.Config.DeviceID})
}

func (a *Agent) assembleAPIServer(cloud *cloud.Cloud, monitor *monitor.NetworkMonitor, adBlocker *adblocker.AdBlocker, router *router.RouterController, vpn *vpn.VPN) *APIService {
	routes := cloud.GetRoutes()

	routes["/api/health"] = handleHealth
	routes["/api/network/stats"] = monitor.HandleStats
	routes["/api/network/speedtest"] = monitor.HandleSpeedtest

	routes["/api/adblock/stats"] = adBlocker.HandleStats
	routes["/api/adblock/toggle"] = adBlocker.HandleToggle

	routes["/api/router/devices"] = router.HandleGetDevices
	routes["/api/router/block"] = router.HandleBlockDevice

	routes["/api/router/config"] = func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost {
			router.HandleSetConfig(w, r)
		} else {
			router.HandleGetConfig(w, r)
		}
	}

	routes["/api/vpn/status"] = vpn.HandleGetStatus
	routes["/api/vpn/setup"] = vpn.HandleSetup
	routes["/api/vpn/toggle"] = vpn.HandleToggleExitNode

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

func (p *ProfilerService) Start() error {
	addr := fmt.Sprintf("0.0.0.0:%d", p.Port)
	log.Printf("[PPROF] Profiling server started on http://%s/debug/pprof", addr)

	return http.ListenAndServe(addr, nil)
}

func handleHealth(w http.ResponseWriter, r *http.Request) {
	type HealthResponse struct {
		Status    string `json:"status"`
		Internet  bool   `json:"internet_access"`
		Timestamp string `json:"timestamp"`
	}

	response := HealthResponse{
		Status:    "ok",
		Internet:  wifi.HasInternet(),
		Timestamp: time.Now().UTC().Format(time.RFC3339),
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)

	if err := json.NewEncoder(w).Encode(response); err != nil {
		log.Printf("[API] Failed to write health response: %v", err)
	}
}
