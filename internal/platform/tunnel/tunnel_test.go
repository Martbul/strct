// // Blackbox test for the tunnel service.
// // We verify config file generation and the runner interactions
// // without ever starting a real frpc process.
package tunnel_test

// import (
// 	"context"
// 	"os"
// 	"path/filepath"
// 	"strings"
// 	"testing"
// 	"time"

// 	"github.com/strct-org/strct-agent/internal/platform/executil"
// 	"github.com/strct-org/strct-agent/internal/platform/tunnel"
// )

// // writeFakeBinary creates a shell script that acts as a long-running process.
// // This lets runLoop actually start it without needing a real frpc binary.
// func writeFakeBinary(t *testing.T, dir string) string {
// 	t.Helper()
// 	path := filepath.Join(dir, "frpc")
// 	// Script sleeps until killed — simulates a long-running frpc process.
// 	if err := os.WriteFile(path, []byte("#!/bin/sh\nsleep 999"), 0755); err != nil {
// 		t.Fatalf("could not write fake frpc binary: %v", err)
// 	}
// 	return path
// }

// // ---------------------------------------------------------------------------
// // Config file generation
// // ---------------------------------------------------------------------------

// func TestStart_WritesFrpcConfigWithCorrectContent(t *testing.T) {
// 	tmp := t.TempDir()
// 	writeFakeBinary(t, tmp)

// 	orig, _ := os.Getwd()
// 	os.Chdir(tmp)
// 	defer os.Chdir(orig)

// 	runner := &executil.Mock{}
// 	svc := tunnel.New(tunnel.Config{
// 		ServerIP:   "10.0.0.1",
// 		ServerPort: 7000,
// 		AuthToken:  "tok-abc",
// 		DeviceID:   "device-xyz",
// 		DataDir:    tmp,
// 		LocalPort:  8080,
// 	}, runner)

// 	ctx, cancel := context.WithCancel(context.Background())
// 	defer cancel()

// 	if err := svc.Start(ctx); err != nil {
// 		t.Fatalf("Start() error: %v", err)
// 	}

// 	content, err := os.ReadFile(filepath.Join(tmp, "frpc.toml"))
// 	if err != nil {
// 		t.Fatalf("frpc.toml not written: %v", err)
// 	}
// 	got := string(content)

// 	checks := map[string]string{
// 		"server address": `serverAddr = "10.0.0.1"`,
// 		"server port":    `serverPort = 7000`,
// 		"auth token":     `auth.token = "tok-abc"`,
// 		"proxy name":     `name = "web_device-xyz"`,
// 		"subdomain":      `subdomain = "device-xyz"`,
// 		"local port":     `localPort = 8080`,
// 	}
// 	for field, want := range checks {
// 		if !strings.Contains(got, want) {
// 			t.Errorf("frpc.toml missing %s: expected to find %q\nfull content:\n%s", field, want, got)
// 		}
// 	}
// }

// // ---------------------------------------------------------------------------
// // Missing binary
// // ---------------------------------------------------------------------------

// func TestStart_MissingBinary_ReturnsError(t *testing.T) {
// 	tmp := t.TempDir()
// 	// Do NOT write a binary — frpc doesn't exist.

// 	orig, _ := os.Getwd()
// 	os.Chdir(tmp)
// 	defer os.Chdir(orig)

// 	svc := tunnel.New(tunnel.Config{DataDir: tmp}, &executil.Mock{})

// 	err := svc.Start(context.Background())
// 	if err == nil {
// 		t.Fatal("expected error for missing binary, got nil")
// 	}
// 	if !strings.Contains(err.Error(), "not found") {
// 		t.Errorf("error should mention 'not found', got: %v", err)
// 	}
// }

// // ---------------------------------------------------------------------------
// // Runner interactions (chmod)
// // ---------------------------------------------------------------------------

// func TestStart_ChmodsTheBinary(t *testing.T) {
// 	tmp := t.TempDir()
// 	binaryPath := writeFakeBinary(t, tmp)

// 	orig, _ := os.Getwd()
// 	os.Chdir(tmp)
// 	defer os.Chdir(orig)

// 	runner := &executil.Mock{}
// 	svc := tunnel.New(tunnel.Config{
// 		DeviceID: "dev-1",
// 		DataDir:  tmp,
// 	}, runner)

// 	ctx, cancel := context.WithCancel(context.Background())
// 	defer cancel()

// 	svc.Start(ctx)

// 	runner.AssertCalled(t, "chmod +x "+binaryPath)
// }

// // ---------------------------------------------------------------------------
// // Context cancellation stops the run loop
// // ---------------------------------------------------------------------------

// func TestStart_ContextCancellation_StopsLoop(t *testing.T) {
// 	tmp := t.TempDir()
// 	writeFakeBinary(t, tmp)

// 	orig, _ := os.Getwd()
// 	os.Chdir(tmp)
// 	defer os.Chdir(orig)

// 	svc := tunnel.New(tunnel.Config{
// 		DeviceID: "dev-1",
// 		DataDir:  tmp,
// 	}, &executil.Mock{})

// 	ctx, cancel := context.WithCancel(context.Background())

// 	if err := svc.Start(ctx); err != nil {
// 		t.Fatalf("Start() error: %v", err)
// 	}

// 	// Let the goroutine start the fake binary.
// 	time.Sleep(100 * time.Millisecond)

// 	// Cancel and give the goroutine time to notice.
// 	cancel()
// 	time.Sleep(200 * time.Millisecond)

// 	// If we reach here without hanging, the loop exited correctly.
// 	// In a real test suite you'd use a done channel or sync.WaitGroup
// 	// exposed via a test-only hook. For now, timing is acceptable.
// }
