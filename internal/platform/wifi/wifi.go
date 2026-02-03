package wifi

type Network struct {
	SSID     string
	Signal   int
	Security string
}

type Provider interface {
	Scan() ([]Network, error)
	Connect(ssid, password string) error
	StartHotspot(ssid, password string) error
	StopHotspot() error
}