package disk

import (
	"log"
	"os"
	"runtime"
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
		path := detectDevicePath()

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




// detectDevicePath checks priority: NVMe -> sda -> sdb -> sdc -> sdd
func detectDevicePath() string {
	if _, err := os.Stat("/dev/nvme0n1"); err == nil {
		return "/dev/nvme0n1"
	}

	possibleDrives := []string{"/dev/sda", "/dev/sdb", "/dev/sdc", "/dev/sdd"}

	for _, path := range possibleDrives {
		if _, err := os.Stat(path); err == nil {
			return path
		}
	}

	return "/dev/nvme0n1"
}