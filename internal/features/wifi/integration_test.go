//go:build integration

// internal/features/wifi/integration_test.go
package wifi_test

import (
    "net"
    "os"
    "strings"
    "testing"
    "time"

    "github.com/strct-org/strct-agent/internal/config"
    "github.com/strct-org/strct-agent/internal/features/wifi"
    "github.com/strct-org/strct-agent/internal/platform/executil"
    "github.com/strct-org/strct-agent/internal/testutil"
)

// TestRouterMode_HostapdStarts verifies that applyRouter actually
// brings up a WiFi AP that the kernel can see.
func TestRouterMode_HostapdStarts(t *testing.T) {
    testutil.RequireRoot(t)
    testutil.RequireInterface(t, "wlan0")
    testutil.RequireBinary(t, "hostapd")
    testutil.RequireBinary(t, "dnsmasq")

    svc := wifi.New(config.Config{}, executil.Real{})
    svc.SetState(wifi.WiFiConfig{
        Mode: wifi.ModeRouter,
        Router: wifi.RouterConfig{
            SSID:        "StrctIntTest",
            Password:    "integration123",
            Band:        "5GHz",
            Channel:     36,
            MaxClients:  5,
            SubnetBase:  "192.168.200",
            DNSProvider: "cloudflare",
        },
    })

    // Cleanup: always tear down after test, even on failure
    t.Cleanup(func() {
        svc.TeardownForTest()
        time.Sleep(500 * time.Millisecond)
    })

    err := svc.ApplyForTest()
    if err != nil {
        t.Fatalf("applyRouter failed: %v", err)
    }

    // Give hostapd time to initialize the interface
    time.Sleep(2 * time.Second)

    // Verify 1: hostapd process is actually running
    out, _ := exec.Command("systemctl", "is-active", "hostapd").Output()
    if strings.TrimSpace(string(out)) != "active" {
        t.Errorf("hostapd is not active after applyRouter, got: %q", string(out))
    }

    // Verify 2: wlan0 has the gateway IP we configured
    iface, err := net.InterfaceByName("wlan0")
    if err != nil {
        t.Fatalf("wlan0 not found: %v", err)
    }
    addrs, _ := iface.Addrs()
    hasGatewayIP := false
    for _, addr := range addrs {
        if strings.HasPrefix(addr.String(), "192.168.200.1") {
            hasGatewayIP = true
            break
        }
    }
    if !hasGatewayIP {
        t.Errorf("wlan0 does not have gateway IP 192.168.200.1, got: %v", addrs)
    }

    // Verify 3: dnsmasq is running
    out, _ = exec.Command("systemctl", "is-active", "dnsmasq").Output()
    if strings.TrimSpace(string(out)) != "active" {
        t.Errorf("dnsmasq is not active, got: %q", string(out))
    }

    // Verify 4: NAT rule is in place
    out, _ = exec.Command("iptables", "-t", "nat", "-L", "POSTROUTING", "-n").Output()
    if !strings.Contains(string(out), "MASQUERADE") {
        t.Error("iptables MASQUERADE rule not found in POSTROUTING chain")
    }

    // Verify 5: hostapd.conf was written with the right SSID
    conf, err := os.ReadFile("/etc/hostapd/hostapd.conf")
    if err != nil {
        t.Fatalf("cannot read hostapd.conf: %v", err)
    }
    if !strings.Contains(string(conf), "ssid=StrctIntTest") {
        t.Errorf("hostapd.conf does not contain expected SSID\n%s", conf)
    }

    // Verify 6: wifi.Status() reflects reality
    status := svc.Status()
    if !status.Active {
        t.Error("Status().Active is false after successful apply")
    }
    if status.Mode != wifi.ModeRouter {
        t.Errorf("Status().Mode = %q, want %q", status.Mode, wifi.ModeRouter)
    }
    if status.GatewayIP != "192.168.200.1" {
        t.Errorf("Status().GatewayIP = %q, want 192.168.200.1", status.GatewayIP)
    }
}

// TestRouterMode_Teardown verifies that stopping actually cleans up.
func TestRouterMode_Teardown(t *testing.T) {
    testutil.RequireRoot(t)
    testutil.RequireInterface(t, "wlan0")

    svc := wifi.New(config.Config{}, executil.Real{})
    // ... apply first, then tear down ...
    svc.TeardownForTest()
    time.Sleep(1 * time.Second)

    out, _ := exec.Command("systemctl", "is-active", "hostapd").Output()
    if strings.TrimSpace(string(out)) == "active" {
        t.Error("hostapd still active after teardown")
    }

    // wlan0 should not have the gateway IP anymore
    iface, _ := net.InterfaceByName("wlan0")
    addrs, _ := iface.Addrs()
    for _, addr := range addrs {
        if strings.HasPrefix(addr.String(), "192.168.200.1") {
            t.Error("gateway IP still assigned after teardown")
        }
    }
}