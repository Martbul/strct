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
	slog.Info("config: generated new device ID", "id", newID)

	dir := filepath.Dir(filePath)
	if err := os.MkdirAll(dir, 0755); err != nil {
		slog.Warn("config: could not create device ID directory", "dir", dir, "err", err)

		return newID
	}

	err = os.WriteFile(filePath, []byte(newID), 0644)
	if err != nil {
		slog.Warn("config: could not persist device ID", "path", filePath, "err", err)

	} else {
		slog.Info("config: device ID persisted", "path", filePath)

	}

	return newID
}
