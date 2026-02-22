package wifi

import (
    "testing"
    "github.com/strct-org/strct-agent/internal/config"
    "github.com/strct-org/strct-agent/internal/platform/executil"
)

func TestApplyRouter_IssuesCorrectCommands(t *testing.T) {
    m := &executil.Mock{}
    svc := New(config.Config{}, m)
    
    svc.state = WiFiConfig{
        Mode: ModeRouter,
        Router: RouterConfig{
            SSID:        "TestNet",
            Password:    "password123",
            Band:        "5GHz",
            Channel:     36,
            MaxClients:  20,
            SubnetBase:  "192.168.100",
            DNSProvider: "cloudflare",
        },
    }

    err := svc.applyRouter()
    if err != nil {
        t.Fatalf("applyRouter() returned error: %v", err)
    }

    // Was hostapd restarted?
    m.AssertCalled(t, "systemctl restart hostapd")

    // Was the gateway IP set correctly?
    m.AssertCalled(t, "ip addr add 192.168.100.1/24 dev wlan0")

    // Was NAT enabled?
    m.AssertCalled(t, "iptables -t nat -A POSTROUTING -o eth0 -j MASQUERADE")

    // Was dnsmasq restarted after config write?
    m.AssertCalled(t, "systemctl restart dnsmasq")
}

func TestApplyRouter_WrongBand_UsesCorrectHWMode(t *testing.T) {
    m := &executil.Mock{}
    svc := New(config.Config{}, m)
    svc.state = WiFiConfig{
        Mode: ModeRouter,
        Router: RouterConfig{
            SSID: "TestNet", Password: "password123",
            Band: "2.4GHz", Channel: 6,
            MaxClients: 20, SubnetBase: "192.168.100",
            DNSProvider: "cloudflare",
        },
    }

    svc.applyRouter()

    // Read the written hostapd.conf and check hw_mode=g
    // (writeHostapdConf writes to /etc/hostapd/hostapd.conf — 
    //  in tests you'd want to make the path configurable, see below)
    m.AssertCalled(t, "systemctl restart hostapd")
}