package agent

import (
	"fmt"
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
	Config  *config.Config
	Wifi    wifi.Provider
	Runners []Runner
}
// HTTPFeature represents a high-level feature that provides API routes
type HTTPFeature interface {
	GetRoutes() map[string]http.HandlerFunc
}

// Runner represents anything that needs to run in the background (Tunnel, Monitor loop, Server)
type Runner interface {
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
	// 1. Connectivity Check (Platform Layer)
	if err := a.ensureConnectivity(); err != nil {
		return err
	}

	// 2. Initialize Features (Domain Layer)
	// These are the "Products" your device offers.
	cloudFeat, err := a.setupCloud()
	if err != nil {
		return fmt.Errorf("cloud feature init: %w", err)
	}
	monitorFeat := a.setupMonitor()

	// 3. Initialize Network Infrastructure (Transport Layer)
	// These are the mechanisms to access the features.
	apiSvc := a.assembleAPIServer(cloudFeat, monitorFeat)
	tunnelSvc := tunnel.New(a.Config)
	dnsSvc := dns.NewAdBlocker(":63")

	// 4. Register everything that needs to run
	// The Agent doesn't care if it's a feature or a network tool, 
	// it just needs to know what to Start().
	a.Runners = []Runner{
		monitorFeat, // Monitor has a background loop
		tunnelSvc,   // Tunnel holds the connection
		dnsSvc,      // DNS listens on UDP
		apiSvc,      // API listens on HTTP
	}

	return nil
}

func (a *Agent) setupCloud() (*cloud.Cloud, error) {
	c := cloud.New(a.Config.DataDir, 8080, a.Config.IsDev)
	if err := c.InitFileSystem(); err != nil {
		return nil, err
	}
	return c, nil
}


// setupMonitor initializes the Network Monitor Feature logic
func (a *Agent) setupMonitor() *monitor.NetworkMonitor {
	// Logic to determine backend URL keeps main code clean
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


// assembleAPIServer acts as the "Switchboard", plugging features into the HTTP server
func (a *Agent) assembleAPIServer(cloud *cloud.Cloud , monitorFeat *monitor.NetworkMonitor) *APIService {
	// 1. Collect Cloud Routes
	routes := cloud.GetRoutes() 

	// 2. Collect Monitor Routes (Manual mapping if the package doesn't support GetRoutes yet)
	// ideally, you add GetRoutes() to the monitor package too, 
	// but mapping here is fine for "glue" code.
	routes["/api/network/now"] = monitorFeat.HandleStats
	routes["/api/network/speedtest"] = monitorFeat.HandleSpeedtest

	// 3. Create the Server
	// Note: We use cloudFeat config for the server, but maybe the Server should have its own config?
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
	// Blocking call to wizard
	a.runSetupWizard()
	
	// Double check after wizard
	if !wifi.HasInternet() {
		return fmt.Errorf("still no internet after setup wizard")
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

// func (a *Agent) Initialize() {
// 	if !wifi.HasInternet() {
// 		log.Println("[INIT] No Internet detected. Starting Setup Wizard...")
// 		a.runSetupWizard()
// 	} else {
// 		log.Println("[INIT] Internet detected. Skipping setup.")
// 	}

	// cloudFeature := cloud.New(a.Config.DataDir, 8080, a.Config.IsDev)
	// if err := cloudFeature.InitFileSystem(); err != nil {
	// 	log.Fatalf("[CRITICAL] Failed to initialize cloud fs: %v", err)
	// }

// 	monitorCfg := monitor.Config{
// 		DeviceID:   a.Config.DeviceID,
// 		BackendURL: "https://dev.api.strct.org", // !load from a.Config.BackendURL
// 		AuthToken:  a.Config.AuthToken,
// 	}
// 	monitorFeature := monitor.New(monitorCfg)
// 	monitorFeature.Start() 

// 	routes := make(map[string]http.HandlerFunc)

// 	for path, handler := range cloudFeature.GetRoutes() {
// 		routes[path] = handler
// 	}

// 	routes["/api/network/now"] = monitorFeature.HandleStats
// 	routes["/api/network/speedtest"] = monitorFeature.HandleSpeedtest

// 	apiSvc := &APIService{
// 		Config: api.Config{
// 			Port:    cloudFeature.Port,
// 			DataDir: cloudFeature.DataDir,
// 			IsDev:   cloudFeature.IsDev,
// 		},
// 		Routes: routes,
// 	}

// 	a.Services = []Service{
// 		tunnel.New(a.Config),    // Frp Tunnel
// 		dns.NewAdBlocker(":63"), // AdGuard Home / DNS
// 		apiSvc,                  // Unified HTTP Server (Cloud + Monitor)
// 	}
// }

// func (a *Agent) Start() {
// 	var wg sync.WaitGroup

// 	log.Println("--- Strct Agent Starting Services ---")

// 	for _, svc := range a.Services {
// 		wg.Add(1)
// 		go func(s Service) {
// 			defer wg.Done()
// 			if err := s.Start(); err != nil {
// 				log.Printf("[CRITICAL] Service crashed: %v", err)
// 			}
// 		}(svc)
// 	}

// 	wg.Wait()
// }



func (a *Agent) runSetupWizard() {
	err := a.Wifi.StartHotspot()
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
