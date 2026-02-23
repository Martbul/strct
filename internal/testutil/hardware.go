package testutil

import (
	"net"
	"os"
	"os/exec"
	"strings"
	"testing"
)

// RequireRoot skips the test if not running as root.
// Integration tests need root for iptables, hostapd, etc.
func RequireRoot(t *testing.T) {
    t.Helper()
    if os.Getuid() != 0 {
        t.Skip("integration test requires root — run with sudo")
    }
}

// RequireBinary skips if a required binary isn't in PATH.
func RequireBinary(t *testing.T, name string) {
    t.Helper()
    if _, err := exec.LookPath(name); err != nil {
        t.Skipf("integration test requires %q in PATH — not found", name)
    }
}

// RequireInterface skips if a network interface doesn't exist.
func RequireInterface(t *testing.T, iface string) {
    t.Helper()
    ifaces, err := net.Interfaces()
    if err != nil {
        t.Skipf("cannot list interfaces: %v", err)
    }
    for _, i := range ifaces {
        if i.Name == iface {
            return
        }
    }
    t.Skipf("integration test requires interface %q — not present", iface)
}

// RequireOrangePI checks for hardware-specific markers.
func RequireOrangePi(t *testing.T) {
    t.Helper()
    model, err := os.ReadFile("/proc/device-tree/model")
    if err != nil || !strings.Contains(string(model), "Orange Pi") {
        t.Skip("integration test requires Orange Pi hardware")
    }
}