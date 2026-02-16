package wifi

import (
	"fmt"
	"log"
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

//		cmd := exec.Command("nmcli", "dev", "wifi", "connect", ssid, "password", password)
//		return cmd.Run()
//	}
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

func (w *RealWiFi) StartHotspot() error {
	macSuffix := "XXXX" //! In prod, get real MAC

	ssid := "Strct-Setup-" + macSuffix
	password := "strct" + macSuffix

	log.Printf("[WIFI] Creating Hotspot on %s. SSID: %s", w.Interface, ssid)

	exec.Command("nmcli", "con", "delete", "Hotspot").Run()

	exec.Command("nmcli", "radio", "wifi", "on").Run()
	time.Sleep(1 * time.Second)

	log.Println("[WIFI] Adding Hotspot connection...")
// 3. ADD: Create the connection
	if err := exec.Command("nmcli", "con", "add", 
		"type", "wifi", 
		"ifname", w.Interface, 
		"con-name", "Hotspot", 
		"autoconnect", "yes", 
		"ssid", ssid).Run(); err != nil {
		return fmt.Errorf("failed to add connection: %v", err)
	}

	// 4. CONFIGURE
	// We batch these for cleaner code, but running them individually is fine too
	configCmds := [][]string{
		{"modify", "Hotspot", "wifi-sec.key-mgmt", "wpa-psk"},
		{"modify", "Hotspot", "wifi-sec.psk", password},
		{"modify", "Hotspot", "802-11-wireless.mode", "ap"},
		// {"modify", "Hotspot", "802-11-wireless.band", "bg"}, // <--- REMOVED: Let driver decide
		{"modify", "Hotspot", "ipv4.method", "shared"},
		{"modify", "Hotspot", "ipv4.addresses", "10.42.0.1/24"},
	}

	for _, args := range configCmds {
		if err := exec.Command("nmcli", append([]string{"con"}, args...)...).Run(); err != nil {
			log.Printf("[WIFI] Warning: Failed config step %v: %v", args, err)
		}
	}

	fmt.Println("[WIFI] Bringing up Hotspot...")
	
	// 5. UP: This automatically handles the disconnect of client mode
	output, err := exec.Command("nmcli", "con", "up", "Hotspot").CombinedOutput()
	if err != nil {
		// If it fails, print the detailed status of the device for debugging
		status, _ := exec.Command("nmcli", "dev", "show", w.Interface).CombinedOutput()
		return fmt.Errorf("failed to up hotspot: %s\nDEV STATUS:\n%s", string(output), string(status))
	}

	return nil
}

func (w *RealWiFi) StopHotspot() error {
	fmt.Println("[WIFI] Stopping Hotspot...")
	return exec.Command("nmcli", "con", "down", "Hotspot").Run()
}
