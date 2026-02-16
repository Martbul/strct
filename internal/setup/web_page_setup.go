package setup

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os/exec"
	"time"

	"github.com/strct-org/strct-agent/internal/platform/wifi"
	"github.com/strct-org/strct-agent/internal/templates"
)

type Credentials struct {
	SSID     string `json:"ssid"`
	Password string `json:"password"`
}

func StartCaptivePortal(wifiMgr wifi.Provider, done chan<- bool, devMode bool) {
	mux := http.NewServeMux()

	mux.HandleFunc("/scan", func(w http.ResponseWriter, r *http.Request) {
		networks, err := wifiMgr.Scan()
		if err != nil {
			http.Error(w, err.Error(), 500)
			return
		}
		json.NewEncoder(w).Encode(networks)
	})

	mux.HandleFunc("/connect", func(w http.ResponseWriter, r *http.Request) {
		var creds Credentials
		if err := json.NewDecoder(r.Body).Decode(&creds); err != nil {
			http.Error(w, "Invalid JSON", 400)
			return
		}

		log.Printf("[SETUP] Received credentials for %s", creds.SSID)

		// 1. Respond to the user IMMEDIATELY.
		// If we wait for the connection logic, the hotspot will die
		// and the user will get a "Network Error" instead of success.
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("Credentials received. Device is rebooting into client mode..."))

		go func() {
			time.Sleep(2 * time.Second)

			log.Println("[SETUP] Applying Wi-Fi settings...")

			err := wifiMgr.Connect(creds.SSID, creds.Password)

			if err != nil {
				log.Printf("[SETUP] CRITICAL ERROR: Failed to connect: %v", err)
				//! In a real production app, you might want to restart the Hotspot here
				return
			}

			log.Println("[SETUP] Connected successfully! Setup complete.")
			done <- true
		}()
	})

	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-cache, no-store, must-revalidate")
		w.Header().Set("Pragma", "no-cache")
		w.Header().Set("Expires", "0")
		w.Header().Set("Content-Type", "text/html")
		fmt.Fprint(w, templates.HtmlPage)
	})

	port := ":80"
	if devMode {
		port = ":8082"
	}

	log.Printf("[SETUP] Web Server listening on %s", port)

	//! after user connects the device to the wifi remover the dns server
	if !devMode {
		dnsServer := StartDNSServer("10.42.0.1", ":5353")
		defer dnsServer.Shutdown()

		iface := "wlan0"

		log.Printf("[SETUP] Adding iptables rule for %s: 53 -> 5353", iface)
		exec.Command("iptables", "-t", "nat", "-A", "PREROUTING", "-i", iface, "-p", "udp", "--dport", "53", "-j", "REDIRECT", "--to-port", "5353").Run()

		defer func() {
			log.Println("[SETUP] Cleaning up iptables rules...")
			exec.Command("iptables", "-t", "nat", "-D", "PREROUTING", "-i", iface, "-p", "udp", "--dport", "53", "-j", "REDIRECT", "--to-port", "5353").Run()
		}()
	}

	if err := http.ListenAndServe(port, mux); err != nil {
		log.Printf("[SETUP] HTTP Server Error: %v", err)
	}
}
