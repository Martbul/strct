package main

import (
	"context"
	"log"
	"os"
	"os/signal"
	"syscall"

	"github.com/strct-org/strct-agent/internal/agent"
	"github.com/strct-org/strct-agent/internal/config"
)

// func main() {
// 	cfg := config.Load()

// 	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
// 	defer stop()

// 	agent := agent.New(cfg)
// 	agent.Initialize()

// 	go agent.Start(ctx)

// 	waitForShutdown()
// }

// func waitForShutdown() {
// 	sigChan := make(chan os.Signal, 1)
// 	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)
// 	<-sigChan
// 	log.Println("Shutting down gracefully...")
// }
func main() {
    cfg := config.Load()

    agent, err := agent.InitializeAgent(cfg)
    if err != nil {
        log.Fatalf("Failed to initialize: %v", err)
    }

    ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
    defer stop()

    agent.Start(ctx)
    log.Println("Shutdown complete.")
}
