package main

import (
	"flag"
	"log"
	"net/http"
	"os"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/joho/godotenv"
	"github.com/strct-org/strct-agent/internal/disk"
	"github.com/strct-org/strct-agent/internal/docker"
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

	cfg := loadConfig()
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
		log.Println("[INIT] No Internet detected. Entering SETUP MODE.")

		err := wifiManager.StartHotspot("StructIO-Setup", "12345678")
		if err != nil {
			log.Printf("[SETUP] Failed to create hotspot: %v", err)
		}

		done := make(chan bool)
		go setup.StartCaptivePortal(wifiManager, done)

		log.Println("[SETUP] Web Server running. Waiting for user credentials...")
		<-done // BLOCK HERE until user connects

		log.Println("[SETUP] Credentials received. Stopping Hotspot and connecting...")
		wifiManager.StopHotspot()
		
		// Give the wifi chip a moment to switch modes
		time.Sleep(5 * time.Second)
	} else {
		log.Println("[INIT] Internet detected. Skipping setup.")
	}

	otaConfig := ota.Config{
		CurrentVersion: "1.0.0",
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
	if runtime.GOARCH == "arm64" {
		dataDir = "/mnt/data"
	}

	if err := diskMgr.EnsureMounted(dataDir); err != nil {
		log.Printf("[DISK] CRITICAL: Failed to mount disk: %v", err)
	}

	log.Printf("[DOCKER] Ensuring FileBrowser is running (Data: %s)...", dataDir)
	err = docker.EnsureFileBrowser(dataDir)
	if err != nil {
		log.Printf("[DOCKER] Critical Error starting container: %v", err)
	}

	tunnelConfig := tunnel.TunnelConfig{
		ServerIP:   cfg.VPSIP,
		ServerPort: cfg.VPSPort,
		Token:      cfg.AuthToken,
		DeviceID:   cfg.DeviceID,
		LocalPort:  80,
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

func loadConfig() Config {
	port, _ := strconv.Atoi(getEnv("VPS_PORT", "7000"))

	return Config{
		VPSIP:     getEnv("VPS_IP", "127.0.0.1"),
		VPSPort:   port,
		AuthToken: getEnv("AUTH_TOKEN", "default-secret"),
		Domain:    getEnv("DOMAIN", "localhost"),
		DeviceID:  getOrGenerateDeviceID(),
	}
}

func getEnv(key, fallback string) string {
	if value, exists := os.LookupEnv(key); exists {
		return value
	}
	return fallback
}

func getOrGenerateDeviceID() string {
	fileName := "device-id.lock"

	content, err := os.ReadFile(fileName)
	if err == nil {
		return strings.TrimSpace(string(content))
	}

	newID := "device-" + uuid.New().String()

	err = os.WriteFile(fileName, []byte(newID), 0644)
	if err != nil {
		log.Printf("[WARN] Could not save device ID to disk: %v", err)
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