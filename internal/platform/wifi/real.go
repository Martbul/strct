// package wifi

// import (
// 	"fmt"
// 	"log"
// 	"os/exec"
// 	"strings"
// 	"time"
// )

// type RealWiFi struct {
// 	Interface string
// }

// func (w *RealWiFi) Scan() ([]Network, error) {
// 	cmd := exec.Command("nmcli", "-t", "-f", "SSID,SIGNAL,SECURITY", "dev", "wifi", "list", "--rescan", "yes")
// 	output, err := cmd.Output()
// 	if err != nil {
// 		return nil, fmt.Errorf("scan failed: %v", err)
// 	}

// 	var networks []Network
// 	lines := strings.Split(string(output), "\n")

// 	for _, line := range lines {
// 		parts := strings.Split(line, ":")
// 		if len(parts) < 3 {
// 			continue
// 		}

// 		if parts[0] == "" {
// 			continue
// 		}

// 		net := Network{
// 			SSID:     parts[0],
// 			Security: parts[2],
// 		}
// 		networks = append(networks, net)
// 	}
// 	return networks, nil
// }

// // func (w *RealWiFi) Connect(ssid, password string) error {
// // 	fmt.Printf("[WIFI] Connecting to %s...\n", ssid)
// // 	exec.Command("nmcli", "con", "delete", ssid).Run()

// //		cmd := exec.Command("nmcli", "dev", "wifi", "connect", ssid, "password", password)
// //		return cmd.Run()
// //	}
// func (w *RealWiFi) Connect(ssid, password string) error {
// 	fmt.Printf("[WIFI] Switching from Hotspot to Client for: %s\n", ssid)

// 	// 1. AGGRESSIVELY kill the hotspot to free the driver
// 	// We ignore errors here because the hotspot might not be running
// 	exec.Command("nmcli", "con", "down", "Hotspot").Run()
// 	exec.Command("nmcli", "con", "delete", "Hotspot").Run()

// 	// 2. Clean up previous connection attempts for this SSID
// 	exec.Command("nmcli", "con", "delete", ssid).Run()

// 	// 3. Connect to the new network
// 	cmd := exec.Command("nmcli", "dev", "wifi", "connect", ssid, "password", password)
// 	output, err := cmd.CombinedOutput()
// 	if err != nil {
// 		return fmt.Errorf("connection failed: %s, %v", string(output), err)
// 	}
// 	return nil
// }

// func (w *RealWiFi) StartHotspot() error {
// 	macSuffix := "XXXX" //! In prod, get real MAC

// 	ssid := "Strct-Setup-" + macSuffix
// 	password := "strct" + macSuffix

// 	log.Printf("[WIFI] Creating Hotspot on %s. SSID: %s", w.Interface, ssid)

// 	exec.Command("nmcli", "con", "delete", "Hotspot").Run()

// 	exec.Command("nmcli", "radio", "wifi", "on").Run()
// 	time.Sleep(1 * time.Second)

// 	log.Println("[WIFI] Adding Hotspot connection...")
// // 3. ADD: Create the connection
// 	if err := exec.Command("nmcli", "con", "add",
// 		"type", "wifi",
// 		"ifname", w.Interface,
// 		"con-name", "Hotspot",
// 		"autoconnect", "yes",
// 		"ssid", ssid).Run(); err != nil {
// 		return fmt.Errorf("failed to add connection: %v", err)
// 	}

// 	// 4. CONFIGURE
// 	// We batch these for cleaner code, but running them individually is fine too
// 	configCmds := [][]string{
// 		{"modify", "Hotspot", "wifi-sec.key-mgmt", "wpa-psk"},
// 		{"modify", "Hotspot", "wifi-sec.psk", password},
// 		{"modify", "Hotspot", "802-11-wireless.mode", "ap"},
// 		// {"modify", "Hotspot", "802-11-wireless.band", "bg"}, // <--- REMOVED: Let driver decide
// 		{"modify", "Hotspot", "ipv4.method", "shared"},
// 		{"modify", "Hotspot", "ipv4.addresses", "10.42.0.1/24"},
// 	}

// 	for _, args := range configCmds {
// 		if err := exec.Command("nmcli", append([]string{"con"}, args...)...).Run(); err != nil {
// 			log.Printf("[WIFI] Warning: Failed config step %v: %v", args, err)
// 		}
// 	}

// 	fmt.Println("[WIFI] Bringing up Hotspot...")

// 	// 5. UP: This automatically handles the disconnect of client mode
// 	output, err := exec.Command("nmcli", "con", "up", "Hotspot").CombinedOutput()
// 	if err != nil {
// 		// If it fails, print the detailed status of the device for debugging
// 		status, _ := exec.Command("nmcli", "dev", "show", w.Interface).CombinedOutput()
// 		return fmt.Errorf("failed to up hotspot: %s\nDEV STATUS:\n%s", string(output), string(status))
// 	}

// 	return nil
// }

// func (w *RealWiFi) StopHotspot() error {
// 	fmt.Println("[WIFI] Stopping Hotspot...")
// 	return exec.Command("nmcli", "con", "down", "Hotspot").Run()
// }

package wifi

import (
	"fmt"
	"log/slog"
	"os"
	"strings"
	"time"
)

// ---------------------------------------------------------------------------
// Narrow interface — wifi only needs Run and Output/CombinedOutput.
// We define our own so mocks can be minimal.
// ---------------------------------------------------------------------------

// commander is the subset of executil.Runner that wifi needs.
type commander interface {
	Run(name string, args ...string) error
	Output(name string, args ...string) ([]byte, error)
	CombinedOutput(name string, args ...string) ([]byte, error)
}

// ---------------------------------------------------------------------------
// RealWiFi
// ---------------------------------------------------------------------------

// RealWiFi manages network connections on real hardware via nmcli.
type RealWiFi struct {
	Interface string
	cmd       commander
}

// NewRealWiFi constructs a RealWiFi.
// In production, pass executil.Real{}.
// In tests, pass *executil.Mock.
func NewRealWiFi(iface string, cmd commander) *RealWiFi {
	return &RealWiFi{Interface: iface, cmd: cmd}
}

func (w *RealWiFi) Scan() ([]Network, error) {
	out, err := w.cmd.Output("nmcli", "-t", "-f", "SSID,SIGNAL,SECURITY",
		"dev", "wifi", "list", "--rescan", "yes")
	if err != nil {
		return nil, fmt.Errorf("wifi: scan failed: %w", err)
	}

	var networks []Network
	for _, line := range strings.Split(string(out), "\n") {
		parts := strings.Split(line, ":")
		if len(parts) < 3 || parts[0] == "" {
			continue
		}
		networks = append(networks, Network{
			SSID:     parts[0],
			Security: parts[2],
		})
	}
	return networks, nil
}

func (w *RealWiFi) Connect(ssid, password string) error {
	slog.Info("wifi: connecting to network", "ssid", ssid)

	// Kill hotspot cleanly first — errors are expected and ignored.
	w.cmd.Run("nmcli", "con", "down", "Hotspot")
	w.cmd.Run("nmcli", "con", "delete", "Hotspot")
	w.cmd.Run("nmcli", "con", "delete", ssid)

	out, err := w.cmd.CombinedOutput("nmcli", "dev", "wifi", "connect", ssid, "password", password)
	if err != nil {
		return fmt.Errorf("wifi: connect to %q failed: %s: %w", ssid, strings.TrimSpace(string(out)), err)
	}
	return nil
}

func (w *RealWiFi) StartHotspot() error {
	// Read the real MAC address from sysfs so the SSID is unique per device.
	macSuffix, err := w.macSuffix()
	if err != nil {
		slog.Warn("wifi: could not read MAC, using placeholder", "err", err)
		macSuffix = "XXXX"
	}

	ssid := "Strct-Setup-" + macSuffix
	password := "strct" + macSuffix

	slog.Info("wifi: starting hotspot", "interface", w.Interface, "ssid", ssid)

	// Remove any stale connection profile.
	w.cmd.Run("nmcli", "con", "delete", "Hotspot")
	w.cmd.Run("nmcli", "radio", "wifi", "on")
	time.Sleep(1 * time.Second)

	if err := w.cmd.Run("nmcli", "con", "add",
		"type", "wifi",
		"ifname", w.Interface,
		"con-name", "Hotspot",
		"autoconnect", "yes",
		"ssid", ssid,
	); err != nil {
		return fmt.Errorf("wifi: failed to add hotspot connection: %w", err)
	}

	configSteps := [][]string{
		{"modify", "Hotspot", "wifi-sec.key-mgmt", "wpa-psk"},
		{"modify", "Hotspot", "wifi-sec.psk", password},
		{"modify", "Hotspot", "802-11-wireless.mode", "ap"},
		{"modify", "Hotspot", "ipv4.method", "shared"},
		{"modify", "Hotspot", "ipv4.addresses", "10.42.0.1/24"},
	}
	for _, args := range configSteps {
		if err := w.cmd.Run("nmcli", append([]string{"con"}, args...)...); err != nil {
			slog.Warn("wifi: hotspot config step failed", "args", args, "err", err)
		}
	}

	out, err := w.cmd.CombinedOutput("nmcli", "con", "up", "Hotspot")
	if err != nil {
		status, _ := w.cmd.CombinedOutput("nmcli", "dev", "show", w.Interface)
		return fmt.Errorf("wifi: failed to bring up hotspot: %s\ndev status:\n%s",
			strings.TrimSpace(string(out)), string(status))
	}

	return nil
}

func (w *RealWiFi) StopHotspot() error {
	slog.Info("wifi: stopping hotspot")
	return w.cmd.Run("nmcli", "con", "down", "Hotspot")
}

// macSuffix reads the last 4 hex characters of the interface MAC address.
// This makes the hotspot SSID unique per physical device without any
// network calls.
func (w *RealWiFi) macSuffix() (string, error) {
	data, err := os.ReadFile("/sys/class/net/" + w.Interface + "/address")
	if err != nil {
		return "", err
	}
	mac := strings.TrimSpace(string(data))
	// MAC format: aa:bb:cc:dd:ee:ff — take last 4 hex chars (no colon)
	mac = strings.ReplaceAll(mac, ":", "")
	if len(mac) < 4 {
		return "", fmt.Errorf("unexpected MAC length: %q", mac)
	}
	return strings.ToUpper(mac[len(mac)-4:]), nil
}
