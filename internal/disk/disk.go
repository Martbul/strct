package disk

import (
	"log"
	"runtime"
)

type Manager interface {
	GetStatus() (string, error)
	Format() error
	EnsureMounted(mountPoint string) error
}

// New Factory.
func New(devMode bool) Manager {
	if devMode {
		log.Println("[DISK] Factory: Returning MOCK Disk Manager")
		return &MockDisk{
			VirtualPath: "VIRTUAL_NVME",
			IsFormatted: false,
		}
	}

	if runtime.GOOS == "linux" {
		path := "/dev/sdb" // Default (VM)
		if runtime.GOARCH == "arm64" {
			path = "/dev/nvme0n1" // Orange Pi
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
