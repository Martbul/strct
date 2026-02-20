package config

import (
	"log/slog"
	"os"
	"path/filepath"
	"strings"

	"github.com/google/uuid"
)

func getOrGenerateDeviceID(isDev bool) string {
	var filePath string

	if isDev {
		filePath = "device-id.lock"
	} else {
		filePath = "/etc/strct/device-id.lock"
	}

	content, err := os.ReadFile(filePath)
	if err == nil {
		return strings.TrimSpace(string(content))
	}

	newID := "device-" + uuid.New().String()
	slog.Info("init: New Device ID generated:", newID)

	dir := filepath.Dir(filePath)
	if err := os.MkdirAll(dir, 0755); err != nil {
		slog.Warn("init: Could not create directory:", dir, "error", err)

		return newID
	}

	err = os.WriteFile(filePath, []byte(newID), 0644)
	if err != nil {
		slog.Warn("init:  Could not save device ID to disk at:", filePath, "error", err)

	} else {
		slog.Info("init: Device ID saved to:", filePath)

	}

	return newID
}
