package disk

import (
	"encoding/json"
	"errors"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
)

type Manager interface {
	GetStatus() (string, error)
	Format() error
	EnsureMounted(mountPoint string) error
}

func New(devMode bool) Manager {
	if devMode {
		slog.Info("disk: Factory: Returning MOCK Disk Manager")
		return &MockDisk{
			VirtualPath: "VIRTUAL_NVME",
			IsFormatted: false,
		}
	}

	if runtime.GOOS == "linux" {
		path, err := detectDevicePath()
		if err != nil {
			slog.Error("disk: Auto-detect failed, defaulting to /dev/sda", "err", err)
			path = "/dev/sda"
		}

		slog.Info("disk: Factory: Returning REAL Disk Manager", "path", path)
		return &RealDisk{
			DevicePath: path,
		}
	}

	slog.Info("disk: Factory: OS is not Linux, defaulting to MOCK")
	return &MockDisk{
		VirtualPath: "VIRTUAL_NVME",
		IsFormatted: false,
	}
}

func detectDevicePath() (string, error) {
	cmd := exec.Command("lsblk", "-J", "-o", "NAME,TYPE,MOUNTPOINT")
	output, err := cmd.Output()
	if err != nil {
		return "", err
	}

	var data lsblkOutput
	if err := json.Unmarshal(output, &data); err != nil {
		return "", err
	}

	for _, dev := range data.Blockdevices {
		if dev.Type == "loop" || dev.Type == "rom" || dev.Name == "sr0" {
			continue
		}

		if strings.HasPrefix(dev.Name, "mmcblk") {
			continue
		}

		isSystem := false
		if dev.Mountpoint == "/" {
			isSystem = true
		}
		for _, child := range dev.Children {
			if child.Mountpoint == "/" {
				isSystem = true
				break
			}
		}

		if isSystem {
			continue
		}

		return "/dev/" + dev.Name, nil
	}

	var noExternalDriveError = errors.New("no suitable external drive found")

	return "", noExternalDriveError
}

func GetDirSize(path string) (uint64, error) {
	var size int64
	err := filepath.WalkDir(path, func(_ string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.IsDir() {
			info, err := d.Info()
			if err == nil {
				size += info.Size()
			}
		}
		return nil
	})
	return uint64(size), err
}
