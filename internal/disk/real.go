package disk

import (
	"encoding/json"
	"fmt"
	"os/exec"
)

type RealDisk struct {
	DevicePath string
}

type lsblkOutput struct {
	Blockdevices []blockDevice `json:"blockdevices"`
}

type blockDevice struct {
	Name     string        `json:"name"`
	Size     string        `json:"size"`
	Type     string        `json:"type"`
	Children []blockDevice `json:"children,omitempty"`
}

func (d *RealDisk) GetStatus() (string, error) {
	cmd := exec.Command("lsblk", "-J", "-o", "NAME,SIZE,TYPE,MOUNTPOINT", d.DevicePath)
	output, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("disk not found or error reading: %v", err)
	}

	var data lsblkOutput
	if err := json.Unmarshal(output, &data); err != nil {
		return "", fmt.Errorf("failed to parse lsblk output: %v", err)
	}

	if len(data.Blockdevices) == 0 {
		return "Not Found", nil
	}

	dev := data.Blockdevices[0]
	status := fmt.Sprintf("Raw/Unformatted (%s)", dev.Size)

	if len(dev.Children) > 0 {
		status = fmt.Sprintf("Formatted (%s)", dev.Size)
	}

	return status, nil
}

func (d *RealDisk) Format() error {
	fmt.Printf("[DISK] REAL FORMATTING INITIATED ON %s\n", d.DevicePath)

	// 2. Create Partition
	if err := exec.Command("parted", d.DevicePath, "--script", "mkpart", "primary", "ext4", "0%", "100%").Run(); err != nil {
		return err
	}

	partPath := d.DevicePath + "1"
	if d.DevicePath == "/dev/nvme0n1" {
		partPath = d.DevicePath + "p1"
	}

	// Refresh kernel partition table
	exec.Command("partprobe", d.DevicePath).Run()

	if err := exec.Command("mkfs.ext4", "-F", partPath).Run(); err != nil {
		return err
	}

	return nil
}

func (d *RealDisk) EnsureMounted(mountPoint string) error {
	//check if mounted
	cmd := exec.Command("grep", mountPoint, "/proc/mounts")
	if err := cmd.Run(); err == nil {
		return nil
	}

	//create dir
	exec.Command("mkdir", "-p", mountPoint).Run()

	partPath := d.DevicePath + "1"
	if d.DevicePath == "/dev/nvme0n1" {
		partPath = d.DevicePath + "p1"
	}

	fmt.Printf("[DISK] Mounting %s to %s\n", partPath, mountPoint)
	if err := exec.Command("mount", partPath, mountPoint).Run(); err != nil {
		return fmt.Errorf("failed to mount: %v", err)
	}
	return nil

}
