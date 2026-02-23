//go:build integration

package vpn_test

func TestVPNApply_ConnectsToTailnet(t *testing.T) {
    testutil.RequireRoot(t)
    testutil.RequireBinary(t, "tailscale")
    testutil.RequireBinary(t, "tailscaled")

    authKey := os.Getenv("TAILSCALE_TEST_AUTH_KEY")
    if authKey == "" {
        t.Skip("set TAILSCALE_TEST_AUTH_KEY to run VPN integration tests")
    }

    // Fake wifi status — VPN just needs to know the subnet
    wifiStub := &wifiStub{
        status: wifi.Status{Active: true, SubnetBase: "192.168.200"},
    }

    svc := vpn.New(config.Config{}, executil.Real{}, wifiStub)
    svc.SetStateForTest(vpn.VPNConfig{
        Enabled:           true,
        AuthKey:           authKey,
        AdvertiseExitNode: false, // don't advertise exit node in tests
    })

    t.Cleanup(func() {
        exec.Command("tailscale", "down").Run()
    })

    err := svc.ApplyForTest()
    if err != nil {
        t.Fatalf("vpn.apply() failed: %v", err)
    }

    // Wait for tailscale to connect
    deadline := time.Now().Add(30 * time.Second)
    var connected bool
    for time.Now().Before(deadline) {
        out, _ := exec.Command("tailscale", "status", "--json").Output()
        var ts struct{ BackendState string }
        json.Unmarshal(out, &ts)
        if ts.BackendState == "Running" {
            connected = true
            break
        }
        time.Sleep(2 * time.Second)
    }

    if !connected {
        t.Error("tailscale did not reach Running state within 30s")
    }

    // Verify the status struct reflects reality
    status := svc.StatusForTest()
    if !status.TailscaleUp {
        t.Error("Status.TailscaleUp is false despite tailscale running")
    }
    if status.TailscaleIP == "" {
        t.Error("Status.TailscaleIP is empty")
    }
    t.Logf("connected with IP: %s", status.TailscaleIP)
}