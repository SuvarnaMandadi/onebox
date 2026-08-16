//go:build !windows

// See diskspace_windows.go's doc comment for why this is split by
// build tag instead of pulling in golang.org/x/sys.
package server

import (
	"fmt"
	"syscall"
)

// diskUsage reports free/total bytes on the filesystem containing path,
// via the real POSIX statfs(2) syscall (stdlib's syscall.Statfs — works
// on Linux and macOS, onebox's two non-Windows cross-compile targets).
func diskUsage(path string) (freeBytes, totalBytes uint64, err error) {
	var stat syscall.Statfs_t
	if err := syscall.Statfs(path, &stat); err != nil {
		return 0, 0, fmt.Errorf("statfs: %w", err)
	}
	free := uint64(stat.Bavail) * uint64(stat.Bsize)
	total := uint64(stat.Blocks) * uint64(stat.Bsize)
	return free, total, nil
}
