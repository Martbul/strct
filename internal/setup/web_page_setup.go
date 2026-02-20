package setup

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os/exec"
	"time"

	"github.com/strct-org/strct-agent/internal/platform/wifi"
)

type Credentials struct {
	SSID     string `json:"ssid"`
	Password string `json:"password"`
}

func StartCaptivePortal(ctx context.Context, wifiMgr wifi.Provider, done chan<- bool, devMode bool) {
	port := ":80"
	if devMode {
		port = ":8082"
	}

	// connected is an internal signal: fires when the user's credentials
	// are accepted and WiFi.Connect succeeds. Separate from done because
	// we need to trigger server shutdown AND notify the caller.
	connected := make(chan struct{})

	mux := buildMux(ctx, wifiMgr, connected)

	srv := &http.Server{
		Addr:         port,
		Handler:      mux,
		ReadTimeout:  15 * time.Second,
		WriteTimeout: 15 * time.Second,
		IdleTimeout:  60 * time.Second,
	}

	// Shutdown watcher — exits when either the agent cancels or the user connects.
	go func() {
		select {
		case <-ctx.Done():
			slog.Info("setup: agent context cancelled, shutting down captive portal")
		case <-connected:
			slog.Info("setup: WiFi connected, shutting down captive portal")
			done <- true // notify the caller AFTER we know the server will stop
		}

		shutCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := srv.Shutdown(shutCtx); err != nil {
			slog.Error("setup: captive portal shutdown error", "err", err)
		}
	}()

	// DNS redirect and iptables — only on real hardware.
	// Set up before ListenAndServe, clean up after it returns.
	if !devMode {
		dnsServer := StartDNSServer("10.42.0.1", ":5353")
		defer dnsServer.Shutdown()

		const iface = "wlan0"
		const rule = "iptables -t nat -A PREROUTING -i " + iface + " -p udp --dport 53 -j REDIRECT --to-port 5353"
		slog.Info("setup: adding iptables DNS redirect", "iface", iface)
		exec.Command("iptables", "-t", "nat", "-A", "PREROUTING",
			"-i", iface, "-p", "udp", "--dport", "53",
			"-j", "REDIRECT", "--to-port", "5353").Run()

		defer func() {
			slog.Info("setup: removing iptables DNS redirect", "rule", rule)
			exec.Command("iptables", "-t", "nat", "-D", "PREROUTING",
				"-i", iface, "-p", "udp", "--dport", "53",
				"-j", "REDIRECT", "--to-port", "5353").Run()
		}()
	}

	slog.Info("setup: captive portal listening", "port", port)

	// ListenAndServe blocks here. When srv.Shutdown() is called above,
	// it returns http.ErrServerClosed — that is the normal exit path.
	if err := srv.ListenAndServe(); !errors.Is(err, http.ErrServerClosed) {
		slog.Error("setup: captive portal server error", "err", err)
	}

	// Deferred cleanup (dnsServer.Shutdown, iptables -D) runs here,
	// AFTER ListenAndServe returns, guaranteed in both shutdown paths.
}

// buildMux wires up the three routes. Extracted so Start is readable.
func buildMux(ctx context.Context, wifiMgr wifi.Provider, connected chan<- struct{}) *http.ServeMux {
	mux := http.NewServeMux()

	mux.HandleFunc("GET /scan", func(w http.ResponseWriter, r *http.Request) {
		networks, err := wifiMgr.Scan()
		if err != nil {
			slog.Error("setup: scan failed", "err", err)
			http.Error(w, "scan failed", http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(networks)
	})

	mux.HandleFunc("POST /connect", func(w http.ResponseWriter, r *http.Request) {
		var creds Credentials
		if err := json.NewDecoder(r.Body).Decode(&creds); err != nil {
			http.Error(w, "invalid JSON", http.StatusBadRequest)
			return
		}
		if creds.SSID == "" {
			http.Error(w, "ssid is required", http.StatusBadRequest)
			return
		}

		slog.Info("setup: credentials received", "ssid", creds.SSID)

		// Respond immediately — the hotspot will drop when we switch to
		// client mode, so the browser must get the response before we Connect.
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, "Credentials received. Connecting...")

		// Apply credentials in the background.
		// Use ctx so this is cancelled cleanly if the agent shuts down
		// in the ~2s window before we call Connect.
		go func() {
			// Brief delay so the HTTP response reaches the browser before
			// the network interface changes and the connection drops.
			select {
			case <-ctx.Done():
				slog.Warn("setup: context cancelled before connect attempt")
				return
			case <-time.After(2 * time.Second):
			}

			slog.Info("setup: connecting to WiFi", "ssid", creds.SSID)
			if err := wifiMgr.Connect(creds.SSID, creds.Password); err != nil {
				slog.Error("setup: WiFi connect failed", "ssid", creds.SSID, "err", err)
				// TODO: signal the frontend somehow (websocket / retry endpoint)
				// For now, the user will have to retry from the hotspot.
				return
			}

			slog.Info("setup: WiFi connected successfully", "ssid", creds.SSID)
			// Non-blocking send: if the shutdown watcher already fired
			// (e.g. ctx cancelled), we don't deadlock.
			select {
			case connected <- struct{}{}:
			default:
			}
		}()
	})

	mux.HandleFunc("GET /", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		w.Header().Set("Cache-Control", "no-cache, no-store, must-revalidate")
		fmt.Fprint(w, HtmlPage)
	})

	return mux
}

// func StartCaptivePortal(ctx context.Context, wifiMgr wifi.Provider, done chan<- bool, devMode bool) {
// 	mux := http.NewServeMux()

// 	mux.HandleFunc("/scan", func(w http.ResponseWriter, r *http.Request) {
// 		networks, err := wifiMgr.Scan()
// 		if err != nil {
// 			http.Error(w, err.Error(), 500)
// 			return
// 		}
// 		json.NewEncoder(w).Encode(networks)
// 	})

// 	mux.HandleFunc("/connect", func(w http.ResponseWriter, r *http.Request) {
// 		var creds Credentials
// 		if err := json.NewDecoder(r.Body).Decode(&creds); err != nil {
// 			http.Error(w, "Invalid JSON", 400)
// 			return
// 		}

// 		log.Printf("[SETUP] Received credentials for %s", creds.SSID)

// 		// respond IMMEDIATELY.
// 		w.WriteHeader(http.StatusOK)
// 		w.Write([]byte("Credentials received. Device is rebooting into client mode..."))

// 		go func() {
// 			select {
// 			case <-ctx.Done():
// 				log.Println("[SETUP] Context cancelled, stopping setup wizard")
// 				return
// 			default:
// 			}
// 			time.Sleep(2 * time.Second)

// 			log.Println("[SETUP] Applying Wi-Fi settings...")

// 			err := wifiMgr.Connect(creds.SSID, creds.Password)

// 			if err != nil {
// 				log.Printf("[SETUP] CRITICAL ERROR: Failed to connect: %v", err)
// 				//! In a real production app, you might want to restart the Hotspot here
// 				return
// 			}

// 			log.Println("[SETUP] Connected successfully! Setup complete.")
// 			done <- true
// 		}()
// 	})

// 	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
// 		w.Header().Set("Cache-Control", "no-cache, no-store, must-revalidate")
// 		w.Header().Set("Pragma", "no-cache")
// 		w.Header().Set("Expires", "0")
// 		w.Header().Set("Content-Type", "text/html")
// 		fmt.Fprint(w, HtmlPage)
// 	})

// 	port := ":80"
// 	if devMode {
// 		port = ":8082"
// 	}

// 	log.Printf("[SETUP] Web Server listening on %s", port)

// 	//! after user connects the device to the wifi remover the dns server
// 	if !devMode {
// 		dnsServer := StartDNSServer("10.42.0.1", ":5353")
// 		defer dnsServer.Shutdown()

// 		iface := "wlan0"

// 		log.Printf("[SETUP] Adding iptables rule for %s: 53 -> 5353", iface)
// 		exec.Command("iptables", "-t", "nat", "-A", "PREROUTING", "-i", iface, "-p", "udp", "--dport", "53", "-j", "REDIRECT", "--to-port", "5353").Run()

// 		defer func() {
// 			log.Println("[SETUP] Cleaning up iptables rules...")
// 			exec.Command("iptables", "-t", "nat", "-D", "PREROUTING", "-i", iface, "-p", "udp", "--dport", "53", "-j", "REDIRECT", "--to-port", "5353").Run()
// 		}()
// 	}

// 	if err := http.ListenAndServe(port, mux); err != nil {
// 		log.Printf("[SETUP] HTTP Server Error: %v", err)
// 	}
// }
