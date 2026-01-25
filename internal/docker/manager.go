package docker

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"time"
)

// runing container check
func EnsureFileBrowser(dataDir string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	containerName := "filebrowser_app"

	//check if running
	checkCmd := exec.CommandContext(ctx, "docker", "ps", "-q", "-f", fmt.Sprintf("name=%s", containerName))
	output, _ := checkCmd.Output()

	if len(output) > 0 {
		fmt.Println("[DOCKER] FileBrowser is already running")
		return nil
	}

	fmt.Println("[DOCKER] Starting FileBrowser container...")

	// 2. Run Docker
	// docker run -d --name filebrowser_app -v /mnt/data:/srv -p 80:80 filebrowser/filebrowser
	cwd, _ := os.Getwd()
	dbPath := filepath.Join(cwd, "filebrowser.db")

	// Fix dataDir if it is relative (for VM testing)
	if dataDir == "./data" {
		dataDir = filepath.Join(cwd, "data")
	}

	runCmd := exec.Command("docker", "run", "-d",
		"--restart=always",
		"--name", containerName,
		"-v", fmt.Sprintf("%s:/srv", dataDir),
		"-v", fmt.Sprintf("%s:/database/filebrowser.db", dbPath), // Absolute path!
		"-p", "80:80",
		"filebrowser/filebrowser",
	)

	if err := runCmd.Run(); err != nil {
		// If it failed, maybe it exists but is stopped? Try 'docker start'
		exec.Command("docker", "rm", containerName).Run() // Cleanup old one
		return fmt.Errorf("failed to start docker container: %v", err)
	}

	fmt.Println("[DOCKER] FileBrowser started successfully on Port 80.")
	return nil

}
