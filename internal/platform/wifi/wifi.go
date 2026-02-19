package wifi

import (
	"net/http"
	"time"
)

type Network struct {
	SSID     string
	Security string
	Signal   int
}

// Provider is the interface both RealWiFi and MockWiFi satisfy.
type Provider interface {
	Scan() ([]Network, error)
	Connect(ssid, password string) error
	StartHotspot() error
	StopHotspot() error
}

func HasInternet() bool {
	client := http.Client{Timeout: 3 * time.Second}
	_, err := client.Get("http://clients3.google.com/generate_204")
	return err == nil
}

// New returns the appropriate Provider based on whether we are on real ARM64 hardware.
func New(isArm64 bool) Provider {
	if isArm64 {
		return &RealWiFi{Interface: "wlan0"}
	}
	return &MockWiFi{}
}