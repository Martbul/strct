package tunnel

import (
	"fmt"
	"html/template"
	"os"
	"os/exec"
)

type TunnelConfig struct {
	ServerIP   string
	Token      string
	DeviceID   string
	ServerPort int
	LocalPort  int // running the user's file browser
	BaseDomain string
}

const frpConfigTmpl = `
serverAddr = "{{.ServerIP}}"
serverPort = {{.ServerPort}}
auth.token = "{{.Token}}"

[[proxies]]
name = "web_{{.DeviceID}}"
type = "http"
localPort = {{.LocalPort}}
customDomains = ["{{.DeviceID}}.{{.BaseDomain}}"]
`

func StartTunnel(cfg TunnelConfig) error {
	fmt.Printf("[TUNNEL] Configuring for Device: %s -> %s:%d\n", cfg.DeviceID, cfg.ServerIP, cfg.ServerPort)

	file, err := os.Create("frpc.toml")
	if err != nil {
		return fmt.Errorf("failed to create config file: %v", err) //maybe default ot some standart config or panic
	}

	defer file.Close()

	tmpl, err := template.New("frpc").Parse(frpConfigTmpl)
	if err != nil {
		return err
	}

	if err := tmpl.Execute(file, cfg); err != nil {
		return fmt.Errorf("failed to write config: %v", err)
	}

	// start the binary
	cmd := exec.Command("./frpc", "-c", "frpc.toml")

	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	fmt.Println("[TUNNEL] Starting FRP Client...")

	if err := cmd.Start(); err != nil {
		return fmt.Errorf("failed to start frpc: %v", err)

	}

	//! implement restart if crash

	return cmd.Wait()

}
