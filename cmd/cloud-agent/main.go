package main

import (
	"flag"
	"log"
	"os"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/joho/godotenv"
	"github.com/strct-org/strct-agent/internal/disk"
	"github.com/strct-org/strct-agent/internal/docker"
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

	nets, err := wifiManager.Scan()
	if err != nil {
		log.Printf("[WIFI] Scan error: %v", err)
	} else {
		log.Printf("[WIFI] Scan found %d networks", len(nets))
	}

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
	// On Linux Arm64 (Production), maybe store in /etc/strct/device-id
	// For now, we store it in the local running folder.
	fileName := "device-id.lock"
	
	// 3. Try to read existing file
	content, err := os.ReadFile(fileName)
	if err == nil {
		return strings.TrimSpace(string(content))
	}

	// 4. Generate NEW ID if file doesn't exist
	newID := "device-" + uuid.New().String()
	
	// 5. Save to disk so it persists after reboot
	err = os.WriteFile(fileName, []byte(newID), 0644)
	if err != nil {
		log.Printf("[WARN] Could not save device ID to disk: %v", err)
	}

	return newID
}
