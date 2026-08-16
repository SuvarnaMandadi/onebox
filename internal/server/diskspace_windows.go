//go:build windows

// diskspace_windows.go and diskspace_unix.go are the two halves of a
// portable free-disk-space check — Section 10's "Disk usage." Go's
// standard library has no cross-platform API for this (unlike
// runtime.MemStats, which metrics.go already uses honestly for
// process-level memory — see its own doc comment on why this codebase
// doesn't fake system-wide numbers it can't get portably). Both halves
// use only the standard library (syscall's Windows LazyDLL/NewProc, and
// syscall.Statfs on unix) rather than adding golang.org/x/sys as a new
// direct dependency for one function.
package server

import (
	"fmt"
	"syscall"
	"unsafe"
)

var (
	kernel32               = syscall.NewLazyDLL("kernel32.dll")
	procGetDiskFreeSpaceEx = kernel32.NewProc("GetDiskFreeSpaceExW")
)

// diskUsage reports free/total bytes on the volume containing path, via
// the real Win32 GetDiskFreeSpaceExW call — never estimated.
func diskUsage(path string) (freeBytes, totalBytes uint64, err error) {
	pathPtr, err := syscall.UTF16PtrFromString(path)
	if err != nil {
		return 0, 0, fmt.Errorf("encode path: %w", err)
	}
	var free, total, totalFree uint64
	ret, _, callErr := procGetDiskFreeSpaceEx.Call(
		uintptr(unsafe.Pointer(pathPtr)),
		uintptr(unsafe.Pointer(&free)),
		uintptr(unsafe.Pointer(&total)),
		uintptr(unsafe.Pointer(&totalFree)),
	)
	if ret == 0 {
		return 0, 0, fmt.Errorf("GetDiskFreeSpaceExW: %w", callErr)
	}
	return free, total, nil
}
