package tunnel

import (
	"context"
	"fmt"
	"html/template"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"time"

	"github.com/strct-org/strct-agent/internal/config"
)

type Service struct {
	GlobalConfig *config.Config
}

type TemplateData struct {
	ServerIP   string
	Token      string
	DeviceID   string
	ServerPort int
	LocalPort  int
}

const frpConfigTmpl = `
serverAddr = "{{.ServerIP}}"
serverPort = {{.ServerPort}}
auth.token = "{{.Token}}"

[[proxies]]
name = "web_{{.DeviceID}}"
type = "http"
localPort = {{.LocalPort}}
subdomain = "{{.DeviceID}}"
`

func New(cfg *config.Config) *Service {
	return &Service{
		GlobalConfig: cfg,
	}
}

// ! implement canceling loginc with ctx context.Context
func (s *Service) Start(ctx context.Context) error {
	// 1. GET PROJECT ROOT (Current Working Directory)
	projectRoot, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("failed to determine project root: %v", err)
	}

	// 2. DEFINE PATHS
	// Binary is in Root (strct/frpc)
	frpcBinaryPath := filepath.Join(projectRoot, "frpc")
	// Config goes into DataDir to keep root clean (strct/data/frpc.toml)
	frpcConfigPath := filepath.Join(s.GlobalConfig.DataDir, "frpc.toml")

	// 3. CHECK IF BINARY EXISTS
	if _, err := os.Stat(frpcBinaryPath); os.IsNotExist(err) {
		log.Printf("===============================================================")
		log.Printf("[TUNNEL] CRITICAL ERROR: 'frpc' binary missing!")
		log.Printf("[TUNNEL] We looked here: %s", frpcBinaryPath)
		log.Printf("[TUNNEL] Please run: wget https://github.com/fatedier/frp/releases/download/v0.54.0/frp_0.54.0_linux_amd64.tar.gz")
		log.Printf("===============================================================")
		// Return nil so we don't crash the whole agent, just this feature fails
		return fmt.Errorf("binary not found at %s", frpcBinaryPath)
	}

	// 4. PREPARE CONFIG DATA
	data := TemplateData{
		ServerIP:   s.GlobalConfig.VPSIP,
		ServerPort: s.GlobalConfig.VPSPort,
		Token:      s.GlobalConfig.AuthToken,
		DeviceID:   s.GlobalConfig.DeviceID,
		LocalPort:  8080,
	}

	log.Printf("[TUNNEL] Configuring for Device: %s -> %s:%d", data.DeviceID, data.ServerIP, data.ServerPort)

	// 5. WRITE CONFIG FILE
	// Ensure data directory exists first
	if err := os.MkdirAll(s.GlobalConfig.DataDir, 0755); err != nil {
		return fmt.Errorf("failed to create data dir: %v", err)
	}

	file, err := os.Create(frpcConfigPath)
	if err != nil {
		return fmt.Errorf("failed to create config file: %v", err)
	}

	// Write template
	func() {
		defer file.Close()
		tmpl, err := template.New("frpc").Parse(frpConfigTmpl)
		if err != nil {
			log.Printf("[TUNNEL] Template parsing error: %v", err)
			return
		}
		if err := tmpl.Execute(file, data); err != nil {
			log.Printf("[TUNNEL] Template execution error: %v", err)
		}
	}()

	// 6. ENSURE PERMISSIONS (chmod +x)
	// This fixes "permission denied" errors automatically
	if err := os.Chmod(frpcBinaryPath, 0755); err != nil {
		log.Printf("[TUNNEL] Warning: Could not chmod binary: %v", err)
	}

	// 7. RUN LOOP
	// go func() {
	// 	for {
	// 		log.Println("[TUNNEL] Starting FRP Client...")

	// 		// Command: ./frpc -c ./data/frpc.toml
	// 		cmd := exec.Command(frpcBinaryPath, "-c", frpcConfigPath)
	// 		cmd.Stdout = os.Stdout
	// 		cmd.Stderr = os.Stderr

	// 		err := cmd.Start()
	// 		if err != nil {
	// 			log.Printf("[TUNNEL] Failed to start binary: %v", err)
	// 			time.Sleep(10 * time.Second)
	// 			continue
	// 		}

	// 		err = cmd.Wait()
	// 		log.Printf("[TUNNEL] Process exited: %v. Restarting in 5 seconds...", err)
	// 		time.Sleep(5 * time.Second)
	// 	}
	// }()

	go func() {
		for {
			select {
			case <-ctx.Done():
				log.Println("[TUNNEL] Context cancelled, stopping frpc")
				return
			default:
			}
			cmd := exec.CommandContext(ctx, frpcBinaryPath, "-c", frpcConfigPath)
			cmd.Stdout = os.Stdout
			cmd.Stderr = os.Stderr
			if err := cmd.Run(); err != nil && ctx.Err() == nil {
				log.Printf("[TUNNEL] frpc exited: %v. Restarting in 5s...", err)
				select {
				case <-ctx.Done():
					return
				case <-time.After(5 * time.Second):
				}
			}
		}
	}()

	return nil
}
