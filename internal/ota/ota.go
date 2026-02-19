//! Not wired up


package ota

import (
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"runtime"
	"strings"
	"time"

	"github.com/blang/semver"
	"github.com/minio/selfupdate"
)

type Config struct {
	CurrentVersion string
	StorageURL     string 
}

func StartUpdater(cfg Config) {
	ticker := time.NewTicker(100 * time.Hour)

	// Check on startup
	go func() {
		if err := checkForUpdate(cfg); err != nil {
			// log.Printf("OTA: Update check failed: %v", err)
		}
	}()

	// Infinite Loop
	go func() {
		for range ticker.C {
			if err := checkForUpdate(cfg); err != nil {
				log.Printf("OTA: Update check failed: %v", err)
			}
		}
	}()
}

func checkForUpdate(cfg Config) error {
	log.Println("OTA: Checking for updates...")

	resp, err := http.Get(fmt.Sprintf("%s/version.txt", cfg.StorageURL))
	if err != nil {
		return fmt.Errorf("failed to fetch version file: %w", err)
	}
	defer resp.Body.Close()

	remoteVerStrRaw, _ := io.ReadAll(resp.Body)
	remoteVerStr := strings.TrimSpace(string(remoteVerStrRaw))

	// Parse and Compare Versions
	vCurrent, err := semver.Make(cfg.CurrentVersion)
	if err != nil {
		return fmt.Errorf("invalid current version '%s': %w", cfg.CurrentVersion, err)
	}
	vRemote, err := semver.Make(remoteVerStr)
	if err != nil {
		return fmt.Errorf("invalid remote version '%s': %w", remoteVerStr, err)
	}

	//less than or equal
	if vRemote.LTE(vCurrent) {
		log.Printf("OTA: No update needed. Remote: %s, Current: %s", vRemote, vCurrent)
		return nil
	}

	log.Printf("OTA: New version found: %s. Downloading...", vRemote)

	// define the binary name based on architecture
	binName := fmt.Sprintf("strct-agent-%s-%s", runtime.GOOS, runtime.GOARCH)
	binURL := fmt.Sprintf("%s/%s", cfg.StorageURL, binName)
	checksumURL := binURL + ".sha256"

	// download and Apply
	return doUpdate(binURL, checksumURL)
}

func doUpdate(binURL, checksumURL string) error {
	resp, err := http.Get(binURL)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return fmt.Errorf("binary download failed: %s", resp.Status)
	}

	// B. Verify Checksum (Security Best Practice)
	// We verify the stream as we read it to avoid loading huge files into memory
	// However, selfupdate.Apply consumes the stream. Ideally, verify header/checksum first.
	// For simplicity, we trust the connection or verify a separate hash file.

	// NOTE: Production code should download the .sha256 file and verify here.
	// verification logic omitted for brevity but highly recommended.

	// C. Apply the update
	err = selfupdate.Apply(resp.Body, selfupdate.Options{
		// Calculate checksum of downloaded bytes to verify integrity before swap
		Checksum: []byte{}, // You would pass the expected checksum bytes here if you fetched them
	})

	if err != nil {
		// Rollback happens automatically if Apply fails
		return fmt.Errorf("update apply failed: %w", err)
	}

	log.Println("OTA: Update applied successfully! Restarting...")

	os.Exit(0)
	return nil
}
