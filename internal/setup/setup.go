package setup

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"

	"github.com/strct-org/strct-agent/internal/platform/wifi"
)

type Credentials struct {
	SSID     string `json:"ssid"`
	Password string `json:"password"`
}
func StartCaptivePortal(wifiMgr wifi.Provider, done chan<- bool, devMode bool) {
	mux := http.NewServeMux()

	// 1. API Handlers
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
		
		err := wifiMgr.Connect(creds.SSID, creds.Password)
		if err != nil {
			http.Error(w, "Failed to connect: "+err.Error(), 500)
			return
		}
		w.Write([]byte("Connected! Rebooting..."))
		done <- true
	})

	// 2. The "Catch-All" Handler (The magic part)
	// This handles "/", "/generate_204", "/hotspot-detect.html", etc.
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		// If it's a specific Android check, sometimes returning 204 prevents the popup.
		// We WANT the popup, so we redirect or serve HTML.
		
		// If the user requests a specific domain (e.g. google.com), redirect them to our IP
		// Note: Replace 10.42.0.1 with your actual Hotspot Gateway IP if different
		// redirectIP := "http://10.42.0.1" 
		
		// However, the simplest way is to just serve the HTML for EVERYTHING.
		// The OS sees "I asked for Google, got this HTML page" -> Triggers Portal.
		
		w.Header().Set("Cache-Control", "no-cache, no-store, must-revalidate")
		w.Header().Set("Pragma", "no-cache")
		w.Header().Set("Expires", "0")
		w.Header().Set("Content-Type", "text/html")
		fmt.Fprint(w, htmlPage)
	})

	port := ":80"
	if devMode {
		port = ":8082"
	}

	log.Printf("[SETUP] Web Server listening on %s", port)
	
	// Start DNS Server (Only in Prod/Linux usually, but good to run if we can bind 53)
	// You need to know your Hotspot Gateway IP. 
	// NetworkManager hotspots usually use 10.42.0.1 by default.
	if !devMode {
		dnsServer := StartDNSServer("10.42.0.1") 
		defer dnsServer.Shutdown() 
	}

	if err := http.ListenAndServe(port, mux); err != nil {
		log.Printf("[SETUP] HTTP Server Error: %v", err)
	}
}

// Simple embedded HTML for the phone
const htmlPage = `
<!DOCTYPE html>
<html>
<body>
<h2>StructIO Setup</h2>
<button onclick="scan()">Scan Networks</button>
<ul id="list"></ul>
<div id="form" style="display:none">
    <input id="ssid" placeholder="SSID"><br>
    <input id="pass" placeholder="Password"><br>
    <button onclick="connect()">Connect</button>
</div>
<script>
async function scan() {
    let res = await fetch('/scan');
    let nets = await res.json();
    let list = document.getElementById('list');
    list.innerHTML = '';
    nets.forEach(n => {
        let li = document.createElement('li');
        li.innerText = n.SSID + ' (' + n.Signal + '%)';
        li.onclick = () => {
            document.getElementById('ssid').value = n.SSID;
            document.getElementById('form').style.display = 'block';
        };
        list.appendChild(li);
    });
}
async function connect() {
    let ssid = document.getElementById('ssid').value;
    let pass = document.getElementById('pass').value;
    await fetch('/connect', {
        method: 'POST',
        body: JSON.stringify({ssid, password: pass})
    });
    alert('Device connecting... check LED status.');
}
</script>
</body>
</html>
`