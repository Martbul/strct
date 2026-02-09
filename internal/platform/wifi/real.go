package wifi

import (
	"fmt"
	"os/exec"
	"strings"
	"time"
)

type RealWiFi struct {
	Interface string
}


func (w *RealWiFi) Scan() ([]Network, error) {
	cmd := exec.Command("nmcli", "-t", "-f", "SSID,SIGNAL,SECURITY", "dev", "wifi", "list", "--rescan", "yes")
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
		
		if parts[0] == "" {
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

// func (w *RealWiFi) Connect(ssid, password string) error {
// 	fmt.Printf("[WIFI] Connecting to %s...\n", ssid)
// 	exec.Command("nmcli", "con", "delete", ssid).Run()
	
// 	cmd := exec.Command("nmcli", "dev", "wifi", "connect", ssid, "password", password)
// 	return cmd.Run()
// }
func (w *RealWiFi) Connect(ssid, password string) error {
	fmt.Printf("[WIFI] Switching from Hotspot to Client for: %s\n", ssid)

    // 1. AGGRESSIVELY kill the hotspot to free the driver
	// We ignore errors here because the hotspot might not be running
	exec.Command("nmcli", "con", "down", "Hotspot").Run()
	exec.Command("nmcli", "con", "delete", "Hotspot").Run() 

	// 2. Clean up previous connection attempts for this SSID
	exec.Command("nmcli", "con", "delete", ssid).Run()
	
	// 3. Connect to the new network
	cmd := exec.Command("nmcli", "dev", "wifi", "connect", ssid, "password", password)
	output, err := cmd.CombinedOutput()
    if err != nil {
        return fmt.Errorf("connection failed: %s, %v", string(output), err)
    }
    return nil
}

func (w *RealWiFi) StartHotspot(ssid, password string) error {
	fmt.Printf("[WIFI] Initializing Hotspot: %s\n", ssid)

	exec.Command("nmcli", "dev", "disconnect", w.Interface).Run()
	
	exec.Command("nmcli", "con", "delete", "Hotspot").Run()

	time.Sleep(1 * time.Second) 


	fmt.Println("[WIFI] Adding Hotspot connection...")
	
	if err := exec.Command("nmcli", "con", "add", "type", "wifi", "ifname", w.Interface, "con-name", "Hotspot", "autoconnect", "yes", "ssid", ssid).Run(); err != nil {
		return fmt.Errorf("failed to add connection: %v", err)
	}

	if err := exec.Command("nmcli", "con", "modify", "Hotspot", "wifi-sec.key-mgmt", "wpa-psk").Run(); err != nil {
		return fmt.Errorf("failed to set security type: %v", err)
	}
	if err := exec.Command("nmcli", "con", "modify", "Hotspot", "wifi-sec.psk", password).Run(); err != nil {
		return fmt.Errorf("failed to set password: %v", err)
	}

	if err := exec.Command("nmcli", "con", "modify", "Hotspot", "802-11-wireless.mode", "ap").Run(); err != nil {
		return fmt.Errorf("failed to set AP mode: %v", err)
	}
	if err := exec.Command("nmcli", "con", "modify", "Hotspot", "802-11-wireless.band", "bg").Run(); err != nil {
		return fmt.Errorf("failed to set band: %v", err)
	}

	if err := exec.Command("nmcli", "con", "modify", "Hotspot", "ipv4.method", "shared").Run(); err != nil {
		return fmt.Errorf("failed to set ipv4 shared: %v", err)
	}
    exec.Command("nmcli", "con", "modify", "Hotspot", "ipv4.addresses", "10.42.0.1/24").Run()

	fmt.Println("[WIFI] Bringing up Hotspot...")
	output, err := exec.Command("nmcli", "con", "up", "Hotspot").CombinedOutput()
	if err != nil {
		return fmt.Errorf("failed to bring up hotspot: %s (Err: %v)", string(output), err)
	}

	return nil
}



func (w *RealWiFi) StopHotspot() error {
	fmt.Println("[WIFI] Stopping Hotspot...")
	return exec.Command("nmcli", "con", "down", "Hotspot").Run()
}
