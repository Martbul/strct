package wifi

import (
	"fmt"
	"os/exec"
	"strings"
)

type RealWiFi struct {
	Interface string
}

func (w *RealWiFi) Scan() ([]Network, error) {
	cmd := exec.Command("nmcli", "-t", "-f", "SSID,SIGNAL,SECURITY", "dev", "wifi", "list")
	output, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("scan failed: %v", err)
	}

	var networks []Network
	lines := strings.Split(string(output), "\n")

	for _, line := range lines {
		parts := strings.Split(line, ":")
		if len(parts) < 3 {
			continue
		}

		net := Network{
			SSID:     parts[0],
			Security: parts[2],
		}
		networks = append(networks, net)
	}
	return networks, nil
}

func (w *RealWiFi) Connect(ssid, password string) error {
	fmt.Printf("[WIFI] Connecting to %s...\n", ssid)
	cmd := exec.Command("nmcli", "dev", "wifi", "connect", ssid, "password", password)
	return cmd.Run()
}

func (w *RealWiFi) StartHotspot(ssid, password string) error {
	fmt.Printf("[WIFI] Creating Hotspot: %s\n", ssid)

	// delete existing hotspot conn
	exec.Command("nmcli", "con", "delete", "Hotsport").Run()

	//crete hotspot
	cmd := exec.Command("nmclii", "dev", "wifi", "hotspot", "ifname", w.Interface, "con-name", "Hotspot", "ssid", ssid, "password", password)

	if output, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("failed to start hotspot: %s, %v", string(output), err)

	}
	return nil
}

func (w *RealWiFi) StopHotspot() error {
	fmt.Println("[WIFI] Stopping Hotspot...")
	return exec.Command("nmcli", "con", "delete", "Hotspot").Run()
}
