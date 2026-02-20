//go:build windows

package disk

import (
	"fmt"
	"syscall"
	"unsafe"
)

func GetFreeDiskSpace(path string) (uint64, error) {
	h := syscall.MustLoadDLL("kernel32.dll")
	c := h.MustFindProc("GetDiskFreeSpaceExW")

	var freeBytesAvailable, totalNumberOfBytes, totalNumberOfFreeBytes int64

	_, _, err := c.Call(
		uintptr(unsafe.Pointer(syscall.StringToUTF16Ptr(path))),
		uintptr(unsafe.Pointer(&freeBytesAvailable)),
		uintptr(unsafe.Pointer(&totalNumberOfBytes)),
		uintptr(unsafe.Pointer(&totalNumberOfFreeBytes)),
	)

	// In Go syscalls, a non-zero error is always returned even on success.
	// We check if the return value suggests failure (though Call returns uintptr).
	// For GetDiskFreeSpaceEx, usually checking if freeBytesAvailable > 0 is a basic sanity check,
	// but strictly speaking 'err' from c.Call contains the LastError if the call failed.
	if freeBytesAvailable == 0 && err != nil {
		//! Implement proper using golang.org/x/sys/windows
		// This is a rough check; for production code consider golang.org/x/sys/windows
	}

	// return uint64(freeBytesAvailable), nil
	return 0, fmt.Errorf("not implemented on windows: %w", err)
}
