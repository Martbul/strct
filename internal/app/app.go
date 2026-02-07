package app

import (
	"log"
	"net/http"
	"sync"
	"time"

	"github.com/strct-org/strct-agent/internal/api"
	"github.com/strct-org/strct-agent/internal/config"
	"github.com/strct-org/strct-agent/internal/features/cloud"
	monitor "github.com/strct-org/strct-agent/internal/features/network_monitor"
	"github.com/strct-org/strct-agent/internal/network/dns"
	"github.com/strct-org/strct-agent/internal/network/tunnel"
	"github.com/strct-org/strct-agent/internal/platform/wifi"
	"github.com/strct-org/strct-agent/internal/setup"
)

type Agent struct {
	Config   *config.Config
	Wifi     wifi.Provider
	Services []Service
}

// Service represents a long-running blocking process (Tunnel, DNS, WebServer)
type Service interface {
	Start() error
}

// APIService is a wrapper to make the generic api package fit the Service interface
type APIService struct {
	Config api.Config
	Routes map[string]http.HandlerFunc
}

func (s *APIService) Start() error {
	return api.Start(s.Config, s.Routes)
}

// constructor
func New(cfg *config.Config) *Agent {
	wifiMgr := loadWifiManager(cfg)
	return &Agent{
		Config: cfg,
		Wifi:   wifiMgr,
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

func (a *Agent) Bootstrap() {
	if !a.hasInternet() {
		log.Println("[INIT] No Internet detected. Starting Setup Wizard...")
		a.runSetupWizard()
	} else {
		log.Println("[INIT] Internet detected. Skipping setup.")
	}

	// 2. Initialize Features (Non-blocking setup)
	cloudFeature := cloud.New(a.Config.DataDir, 8080, a.Config.IsDev)
	if err := cloudFeature.InitFileSystem(); err != nil {
		log.Fatalf("[CRITICAL] Failed to initialize cloud fs: %v", err)
	}

	monitorCfg := monitor.Config{
		DeviceID:   a.Config.DeviceID,
		BackendURL: "https://api.strct.org", // !load from a.Config.BackendURL
		AuthToken:  a.Config.AuthToken,
	}
	monitorFeature := monitor.New(monitorCfg)
	monitorFeature.Start() // Starts background tickers (non-blocking)

	// 3. Aggregate Routes
	// We combine routes from all features into one map for the single HTTP server
	routes := make(map[string]http.HandlerFunc)

	// Add Cloud Routes
	for path, handler := range cloudFeature.GetRoutes() {
		routes[path] = handler
	}

	// Add Monitor Routes
	routes["/api/network/now"] = monitorFeature.HandleStats

	// 4. Prepare the API Service Wrapper
	apiSvc := &APIService{
		Config: api.Config{
			Port:    cloudFeature.Port,
			DataDir: cloudFeature.DataDir,
			IsDev:   cloudFeature.IsDev,
		},
		Routes: routes,
	}

	a.Services = []Service{
		tunnel.New(a.Config),    // Frp Tunnel
		dns.NewAdBlocker(":53"), // AdGuard Home / DNS
		apiSvc,                  // Unified HTTP Server (Cloud + Monitor)
	}
}

func (a *Agent) Start() {
	var wg sync.WaitGroup

	log.Println("--- Strct Agent Starting Services ---")

	for _, svc := range a.Services {
		wg.Add(1)
		go func(s Service) {
			defer wg.Done()
			if err := s.Start(); err != nil {
				log.Printf("[CRITICAL] Service crashed: %v", err)
			}
		}(svc)
	}

	wg.Wait()
}

func (a *Agent) hasInternet() bool {
	client := http.Client{Timeout: 3 * time.Second}
	_, err := client.Get("http://clients3.google.com/generate_204")
	return err == nil
}

func (a *Agent) runSetupWizard() {
	macSuffix := "XXXX" //! In prod, get real MAC

	ssid := "Strct-Setup-" + macSuffix
	password := "strct" + macSuffix

	log.Printf("[SETUP] Creating Hotspot. SSID: %s", ssid)

	err := a.Wifi.StartHotspot(ssid, password)
	if err != nil {
		log.Printf("[SETUP] Failed to create hotspot: %v", err)
	}

	done := make(chan bool)

	go setup.StartCaptivePortal(a.Wifi, done, a.Config.IsDev)

	log.Println("[SETUP] Waiting for user credentials...")
	<-done

	a.Wifi.StopHotspot()
	time.Sleep(2 * time.Second)
}
