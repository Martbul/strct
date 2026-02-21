// internal/platform/executil/dev.go
//
// DevRunner wraps Real{} and stubs hardware-only commands that don't
// exist on a dev laptop (arp, iptables, hostapd, iwconfig, tc …).
//
// Commands that need to return data (arp -a, iw scan, tailscale status)
// return realistic fake output so the parsers in router/wifi/vpn work
// normally — the API responds with mock data instead of errors.
//
// Commands that are pure side-effects (iptables rules, systemctl, tc)
// are logged at DEBUG level and silently succeed.
//
// Nothing in this file should ever be imported by production code —
// it is selected only when cfg.IsDev == true in NewFromConfig().
package executil

import (
	"log/slog"
	"strings"
)

// DevRunner satisfies Runner. Wrap it around Real{} so any command we
// don't explicitly stub falls through to the real binary (e.g. frpc chmod).
type DevRunner struct{ real Runner }

func NewDevRunner() Runner { return &DevRunner{real: Real{}} }

// ── stub tables ───────────────────────────────────────────────────────────────

// silentOK — these commands are pure side-effects on real hardware.
// On a dev machine they either don't exist or would fail with permission
// denied. We log at DEBUG and return nil so callers never see an error.
var silentOK = map[string]bool{
	"iptables":       true,
	"ip6tables":      true,
	"iwconfig":       true,
	"iw":             true,
	"tc":             true,
	"killall":        true,
	"dhclient":       true,
	"wpa_supplicant": true,
	"hostapd":        true,
	"dnsmasq":        true,
	"tailscale":      true,
	"tailscaled":     true,
	"sysctl":         true,
}

// silentOKSystemctlActions — `systemctl <action> <unit>` pairs to stub.
// We only stub hardware-specific units; systemctl for anything else
// (e.g. a real service the dev machine has) falls through.
var silentOKSystemctlUnits = map[string]bool{
	"hostapd":    true,
	"dnsmasq":    true,
	"tailscaled": true,
}

// ── Runner interface ──────────────────────────────────────────────────────────

func (d *DevRunner) Run(name string, args ...string) error {
	if d.shouldStub(name, args) {
		slog.Debug("dev: stubbed (no-op)", "cmd", name, "args", strings.Join(args, " "))
		return nil
	}
	return d.real.Run(name, args...)
}

func (d *DevRunner) Output(name string, args ...string) ([]byte, error) {
	if out, ok := d.fakeOutput(name, args); ok {
		slog.Debug("dev: stubbed with fake output", "cmd", name)
		return out, nil
	}
	return d.real.Output(name, args...)
}

func (d *DevRunner) CombinedOutput(name string, args ...string) ([]byte, error) {
	if out, ok := d.fakeOutput(name, args); ok {
		slog.Debug("dev: stubbed with fake output", "cmd", name)
		return out, nil
	}
	return d.real.CombinedOutput(name, args...)
}

// ── decision logic ────────────────────────────────────────────────────────────

func (d *DevRunner) shouldStub(name string, args []string) bool {
	if silentOK[name] {
		return true
	}
	// systemctl start|stop|restart|kill <hardware-unit>
	if name == "systemctl" && len(args) >= 2 {
		unit := args[len(args)-1]
		action := args[0]
		hardwareAction := action == "start" || action == "stop" ||
			action == "restart" || action == "kill" || action == "is-active"
		if hardwareAction && silentOKSystemctlUnits[unit] {
			return true
		}
	}
	// Writing to /proc — ip_forward etc.
	if name == "sh" && len(args) >= 2 && strings.Contains(args[1], "/proc/sys") {
		return true
	}
	return false
}

// fakeOutput returns realistic stub data for commands that need to
// produce output consumed by parsers.
func (d *DevRunner) fakeOutput(name string, args []string) ([]byte, bool) {
	switch name {

	// ── arp -a  (router.go scanDevices, wifi.go refreshStatus) ───────────────
	case "arp":
		return []byte(fakeARP), true

	// ── ip neigh show  (fallback in router.go) ────────────────────────────────
	case "ip":
		if len(args) >= 2 && args[0] == "neigh" {
			return []byte(fakeIPNeigh), true
		}
		// ip addr add / flush / link set → silent no-op with empty output
		if len(args) >= 1 && (args[0] == "addr" || args[0] == "link") {
			return []byte(""), true
		}

	// ── iw dev wlan0 scan  (wifi.go handleScanNetworks) ──────────────────────
	case "iw":
		if len(args) >= 3 && args[2] == "scan" {
			return []byte(fakeIWScan), true
		}
		// iw dev wlan0 station dump (signal strength)
		if len(args) >= 3 && args[2] == "station" {
			return []byte(fakeStation), true
		}
		// iw dev wlan0_ap del / interface add → silent
		return []byte(""), true

	// ── tailscale status --json  (vpn.go refreshStatus) ──────────────────────
	case "tailscale":
		if len(args) >= 1 && args[0] == "status" {
			return []byte(fakeTailscaleStatus), true
		}

	// ── systemctl is-active <unit> ────────────────────────────────────────────
	case "systemctl":
		if len(args) >= 2 && args[0] == "is-active" {
			return []byte("inactive\n"), true
		}
	}

	return nil, false
}

// ── fake output constants ─────────────────────────────────────────────────────
// These match the exact format the parsers in router.go and wifi.go expect.

// fakeARP — three mock devices on wlan0.
// router.go regexp: `\(([\d.]+)\) at ([0-9a-f:]{17})`
const fakeARP = `? (192.168.100.50) at a1:b2:c3:d4:e5:f6 [ether] on wlan0
? (192.168.100.51) at de:ad:be:ef:ca:fe [ether] on wlan0
? (192.168.100.52) at 11:22:33:44:55:66 [ether] on wlan0
`

// fakeIPNeigh — same devices, ip-neigh format.
const fakeIPNeigh = `192.168.100.50 dev wlan0 lladdr a1:b2:c3:d4:e5:f6 REACHABLE
192.168.100.51 dev wlan0 lladdr de:ad:be:ef:ca:fe STALE
192.168.100.52 dev wlan0 lladdr 11:22:33:44:55:66 REACHABLE
`

// fakeIWScan — three visible networks.
// wifi.go parseIWScan: BSS, SSID:, freq:, signal:, Privacy
const fakeIWScan = `BSS aa:bb:cc:dd:ee:ff(on wlan0)
	SSID: HomeNetwork
	freq: 5180
	signal: -52.00 dBm
	capability: ESS Privacy
BSS 11:22:33:44:55:00(on wlan0)
	SSID: NeighboursWifi
	freq: 2437
	signal: -71.00 dBm
	capability: ESS Privacy
BSS de:ad:be:ef:00:01(on wlan0)
	SSID: OpenCafe
	freq: 2412
	signal: -85.00 dBm
	capability: ESS
`

// fakeStation — iw station dump output (signal per connected device).
const fakeStation = `Station a1:b2:c3:d4:e5:f6 (on wlan0)
	signal:  		-52 dBm
	tx bitrate:		144.4 MBit/s
Station de:ad:be:ef:ca:fe (on wlan0)
	signal:  		-67 dBm
	tx bitrate:		72.2 MBit/s
`

// fakeTailscaleStatus — vpn.go unmarshals this to check BackendState.
// "NeedsLogin" means "not connected" without being an error —
// the VPN feature shows as disabled, which is correct in dev mode.
const fakeTailscaleStatus = `{
  "BackendState": "NeedsLogin",
  "Self": { "TailscaleIPs": [], "HostName": "dev-laptop" },
  "Peer": {}
}`