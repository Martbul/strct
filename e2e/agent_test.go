// // Package e2e contains end-to-end tests that build and run the real agent binary.
// // These tests are skipped in normal `go test ./...` runs via the build tag.
// // Run them explicitly with: go test -tags e2e ./e2e/...
// //
// // They require no real hardware — they use --dev mode which mocks WiFi and disk.
// //
// //go:build e2e
package e2e_test

// import (
// 	"context"
// 	"encoding/json"
// 	"fmt"
// 	"net/http"
// 	"os"
// 	"os/exec"
// 	"path/filepath"
// 	"testing"
// 	"time"
// )

// const agentPort = 18080

// // TestAgentHealthEndpoint builds the agent, starts it in dev mode,
// // hits the health endpoint, and verifies the response shape.
// func TestAgentHealthEndpoint(t *testing.T) {
// 	binary := buildAgent(t)
// 	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
// 	defer cancel()

// 	proc := startAgent(t, ctx, binary)
// 	defer proc.Process.Kill()

// 	waitForAgent(t, agentPort)

// 	resp, err := http.Get(fmt.Sprintf("http://localhost:%d/api/health", agentPort))
// 	if err != nil {
// 		t.Fatalf("GET /api/health: %v", err)
// 	}
// 	defer resp.Body.Close()

// 	if resp.StatusCode != http.StatusOK {
// 		t.Fatalf("expected 200, got %d", resp.StatusCode)
// 	}

// 	var body struct {
// 		Status   string `json:"status"`
// 		Internet bool   `json:"internet_access"`
// 	}
// 	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
// 		t.Fatalf("decode response: %v", err)
// 	}
// 	if body.Status != "ok" {
// 		t.Errorf("status = %q, want %q", body.Status, "ok")
// 	}
// }

// func TestAgentFilesEndpoint_EmptyOnStart(t *testing.T) {
// 	binary := buildAgent(t)
// 	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
// 	defer cancel()

// 	proc := startAgent(t, ctx, binary)
// 	defer proc.Process.Kill()

// 	waitForAgent(t, agentPort)

// 	resp, err := http.Get(fmt.Sprintf("http://localhost:%d/api/files", agentPort))
// 	if err != nil {
// 		t.Fatalf("GET /api/files: %v", err)
// 	}
// 	defer resp.Body.Close()

// 	if resp.StatusCode != http.StatusOK {
// 		t.Fatalf("expected 200, got %d", resp.StatusCode)
// 	}

// 	var body struct {
// 		Files []any `json:"files"`
// 	}
// 	json.NewDecoder(resp.Body).Decode(&body)
// 	// In dev mode with a fresh data dir, files should be empty.
// 	if len(body.Files) != 0 {
// 		t.Errorf("expected 0 files on fresh start, got %d", len(body.Files))
// 	}
// }

// // ---------------------------------------------------------------------------
// // Helpers
// // ---------------------------------------------------------------------------

// // buildAgent compiles the agent binary into a temp dir and returns the path.
// // Skips the test if the build fails (e.g. in CI without Go installed).
// func buildAgent(t *testing.T) string {
// 	t.Helper()
// 	tmp := t.TempDir()
// 	binary := filepath.Join(tmp, "strct-agent-e2e")

// 	cmd := exec.Command("go", "build",
// 		"-o", binary,
// 		"./cmd/agent",
// 	)
// 	cmd.Dir = findRepoRoot(t)
// 	out, err := cmd.CombinedOutput()
// 	if err != nil {
// 		t.Fatalf("build failed:\n%s", out)
// 	}
// 	return binary
// }

// // startAgent launches the agent in dev mode with a known port.
// func startAgent(t *testing.T, ctx context.Context, binary string) *exec.Cmd {
// 	t.Helper()
// 	tmp := t.TempDir() // fresh data dir per test

// 	cmd := exec.CommandContext(ctx, binary,
// 		"--dev",
// 		fmt.Sprintf("-port=%d", agentPort),
// 	)
// 	cmd.Env = append(os.Environ(),
// 		fmt.Sprintf("DATA_DIR=%s", tmp),
// 	)
// 	cmd.Stdout = os.Stdout
// 	cmd.Stderr = os.Stderr

// 	if err := cmd.Start(); err != nil {
// 		t.Fatalf("could not start agent: %v", err)
// 	}
// 	return cmd
// }

// // waitForAgent polls the health endpoint until it responds or timeout.
// func waitForAgent(t *testing.T, port int) {
// 	t.Helper()
// 	deadline := time.Now().Add(5 * time.Second)
// 	for time.Now().Before(deadline) {
// 		resp, err := http.Get(fmt.Sprintf("http://localhost:%d/api/health", port))
// 		if err == nil && resp.StatusCode == http.StatusOK {
// 			return
// 		}
// 		time.Sleep(100 * time.Millisecond)
// 	}
// 	t.Fatal("agent did not become ready within 5 seconds")
// }

// // findRepoRoot walks up from the test file until it finds go.mod.
// func findRepoRoot(t *testing.T) string {
// 	t.Helper()
// 	dir, err := os.Getwd()
// 	if err != nil {
// 		t.Fatal(err)
// 	}
// 	for {
// 		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
// 			return dir
// 		}
// 		parent := filepath.Dir(dir)
// 		if parent == dir {
// 			t.Fatal("could not find repo root (no go.mod found)")
// 		}
// 		dir = parent
// 	}
// }
