// Package tunnel manages the frpc reverse proxy process that exposes the
// local agent HTTP server through a VPS-side frps instance.
package tunnel

import (
	"bytes"
	"context"
	"fmt"
	"html/template"
	"log/slog"
	"os"
	"path/filepath"
	"time"

	"github.com/strct-org/strct-agent/internal/config"
	"github.com/strct-org/strct-agent/internal/platform/executil"
)

// ---------------------------------------------------------------------------
// Narrow interface — defined here, consumed here.
// Satisfied by executil.Real in prod and executil.Mock in tests.
// We only need Run (not Output or CombinedOutput) so the interface is minimal.
// ---------------------------------------------------------------------------

// processRunner is the subset of executil.Runner that tunnel needs.
// Keeping it narrow means mocks only need to implement Run.
//
// Note: tunnel uses exec.CommandContext for the frpc process itself (so that
// ctx cancellation kills the child process). That's done directly via
// os/exec because it needs the context-aware variant — the runner is used
// only for setup steps like chmod.
type processRunner interface {
	Run(name string, args ...string) error
}

// ---------------------------------------------------------------------------
// Config — tunnel's own config struct, not a raw *config.Config dependency.
// This makes the service testable without building a full global config.
// ---------------------------------------------------------------------------

// Config holds everything the tunnel service needs to operate.
type Config struct {
	ServerIP   string
	ServerPort int
	AuthToken  string
	DeviceID   string
	DataDir    string
	LocalPort  int
}

// ---------------------------------------------------------------------------
// Service
// ---------------------------------------------------------------------------

// Service manages the frpc child process lifecycle.
type Service struct {
	cfg    Config
	runner processRunner
}

// New is the base constructor. Use NewFromConfig in application code.
// Pass executil.Real{} for runner in production.
func New(cfg Config, runner processRunner) *Service {
	return &Service{cfg: cfg, runner: runner}
}

// NewFromConfig constructs a Service from the global application config.
// This is what main.go calls — it injects the real OS runner automatically.
func NewFromConfig(cfg *config.Config) *Service {
	return New(
		Config{
			ServerIP:   cfg.VPSIP,
			ServerPort: cfg.VPSPort,
			AuthToken:  cfg.AuthToken,
			DeviceID:   cfg.DeviceID,
			DataDir:    cfg.DataDir,
			LocalPort:  8080,
		},
		executil.Real{}, // production: real os/exec
	)
}

// Start implements agent.Service.
// It writes the frpc config file, then runs frpc in a restart loop
// that respects context cancellation.
func (s *Service) Start(ctx context.Context) error {
	projectRoot, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("tunnel: could not determine working directory: %w", err)
	}

	frpcBinary := filepath.Join(projectRoot, "frpc")
	frpcConfig := filepath.Join(s.cfg.DataDir, "frpc.toml")

	// Fail fast if the binary isn't present — no point proceeding.
	if _, err := os.Stat(frpcBinary); os.IsNotExist(err) {
		slog.Error("tunnel: frpc binary missing",
			"path", frpcBinary,
			"hint", "wget https://github.com/fatedier/frp/releases/download/v0.61.0/frp_0.61.0_linux_arm64.tar.gz",
		)
		return fmt.Errorf("tunnel: frpc binary not found at %s", frpcBinary)
	}

	if err := s.writeConfig(frpcConfig); err != nil {
		return err
	}

	// chmod +x — use the injected runner so tests don't need a real binary.
	if err := s.runner.Run("chmod", "+x", frpcBinary); err != nil {
		// Non-fatal: binary might already be executable.
		slog.Warn("tunnel: could not chmod binary", "path", frpcBinary, "err", err)
	}

	go s.runLoop(ctx, frpcBinary, frpcConfig)
	return nil
}

// runLoop runs frpc and restarts it if it exits unexpectedly.
// It exits cleanly when ctx is cancelled.
func (s *Service) runLoop(ctx context.Context, binary, cfgPath string) {
	for {
		// Check for cancellation before each attempt.
		select {
		case <-ctx.Done():
			slog.Info("tunnel: stopped")
			return
		default:
		}

		slog.Info("tunnel: starting frpc")

		// exec.CommandContext kills the child process when ctx is cancelled.
		// This is why we use os/exec directly here instead of the runner —
		// we need the context-aware variant.
		//
		// If you ever need to test runLoop, you can extract this into a
		// "processLauncher" interface with a single RunContext method.
		// For now, keeping it simple is the right call.
		cmd := newCommand(ctx, binary, "-c", cfgPath)

		if err := cmd.Run(); err != nil {
			if ctx.Err() != nil {
				// Context was cancelled — this exit was expected.
				slog.Info("tunnel: frpc stopped by context cancellation")
				return
			}
			slog.Error("tunnel: frpc exited unexpectedly, restarting",
				"err", err,
				"delay", "5s",
			)
		}

		// Wait before restarting, but wake immediately if ctx is cancelled.
		select {
		case <-ctx.Done():
			return
		case <-time.After(5 * time.Second):
		}
	}
}

// writeConfig renders the frpc TOML config and writes it to disk.
func (s *Service) writeConfig(path string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return fmt.Errorf("tunnel: could not create config directory: %w", err)
	}

	tmpl, err := template.New("frpc").Parse(frpConfigTmpl)
	if err != nil {
		// This is a programming error (bad template literal), not a runtime one.
		panic(fmt.Sprintf("tunnel: frpc config template is invalid: %v", err))
	}

	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, templateData{
		ServerIP:   s.cfg.ServerIP,
		ServerPort: s.cfg.ServerPort,
		Token:      s.cfg.AuthToken,
		DeviceID:   s.cfg.DeviceID,
		LocalPort:  s.cfg.LocalPort,
	}); err != nil {
		return fmt.Errorf("tunnel: could not render frpc config: %w", err)
	}

	if err := os.WriteFile(path, buf.Bytes(), 0644); err != nil {
		return fmt.Errorf("tunnel: could not write frpc config to %s: %w", path, err)
	}

	slog.Info("tunnel: config written",
		"path", path,
		"deviceID", s.cfg.DeviceID,
		"server", fmt.Sprintf("%s:%d", s.cfg.ServerIP, s.cfg.ServerPort),
	)
	return nil
}

// ---------------------------------------------------------------------------
// Template
// ---------------------------------------------------------------------------

type templateData struct {
	ServerIP   string
	Token      string
	DeviceID   string
	ServerPort int
	LocalPort  int
}

const frpConfigTmpl = `serverAddr = "{{.ServerIP}}"
serverPort = {{.ServerPort}}
auth.token = "{{.Token}}"

[[proxies]]
name = "web_{{.DeviceID}}"
type = "http"
localPort = {{.LocalPort}}
subdomain = "{{.DeviceID}}"
`