package setup

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"github.com/strct-org/strct-agent/internal/wifi"
)

// This channel tells Main that setup is done
type Credentials struct {
	SSID     string `json:"ssid"`
	Password string `json:"password"`
}

func StartCaptivePortal(wifiMgr wifi.Provider, done chan<- bool) {
	mux := http.NewServeMux()

	// 1. Endpoint to list networks (Phone asks "What WiFi is around?")
	mux.HandleFunc("/scan", func(w http.ResponseWriter, r *http.Request) {
		networks, err := wifiMgr.Scan()
		if err != nil {
			http.Error(w, err.Error(), 500)
			return
		}
		json.NewEncoder(w).Encode(networks)
	})

	// 2. Endpoint to connect (Phone sends "Use this WiFi")
	mux.HandleFunc("/connect", func(w http.ResponseWriter, r *http.Request) {
		var creds Credentials
		if err := json.NewDecoder(r.Body).Decode(&creds); err != nil {
			http.Error(w, "Invalid JSON", 400)
			return
		}

		log.Printf("[SETUP] Received credentials for %s", creds.SSID)
		
		// Attempt connection
		err := wifiMgr.Connect(creds.SSID, creds.Password)
		if err != nil {
			http.Error(w, "Failed to connect: "+err.Error(), 500)
			return
		}

		w.Write([]byte("Connected! Rebooting..."))
		
		// Signal main thread we are done
		done <- true
	})

	// 3. Serve a simple HTML page (The UI)
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		fmt.Fprint(w, htmlPage)
	})

	log.Println("[SETUP] Web Server listening on :80 (Port 8082 for Dev)")
	// In Prod (Pi) we use :80. In Dev (VM) we use :8082 to avoid conflict with Docker
	http.ListenAndServe(":8082", mux) 
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