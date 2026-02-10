package tunnel

import (
	"fmt"
	"html/template"
	"log"
	"os"
	"os/exec"
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

func (s *Service) Start() error {
	data := TemplateData{
		ServerIP:   s.GlobalConfig.VPSIP,
		ServerPort: s.GlobalConfig.VPSPort,
		Token:      s.GlobalConfig.AuthToken,
		DeviceID:   s.GlobalConfig.DeviceID,
		LocalPort:  8080, 
	}

	log.Printf("[TUNNEL] Configuring for Device: %s -> %s:%d", data.DeviceID, data.ServerIP, data.ServerPort)

	file, err := os.Create("frpc.toml")
	if err != nil {
		return fmt.Errorf("failed to create config file: %v", err)
	}
	defer file.Close()

	tmpl, err := template.New("frpc").Parse(frpConfigTmpl)
	if err != nil {
		return err
	}

	if err := tmpl.Execute(file, data); err != nil {
		return fmt.Errorf("failed to write config: %v", err)
	}

	for {
		log.Println("[TUNNEL] Starting FRP Client...")
		
		cmd := exec.Command("./frpc", "-c", "frpc.toml")
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr

		err := cmd.Start()
		if err != nil {
			log.Printf("[TUNNEL] Failed to start binary: %v. Is ./frpc inside the folder?", err)
			time.Sleep(10 * time.Second)
			continue
		}

		err = cmd.Wait()
		log.Printf("[TUNNEL] Process exited: %v. Restarting in 5 seconds...", err)
		time.Sleep(5 * time.Second)
	}
}



//! check future implementation
// package tunnel

// import (
// 	"fmt"
// 	"log"
// 	"os"
// 	"os/exec"
// 	"text/template" // Used text/template for config files (not html)
// 	"time"

// 	"github.com/strct-org/strct-agent/internal/config"
// )

// type Service struct {
// 	GlobalConfig *config.Config
// }

// // TemplateData holds values to inject into frpc.toml
// type TemplateData struct {
// 	// Server Params
// 	ServerIP   string
// 	ServerPort int
// 	Token      string
	
// 	// Device Identity
// 	DeviceID   string

// 	// Local Ports (On the Raspberry Pi)
// 	LocalWebPort  int // Your Cloud/UI (8080)
// 	LocalVPNPort  int // WireGuard (51820)
// 	LocalSSHPort  int // SSH (22)

// 	// Remote Ports (On the VPS)
// 	// These MUST be unique per device for TCP/UDP protocols!
// 	RemoteVPNPort int 
// 	RemoteSSHPort int
// }

// // -----------------------------------------------------------------------------
// // FRP CONFIGURATION TEMPLATE
// // -----------------------------------------------------------------------------
// const frpConfigTmpl = `
// # Common Server Settings
// serverAddr = "{{.ServerIP}}"
// serverPort = {{.ServerPort}}
// auth.token = "{{.Token}}"

// # -----------------------------------------------------------------------------
// # 1. WEB DASHBOARD (Cloud + AdBlocker UI)
// # -----------------------------------------------------------------------------
// # Accessed via: https://{{.DeviceID}}.strct.org
// # Caddy (VPS) -> FRP VHost Port -> Tunnel -> Pi:{{.LocalWebPort}}
// [[proxies]]
// name = "web_{{.DeviceID}}"
// type = "http"
// localPort = {{.LocalWebPort}}
// subdomain = "{{.DeviceID}}"

// # -----------------------------------------------------------------------------
// # 2. VPN (WireGuard)
// # -----------------------------------------------------------------------------
// # Accessed via: {{.ServerIP}}:{{.RemoteVPNPort}}
// # This tunnels UDP traffic for VPN connection.
// [[proxies]]
// name = "vpn_{{.DeviceID}}"
// type = "udp"
// localPort = {{.LocalVPNPort}}
// remotePort = {{.RemoteVPNPort}}

// # -----------------------------------------------------------------------------
// # 3. SSH ACCESS (Remote Maintenance)
// # -----------------------------------------------------------------------------
// # Accessed via: ssh pi@{{.ServerIP}} -p {{.RemoteSSHPort}}
// [[proxies]]
// name = "ssh_{{.DeviceID}}"
// type = "tcp"
// localPort = {{.LocalSSHPort}}
// remotePort = {{.RemoteSSHPort}}
// `

// func New(cfg *config.Config) *Service {
// 	return &Service{
// 		GlobalConfig: cfg,
// 	}
// }

// func (s *Service) Start() error {
// 	// 1. Prepare the Data
// 	// In a real production scenario, RemoteVPNPort and RemoteSSHPort
// 	// should come from s.GlobalConfig, assigned by your backend API 
// 	// when the device registers.
	
// 	// Example fallback if config is missing specific ports (YOU MUST FIX THIS FOR PROD)
// 	vpnPort := s.GlobalConfig.AssignedVPNPort
// 	if vpnPort == 0 {
// 		vpnPort = 6000 // Default/Fallback (Dangerous in prod: collisions will occur)
// 	}

// 	sshPort := s.GlobalConfig.AssignedSSHPort
// 	if sshPort == 0 {
// 		sshPort = 2200 // Default/Fallback
// 	}

// 	data := TemplateData{
// 		ServerIP:      s.GlobalConfig.VPSIP,
// 		ServerPort:    s.GlobalConfig.VPSPort,
// 		Token:         s.GlobalConfig.AuthToken,
// 		DeviceID:      s.GlobalConfig.DeviceID,
		
// 		// Internal Services
// 		LocalWebPort:  8080,  // Matches your 'cloud' package port
// 		LocalVPNPort:  51820, // Standard WireGuard port
// 		LocalSSHPort:  22,    // Standard SSH port

// 		// External Access
// 		RemoteVPNPort: vpnPort,
// 		RemoteSSHPort: sshPort,
// 	}

// 	log.Printf("[TUNNEL] Generating config for Device: %s", data.DeviceID)
// 	log.Printf("[TUNNEL] Proxy Routing: Web(Subdomain) | VPN(:%d) | SSH(:%d)", data.RemoteVPNPort, data.RemoteSSHPort)

// 	// 2. Write the Config File
// 	if err := s.writeConfig(data); err != nil {
// 		return err
// 	}

// 	// 3. Run the Binary Loop
// 	go s.runProcess()

// 	return nil
// }

// func (s *Service) writeConfig(data TemplateData) error {
// 	file, err := os.Create("frpc.toml")
// 	if err != nil {
// 		return fmt.Errorf("failed to create frpc.toml: %v", err)
// 	}
// 	defer file.Close()

// 	tmpl, err := template.New("frpc").Parse(frpConfigTmpl)
// 	if err != nil {
// 		return fmt.Errorf("failed to parse template: %v", err)
// 	}

// 	if err := tmpl.Execute(file, data); err != nil {
// 		return fmt.Errorf("failed to write config content: %v", err)
// 	}
	
// 	return nil
// }

// func (s *Service) runProcess() {
// 	for {
// 		log.Println("[TUNNEL] Starting FRP Client...")

// 		// Ensure ./frpc is executable
// 		os.Chmod("./frpc", 0755)

// 		cmd := exec.Command("./frpc", "-c", "frpc.toml")
// 		cmd.Stdout = os.Stdout
// 		cmd.Stderr = os.Stderr

// 		if err := cmd.Start(); err != nil {
// 			log.Printf("[TUNNEL] Failed to start binary: %v. Is ./frpc present?", err)
// 			time.Sleep(15 * time.Second)
// 			continue
// 		}

// 		// Wait for the process to exit (it shouldn't, unless network drops)
// 		err := cmd.Wait()
// 		log.Printf("[TUNNEL] Connection lost: %v. Restarting in 5s...", err)
// 		time.Sleep(5 * time.Second)
// 	}
// }