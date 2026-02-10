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