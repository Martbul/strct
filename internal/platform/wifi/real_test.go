// Blackbox test: package wifi_test.
// We test the public API of RealWiFi through the wifi.Provider interface,
// using executil.Mock to avoid real nmcli calls.
package wifi_test

import (
	"errors"
	"testing"

	"github.com/strct-org/strct-agent/internal/platform/executil"
	"github.com/strct-org/strct-agent/internal/platform/wifi"
)

// ---------------------------------------------------------------------------
// Scan
// ---------------------------------------------------------------------------

func TestScan_ParsesNmcliOutput(t *testing.T) {
	runner := &executil.Mock{}
	runner.Expect(
		"nmcli -t -f SSID,SIGNAL,SECURITY dev wifi list --rescan yes",
		executil.MockResult{
			Output: []byte("HomeNet:85:WPA2\nOfficeWifi:72:WPA3\nHiddenNet:60:WPA2\n"),
		},
	)

	w := wifi.NewRealWiFi("wlan0", runner)
	networks, err := w.Scan()
	if err != nil {
		t.Fatalf("Scan() unexpected error: %v", err)
	}

	if len(networks) != 3 {
		t.Fatalf("expected 3 networks, got %d: %+v", len(networks), networks)
	}

	tests := []struct {
		ssid     string
		security string
	}{
		{"HomeNet", "WPA2"},
		{"OfficeWifi", "WPA3"},
		{"HiddenNet", "WPA2"},
	}
	for i, tt := range tests {
		if networks[i].SSID != tt.ssid {
			t.Errorf("networks[%d].SSID = %q, want %q", i, networks[i].SSID, tt.ssid)
		}
		if networks[i].Security != tt.security {
			t.Errorf("networks[%d].Security = %q, want %q", i, networks[i].Security, tt.security)
		}
	}
}

func TestScan_EmptyOutput_ReturnsEmptySlice(t *testing.T) {
	runner := &executil.Mock{}
	runner.Expect(
		"nmcli -t -f SSID,SIGNAL,SECURITY dev wifi list --rescan yes",
		executil.MockResult{Output: []byte("\n")},
	)

	w := wifi.NewRealWiFi("wlan0", runner)
	networks, err := w.Scan()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(networks) != 0 {
		t.Errorf("expected 0 networks, got %d", len(networks))
	}
}

func TestScan_NmcliFails_ReturnsError(t *testing.T) {
	runner := &executil.Mock{}
	runner.Expect(
		"nmcli -t -f SSID,SIGNAL,SECURITY dev wifi list --rescan yes",
		executil.MockResult{Err: errors.New("nmcli: command not found")},
	)

	w := wifi.NewRealWiFi("wlan0", runner)
	_, err := w.Scan()
	if err == nil {
		t.Error("expected error when nmcli fails, got nil")
	}
}

// ---------------------------------------------------------------------------
// Connect
// ---------------------------------------------------------------------------

func TestConnect_TearDownHotspotBeforeConnecting(t *testing.T) {
	runner := &executil.Mock{}
	// The final connect succeeds.
	runner.Expect("nmcli dev wifi connect HomeNet password s3cr3t",
		executil.MockResult{Output: []byte("Device 'wlan0' successfully activated")})

	w := wifi.NewRealWiFi("wlan0", runner)
	if err := w.Connect("HomeNet", "s3cr3t"); err != nil {
		t.Fatalf("Connect() error: %v", err)
	}

	// Hotspot teardown must happen before connect.
	runner.AssertCalled(t, "nmcli con down Hotspot")
	runner.AssertCalled(t, "nmcli con delete Hotspot")
	runner.AssertCalled(t, "nmcli dev wifi connect HomeNet password s3cr3t")
}

func TestConnect_NmcliFails_ReturnsError(t *testing.T) {
	runner := &executil.Mock{}
	runner.Expect("nmcli dev wifi connect BadNet password wrong",
		executil.MockResult{
			Output: []byte("Error: Connection activation failed."),
			Err:    errors.New("exit status 1"),
		},
	)

	w := wifi.NewRealWiFi("wlan0", runner)
	err := w.Connect("BadNet", "wrong")
	if err == nil {
		t.Error("expected error for failed connect, got nil")
	}
}

// ---------------------------------------------------------------------------
// StartHotspot
// ---------------------------------------------------------------------------

func TestStartHotspot_RunsRequiredCommands(t *testing.T) {
	runner := &executil.Mock{}
	// All commands succeed by default (zero MockResult = success).

	w := wifi.NewRealWiFi("wlan0", runner)
	if err := w.StartHotspot(); err != nil {
		t.Fatalf("StartHotspot() error: %v", err)
	}

	// These are the commands that MUST happen, in conceptual order.
	runner.AssertCalled(t, "nmcli con delete Hotspot")
	runner.AssertCalled(t, "nmcli radio wifi on")
	runner.AssertCalled(t, "nmcli con up Hotspot")

	// The add command must include the interface and ssid.
	found := false
	for _, c := range runner.Calls {
		if c.Name == "nmcli" && len(c.Args) > 0 && c.Args[0] == "con" {
			found = true
			break
		}
	}
	if !found {
		t.Error("expected nmcli con add to be called")
	}
}

func TestStartHotspot_UpFails_ReturnsError(t *testing.T) {
	runner := &executil.Mock{}
	runner.Expect("nmcli con up Hotspot",
		executil.MockResult{
			Output: []byte("Error: Could not activate connection."),
			Err:    errors.New("exit status 1"),
		},
	)
	// dev show (called in error path):
	runner.Expect("nmcli dev show wlan0", executil.MockResult{
		Output: []byte("GENERAL.STATE: 30 (disconnected)"),
	})

	w := wifi.NewRealWiFi("wlan0", runner)
	err := w.StartHotspot()
	if err == nil {
		t.Error("expected error when nmcli con up fails, got nil")
	}
}

// ---------------------------------------------------------------------------
// StopHotspot
// ---------------------------------------------------------------------------

func TestStopHotspot_RunsNmcliDown(t *testing.T) {
	runner := &executil.Mock{}

	w := wifi.NewRealWiFi("wlan0", runner)
	if err := w.StopHotspot(); err != nil {
		t.Fatalf("StopHotspot() error: %v", err)
	}

	runner.AssertCalled(t, "nmcli con down Hotspot")
}
