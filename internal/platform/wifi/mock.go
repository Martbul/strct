package wifi

import "fmt"

type MockWiFi struct {
	IsHotspotRunning bool
}

func (m *MockWiFi) Scan() ([]Network, error) {
	return []Network{
		{SSID: "Test_Net", Signal: 99, Security: "WPA2"},
	}, nil
}

func (m *MockWiFi) Connect(ssid, password string) error {
	fmt.Printf("[MOCK] Connected to %s\n", ssid)
	return nil
}

func (m *MockWiFi) StartHotspot() error {
	ssid := "mock_hotspot"
	password := "mock_password"
	m.IsHotspotRunning = true
	fmt.Printf("[MOCK] >>> HOTSPOT STARTED <<<\n")
	fmt.Printf("[MOCK] Name: %s | Pass: %s\n", ssid, password)
	fmt.Printf("[MOCK] Access via: http://localhost:8082\n")
	return nil
}

func (m *MockWiFi) StopHotspot() error {
	m.IsHotspotRunning = false
	fmt.Println("[MOCK] >>> HOTSPOT STOPPED <<<")
	return nil
}
