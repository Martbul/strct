package setup

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/strct-org/strct-agent/internal/wifi"
)

type SpyWiFi struct {
	HotspotStarted bool
	ConnectCalled  bool
	SSIDReceived   string
	PassReceived   string
}

func (s *SpyWiFi) Scan() ([]wifi.Network, error) {
	return []wifi.Network{{SSID: "TestNet", Signal: 100}}, nil
}

func (s *SpyWiFi) Connect(ssid, password string) error {
	s.ConnectCalled = true
	s.SSIDReceived = ssid
	s.PassReceived = password
	return nil
}

func (s *SpyWiFi) StartHotspot(ssid, password string) error {
	s.HotspotStarted = true
	return nil
}

func (s *SpyWiFi) StopHotspot() error {
	s.HotspotStarted = false
	return nil
}

func TestCaptivePortalFlow(t *testing.T) {
	// A. Setup the Mock
	mockWifi := &SpyWiFi{}
	done := make(chan bool)

	// B. Start the Portal in a Goroutine (Simulating the device running)
	// Note: In your real code, you used http.ListenAndServe(":8082"). 
	// For tests, it's better to pass the ServeMux to httptest, but 
	// assuming your function blocks, we run it in background.
	// *Modification needed in your code*: Split logic so we can test the handler without binding a port.
	
	// Let's test the Logic directly using httptest (Cleaner approach)
	
	// 1. Initialize logic
	// We are manually doing what StartCaptivePortal does internally to test the handlers
	mux := http.NewServeMux()
	mux.HandleFunc("/connect", func(w http.ResponseWriter, r *http.Request) {
		var creds Credentials
		if err := json.NewDecoder(r.Body).Decode(&creds); err != nil {
			http.Error(w, "Invalid JSON", 400)
			return
		}
		// Call the mock
		mockWifi.Connect(creds.SSID, creds.Password)
		done <- true
	})

	// 2. Create a simulated HTTP Server
	ts := httptest.NewServer(mux)
	defer ts.Close()

	// 3. Simulate the Phone sending credentials
	phonePayload := []byte(`{"ssid":"HomeWiFi", "password":"secretpassword"}`)
	resp, err := http.Post(ts.URL+"/connect", "application/json", bytes.NewBuffer(phonePayload))
	if err != nil {
		t.Fatalf("Failed to send request: %v", err)
	}

	if resp.StatusCode != 200 {
		t.Errorf("Expected 200 OK, got %d", resp.StatusCode)
	}

	// 4. Wait for the 'done' signal (Simulate main thread waiting)
	select {
	case <-done:
		// Success!
	case <-time.After(2 * time.Second):
		t.Fatal("Timeout: Server never signalled completion")
	}

	// 5. Verify the Logic (Did it actually try to connect?)
	if !mockWifi.ConnectCalled {
		t.Error("Logic Error: WiFi.Connect() was never called")
	}
	if mockWifi.SSIDReceived != "HomeWiFi" {
		t.Errorf("Expected SSID 'HomeWiFi', got '%s'", mockWifi.SSIDReceived)
	}
}