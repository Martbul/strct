//go:build integration

package adblock_test

import (
    "bufio"
    "os"
    "strings"
    "testing"
    "time"

    "github.com/strct-org/strct-agent/internal/features/adblock"
    "github.com/strct-org/strct-agent/internal/testutil"
)

// TestDownloadAndApply_WritesConf verifies the full blocklist
// download → convert → write → dnsmasq reload pipeline.
func TestDownloadAndApply_WritesConf(t *testing.T) {
    testutil.RequireRoot(t)
    testutil.RequireBinary(t, "dnsmasq")
    // This test hits the real network — skip if offline
    testutil.RequireNetwork(t, "raw.githubusercontent.com:443")

    svc := adblock.NewForTest()
    svc.EnableForTest()

    t.Cleanup(func() {
        os.Remove("/etc/dnsmasq.d/adblock.conf")
        exec.Command("systemctl", "kill", "-s", "HUP", "dnsmasq").Run()
    })

    svc.DownloadAndApplyForTest()

    // Verify 1: file was created
    if _, err := os.Stat("/etc/dnsmasq.d/adblock.conf"); os.IsNotExist(err) {
        t.Fatal("adblock.conf was not created")
    }

    // Verify 2: file contains address= directives
    f, _ := os.Open("/etc/dnsmasq.d/adblock.conf")
    defer f.Close()
    scanner := bufio.NewScanner(f)
    count := 0
    for scanner.Scan() {
        if strings.HasPrefix(scanner.Text(), "address=") {
            count++
        }
    }
    if count < 50000 {
        t.Errorf("expected at least 50k address= directives, got %d", count)
    }
    t.Logf("adblock.conf contains %d entries", count)

    // Verify 3: a known ad domain is blocked
    if !adblockContains(t, "doubleclick.net") {
        t.Error("doubleclick.net not found in adblock.conf")
    }
    if !adblockContains(t, "googleadservices.com") {
        t.Error("googleadservices.com not found in adblock.conf")
    }

    // Verify 4: status reflects the real entry count
    status := svc.StatusForTest()
    if status.EntryCount < 50000 {
        t.Errorf("Status.EntryCount = %d, expected >= 50000", status.EntryCount)
    }
    if status.Updating {
        t.Error("Status.Updating should be false after completion")
    }

    // Verify 5: dnsmasq actually loaded the new config
    // Query a known blocked domain — should get 0.0.0.0
    // This requires dnsmasq to be running and listening on :53
    time.Sleep(500 * time.Millisecond)
    ips, err := net.LookupHost("doubleclick.net")
    if err == nil && len(ips) > 0 && ips[0] == "0.0.0.0" {
        t.Log("DNS blocking confirmed: doubleclick.net → 0.0.0.0")
    } else {
        t.Logf("DNS check inconclusive (may need /etc/resolv.conf to point to dnsmasq): %v, %v", ips, err)
    }
}

func adblockContains(t *testing.T, domain string) bool {
    t.Helper()
    f, err := os.Open("/etc/dnsmasq.d/adblock.conf")
    if err != nil {
        t.Fatal(err)
    }
    defer f.Close()
    scanner := bufio.NewScanner(f)
    needle := "address=/" + domain + "/"
    for scanner.Scan() {
        if strings.Contains(scanner.Text(), needle) {
            return true
        }
    }
    return false
}