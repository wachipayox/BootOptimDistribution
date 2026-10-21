//go:build windows

package storage

import (
	"syscall"
	"unsafe"
)

var getDiskFreeSpaceEx = syscall.NewLazyDLL("kernel32.dll").NewProc("GetDiskFreeSpaceExW")

// DiskUsage reports the bytes available to this process and the total size of
// the volume holding path. The service runs on Linux; this keeps tooling and
// tests buildable on Windows workstations.
func DiskUsage(path string) (free, total uint64, err error) {
	name, err := syscall.UTF16PtrFromString(path)
	if err != nil {
		return 0, 0, err
	}
	var available, size, totalFree uint64
	ok, _, callErr := getDiskFreeSpaceEx.Call(uintptr(unsafe.Pointer(name)),
		uintptr(unsafe.Pointer(&available)), uintptr(unsafe.Pointer(&size)), uintptr(unsafe.Pointer(&totalFree)))
	if ok == 0 {
		return 0, 0, callErr
	}
	return available, size, nil
}
