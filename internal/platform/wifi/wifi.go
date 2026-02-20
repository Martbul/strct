package wifi

import (
	"net/http"
	"time"

	"github.com/strct-org/strct-agent/internal/platform/executil"
)

type Network struct {
	SSID     string
	Security string
	Signal   int
}

//  RealWiFi and MockWiFi satisfy
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

func New(isArm64 bool) Provider {
	if isArm64 {
		return NewRealWiFi("wlan0", executil.Real{})
	}
	return &MockWiFi{}
}
