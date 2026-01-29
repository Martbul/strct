package disk

import (
	"encoding/json"
	"errors"
	"log"
	"os/exec"
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
		log.Println("[DISK] Factory: Returning MOCK Disk Manager")
		return &MockDisk{
			VirtualPath: "VIRTUAL_NVME",
			IsFormatted: false,
		}
	}

	if runtime.GOOS == "linux" {
		path, err := detectDevicePath()
		if err != nil {
			log.Printf("[DISK] CRITICAL: Auto-detect failed (%v). Defaulting to /dev/sda", err)
			path = "/dev/sda"
		}

		log.Printf("[DISK] Factory: Returning REAL Disk Manager targeting %s", path)
		return &RealDisk{
			DevicePath: path,
		}
	}

	log.Println("[DISK] Factory: OS is not Linux, defaulting to MOCK")
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

	// We reuse the structs defined in real.go (lsblkOutput, blockDevice)
	// because we are in the same 'package disk'
	var data lsblkOutput
	if err := json.Unmarshal(output, &data); err != nil {
		return "", err
	}

	for _, dev := range data.Blockdevices {
		// 1. Filter out Loopback (snaps), ROM (cd), and RAM disks
		if dev.Type == "loop" || dev.Type == "rom" || dev.Name == "sr0" {
			continue
		}

		// 2. Filter out the SD Card / eMMC
		// Raspberry Pi/Orange Pi SD cards usually start with "mmcblk"
		if strings.HasPrefix(dev.Name, "mmcblk") {
			continue
		}

		// 3. Double Check: Skip if it's the system root drive
		// (In case you booted from USB, we don't want to format the OS drive)
		isSystem := false
		if dev.Mountpoint == "/" {
			isSystem = true
		}
		// Check partitions (children) for root mount
		for _, child := range dev.Children {
			if child.Mountpoint == "/" {
				isSystem = true
				break
			}
		}

		if isSystem {
			continue
		}

		// If we survived the filters, this is likely our target drive
		return "/dev/" + dev.Name, nil
	}

	var noExternalDriveError = errors.New("No suitable external drive found")

	return "", noExternalDriveError // generic error
}
