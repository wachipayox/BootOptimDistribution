//go:build !windows

package storage

import "syscall"

// DiskUsage reports the bytes available to this unprivileged process and the
// total size of the filesystem holding path.
func DiskUsage(path string) (free, total uint64, err error) {
	var stat syscall.Statfs_t
	if err := syscall.Statfs(path, &stat); err != nil {
		return 0, 0, err
	}
	return stat.Bavail * uint64(stat.Bsize), stat.Blocks * uint64(stat.Bsize), nil
}
