package disk

import (
	"fmt"
	"time"
)

type MockDisk struct {
	VirtualPath string
	IsFormatted bool
}

func (d *MockDisk) GetStatus() (string, error) {
	if d.IsFormatted {
		return "Formatted (Virtual 1TB)", nil
	}
	return "Raw/Unformatted (Virtual 1TB)", nil
}

func (d *MockDisk) Format() error {
	fmt.Printf("[MOCK DISK] Simulating format of %s...\n", d.VirtualPath)
	fmt.Println("[MOCK DISK] Creating GPT Table...")
	time.Sleep(1 * time.Second)
	fmt.Println("[MOCK DISK] Creating Partition...")
	time.Sleep(1 * time.Second)
	fmt.Println("[MOCK DISK] Running mkfs.ext4...")
	time.Sleep(2 * time.Second)
	
	d.IsFormatted = true // Update state in memory
	fmt.Println("[MOCK DISK] Format Complete.")
	return nil
}

func (d *MockDisk) EnsureMounted(mountPoint string) error {
	fmt.Printf("[MOCK DISK] Ensuring %s is mounted to %s...\n", d.VirtualPath, mountPoint)
	
	time.Sleep(200 * time.Millisecond)
	fmt.Println("[MOCK DISK] Checking /proc/mounts... (Simulated: Not mounted)")

	fmt.Printf("[MOCK DISK] Creating directory %s...\n", mountPoint)

	time.Sleep(500 * time.Millisecond)
	fmt.Printf("[MOCK DISK] Mounted partition to %s successfully.\n", mountPoint)
	
	return nil
}