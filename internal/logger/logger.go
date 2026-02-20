package logger

import (
	"log/slog"
	"os"
)

func Init(isDev bool) {
	var handler slog.Handler

	opts := &slog.HandlerOptions{
		Level:     slog.LevelDebug,
		AddSource: true,
	}

	if isDev {
		handler = slog.NewTextHandler(os.Stdout, opts)
	} else {
		handler = slog.NewJSONHandler(os.Stdout, opts)
	}

	slog.SetDefault(slog.New(handler))
}

//! slog example

// This file is a REFERENCE / EXAMPLE — not a real package file.
// It shows how slog should be used throughout the codebase after logger.Init()
// is called in main. No instance is passed around; all code imports log/slog directly.
//
// RULE: never import "log" after logger.Init() is called. Use "log/slog" everywhere.

// package examples

// import (
// 	"context"
// 	"log/slog"
// 	"time"
// )

// // ---------------------------------------------------------------------------
// // 1. BASIC LEVELS
// // Use the level that matches the severity:
// //
// //   Debug  — verbose, only useful when debugging. Off by default in prod.
// //   Info   — normal operational events (service started, config loaded).
// //   Warn   — something unexpected but recoverable happened.
// //   Error  — something failed; action may be required.
// // ---------------------------------------------------------------------------

// func basicLevels() {
// 	// Structured: always use key=value pairs, not format strings
// 	slog.Debug("tunnel: frpc restarting", "attempt", 3, "delay", "5s")
// 	slog.Info("api: listening", "port", 8080, "dev", true)
// 	slog.Warn("config: no .env file found, using system env vars")
// 	slog.Error("monitor: ping failed", "target", "8.8.8.8", "err", "timeout")
// }

// // ---------------------------------------------------------------------------
// // 2. REPLACING OLD log.Printf PATTERNS
// // ---------------------------------------------------------------------------

// func oldVsNew() {
// 	// OLD — format string, no structure, hard to query:
// 	// log.Printf("[MONITOR] Upload failed: %v", err)

// 	// NEW — structured, queryable by field name in any log aggregator:
// 	// slog.Error("monitor: upload failed", "err", err)

// 	// OLD:
// 	// log.Printf("[TUNNEL] Configuring for Device: %s -> %s:%d", deviceID, serverIP, serverPort)

// 	// NEW:
// 	// slog.Info("tunnel: configuring",
// 	//     "deviceID", deviceID,
// 	//     "serverIP", serverIP,
// 	//     "serverPort", serverPort,
// 	// )
// }

// // ---------------------------------------------------------------------------
// // 3. CONTEXT-AWARE LOGGING (slog.With)
// // Create a child logger with fields that apply to all logs from a component.
// // This is the idiomatic way to add component-level context without repeating keys.
// // ---------------------------------------------------------------------------

// type NetworkMonitor struct {
// 	log    *slog.Logger // component-scoped logger
// 	Target string
// }

// func NewNetworkMonitor(target string) *NetworkMonitor {
// 	return &NetworkMonitor{
// 		Target: target,
// 		// slog.With creates a child logger that always includes these fields.
// 		// Every call on m.log will have "service" and "target" attached.
// 		log: slog.With("service", "monitor", "target", target),
// 	}
// }

// func (m *NetworkMonitor) runPing() {
// 	// No need to repeat "service" or "target" — they're baked into m.log
// 	m.log.Debug("ping: starting")

// 	// Simulate a result
// 	latency := 12.4
// 	m.log.Info("ping: complete", "latency_ms", latency, "loss_pct", 0.0)
// }

// func (m *NetworkMonitor) Start(ctx context.Context) error {
// 	m.log.Info("starting")
// 	ticker := time.NewTicker(120 * time.Second)
// 	defer ticker.Stop()

// 	for {
// 		select {
// 		case <-ctx.Done():
// 			m.log.Info("stopping", "reason", ctx.Err())
// 			return nil
// 		case <-ticker.C:
// 			m.runPing()
// 		}
// 	}
// }

// // ---------------------------------------------------------------------------
// // 4. ERROR LOGGING — always log the error as "err", not "error" or "msg"
// // "err" is the conventional key in the Go ecosystem.
// // ---------------------------------------------------------------------------

// func errorLogging(err error) {
// 	// CORRECT:
// 	slog.Error("vpn: provisioning failed", "err", err)

// 	// WRONG — don't use format strings with slog:
// 	// slog.Error(fmt.Sprintf("vpn: provisioning failed: %v", err))

// 	// For errors with extra context:
// 	slog.Error("disk: mount failed",
// 		"device", "/dev/nvme0n1",
// 		"mountPoint", "/mnt/strct_data",
// 		"err", err,
// 	)
// }

// // ---------------------------------------------------------------------------
// // 5. HTTP HANDLER LOGGING — log at the right level
// // ---------------------------------------------------------------------------

// func handlerLogging() {
// 	// Don't log every request — the framework/middleware handles that.
// 	// Only log when something notable happens inside the handler:

// 	// User did something wrong (their fault): Warn
// 	slog.Warn("cloud: path traversal attempt", "path", "../../../etc/passwd")

// 	// We failed to do something (our fault): Error
// 	slog.Error("cloud: failed to write file", "path", "/mnt/data/foo.txt", "err", "disk full")

// 	// Background job completed: Info
// 	slog.Info("adblocker: blocklist updated", "domains", 142000)
// }

// // ---------------------------------------------------------------------------
// // 6. STARTUP LOGGING PATTERN
// // Log key config values at startup so you can verify what the agent loaded.
// // Never log secrets (tokens, passwords).
// // ---------------------------------------------------------------------------

// func startupLogging(deviceID, dataDir string, port int, isDev bool) {
// 	slog.Info("agent: starting",
// 		"deviceID", deviceID,
// 		"dataDir", dataDir,
// 		"port", port,
// 		"dev", isDev,
// 	)
// }
