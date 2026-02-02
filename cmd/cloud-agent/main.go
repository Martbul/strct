package main

import (
	"flag"
	"log"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/joho/godotenv"
	"github.com/strct-org/strct-agent/internal/disk"
	"github.com/strct-org/strct-agent/internal/fileserver"
	"github.com/strct-org/strct-agent/internal/ota"
	"github.com/strct-org/strct-agent/internal/setup"
	"github.com/strct-org/strct-agent/internal/tunnel"
	"github.com/strct-org/strct-agent/internal/wifi"
)

type Config struct {
	VPSIP     string
	VPSPort   int
	AuthToken string
	DeviceID  string
	Domain    string
}

func main() {
	devMode := flag.Bool("dev", false, "Run in development mode (Mock hardware)")
	flag.Parse()

	log.Println("--- Strct Agent Starting ---")

	if err := godotenv.Load(); err != nil {
		log.Println("[CONFIG] No .env file found, relying on system env vars")
	}

	cfg := loadConfig(*devMode)
	log.Printf("[INIT] Device ID: %s", cfg.DeviceID)
	log.Printf("[INIT] Target VPS: %s:%d", cfg.VPSIP, cfg.VPSPort)
	log.Printf("[INIT] Domain: %s", cfg.Domain)

	var wifiManager wifi.Provider

	if runtime.GOOS == "linux" && runtime.GOARCH == "arm64" && !*devMode {
		log.Println("[INIT] Detected Orange Pi. Using REAL Wi-Fi.")
		wifiManager = &wifi.RealWiFi{Interface: "wlan0"}
	} else {
		log.Println("[INIT] Detected VM. Using MOCK Wi-Fi.")
		wifiManager = &wifi.MockWiFi{}
	}

	if !hasInternet() {
		log.Println("[INIT] No Internet detected")

		macSuffix, fullMac := getMacDetails()

		log.Printf("[SETUP] Device Hardware MAC: %s", fullMac)

		ssid := "Strct-Setup-" + macSuffix
		password := "strct" + macSuffix

		log.Printf("[SETUP] Creating Hotspot. SSID: %s | Pass: %s", ssid, password)

		err := wifiManager.StartHotspot(ssid, password)
		if err != nil {
			log.Printf("[SETUP] Failed to create hotspot: %v", err)
		}

		done := make(chan bool)
		go setup.StartCaptivePortal(wifiManager, done, *devMode)

		log.Println("[SETUP] Web Server running. Waiting for user credentials...")
		<-done // BLOCK HERE until user connects

		log.Println("[SETUP] Credentials received. Stopping Hotspot and connecting...")
		wifiManager.StopHotspot()

		// switch modes
		time.Sleep(2 * time.Second)
	} else {
		log.Println("[INIT] Internet detected. Skipping setup.")
	}

	otaConfig := ota.Config{
		CurrentVersion: "1.0.9",
		StorageURL:     "https://portal.strct.org/updates",
	}

	ota.StartUpdater(otaConfig)

	diskMgr := disk.New(*devMode)

	status, err := diskMgr.GetStatus()
	if err != nil {
		log.Printf("[DISK] Error: %v", err)
	} else {
		log.Printf("[DISK] Status: %s", status)
	}

	  dataDir := "./data"
    if !*devMode && runtime.GOARCH == "arm64" {
        dataDir = "/mnt/data"
    }

    if *devMode {
        os.MkdirAll(dataDir, 0755)
    }

	if err := diskMgr.EnsureMounted(dataDir); err != nil {
		log.Printf("[DISK] CRITICAL: Failed to mount disk: %v", err)
	}

	log.Printf("[SYSTEM] Starting Native File Server (Data: %s)...", dataDir)
	
	go fileserver.Start(dataDir, 8080, *devMode)

	tunnelConfig := tunnel.TunnelConfig{
		ServerIP:   cfg.VPSIP,
		ServerPort: cfg.VPSPort,
		Token:      cfg.AuthToken,
		DeviceID:   cfg.DeviceID,
		LocalPort:  8080,
		BaseDomain: cfg.Domain,
	}

	go func() {
		for {
			log.Println("[TUNNEL] Connecting to Hub...")
			err := tunnel.StartTunnel(tunnelConfig)
			if err != nil {
				log.Printf("[TUNNEL] Connection lost or failed: %v", err)
				log.Println("[TUNNEL] Retrying in 10 seconds...")
			}
			time.Sleep(10 * time.Second)
		}
	}()

	log.Println("[SYSTEM] Agent is running. Press Ctrl+C to stop.")

	// Blocks forever, preventing the program from exiting
	select {}
}

func loadConfig(devMode bool) Config {
	port, _ := strconv.Atoi(getEnv("VPS_PORT", "7000"))

	return Config{
		VPSIP:     getEnv("VPS_IP", "127.0.0.1"),
		VPSPort:   port,
		AuthToken: getEnv("AUTH_TOKEN", "default-secret"),
		Domain:    getEnv("DOMAIN", "localhost"),
		DeviceID:  getOrGenerateDeviceID(devMode), // Pass it down
	}
}


func getEnv(key, fallback string) string {
	if value, exists := os.LookupEnv(key); exists {
		return value
	}
	return fallback
}


// CHANGED: Logic to handle Dev vs Prod paths
func getOrGenerateDeviceID(devMode bool) string {
	var filePath string

	if devMode {
		// In Dev mode, save to the current working directory
		// This ensures permissions issues don't block saving
		filePath = "device-id.lock"
	} else {
		// In Production, use the system path
		filePath = "/etc/strct/device-id.lock"
	}

	// 1. Try to read existing ID
	content, err := os.ReadFile(filePath)
	if err == nil {
		// ID found! Return it.
		return strings.TrimSpace(string(content))
	}

	// 2. Generate New ID
	newID := "device-" + uuid.New().String()
	log.Printf("[INIT] New Device ID generated: %s", newID)

	// 3. Ensure Directory exists (Critical for /etc/strct/)
	dir := filepath.Dir(filePath)
	if err := os.MkdirAll(dir, 0755); err != nil {
		log.Printf("[WARN] Could not create directory %s: %v", dir, err)
	}

	// 4. Save to disk
	err = os.WriteFile(filePath, []byte(newID), 0644)
	if err != nil {
		log.Printf("[WARN] Could not save device ID to disk at %s: %v", filePath, err)
	} else {
		log.Printf("[INIT] Device ID saved to %s", filePath)
	}

	return newID
}

func hasInternet() bool {
	client := http.Client{
		Timeout: 3 * time.Second,
	}
	_, err := client.Get("http://clients3.google.com/generate_204")
	return err == nil
}

func getMacDetails() (string, string) {
	// Try to get wlan0, fallback to first available
	ifas, err := net.Interfaces()
	if err != nil {
		return "XXXX", "00:00:00:00:00:00"
	}

	for _, ifa := range ifas {
		if ifa.Name == "wlan0" && len(ifa.HardwareAddr) > 0 {
			mac := ifa.HardwareAddr.String()
			// clean it up (remove colons)
			cleanMac := strings.ReplaceAll(mac, ":", "")
			cleanMac = strings.ToUpper(cleanMac)

			if len(cleanMac) >= 4 {
				return cleanMac[len(cleanMac)-4:], mac
			}
		}
	}
	return "XXXX", "00:00:00:00:00:00"
}
