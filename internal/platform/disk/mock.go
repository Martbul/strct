package disk

import (
	"log/slog"
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
	slog.Debug("mock disk: formatting", "path", d.VirtualPath)
	d.IsFormatted = true
	slog.Debug("mock disk: format complete")
	return nil
}

func (d *MockDisk) EnsureMounted(mountPoint string) error {
	slog.Debug("mock disk: Ensuring mounted", "path", d.VirtualPath, "mountPoint", mountPoint)
	time.Sleep(200 * time.Millisecond)

	slog.Debug("mock disk: created directory", "mountPoint", mountPoint)
	time.Sleep(500 * time.Millisecond)
	slog.Debug("mock disk: mount partition succesfuly", "mountPoint", mountPoint)
	return nil
}
