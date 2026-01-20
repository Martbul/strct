package main

import (
	"flag"
	"log"
	"runtime"
	"struct33_cloud/internal/disk"
	"struct33_cloud/internal/docker"
	"struct33_cloud/internal/tunnel"
	"struct33_cloud/internal/wifi"
	"time"
)

//! Put this in env
const (
	VPS_IP     = "157.90.167.157"
	VPS_PORT   = 7000
	AUTH_TOKEN = "Struct33_Secret_Key_99"
	DEVICE_ID  = "device-001"             // In prod use a file/UUID
	DOMAIN     = "strct.org"
)

func main() {
	devMode := flag.Bool("dev", false, "Run in development mode (Mock hardware)")
	flag.Parse()

	log.Println("--- Strct Agent Starting ---")

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
		ServerIP:   VPS_IP,
		ServerPort: VPS_PORT,
		Token:      AUTH_TOKEN,
		DeviceID:   DEVICE_ID,
		LocalPort:  80,     
		BaseDomain: DOMAIN, 
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
