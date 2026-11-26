//go:build !windows

package platform

import "syscall"

func dataDiskSpace(path string) (total, free uint64, err error) {
	var disk syscall.Statfs_t
	if err = syscall.Statfs(path, &disk); err != nil {
		return
	}
	return disk.Blocks * uint64(disk.Bsize) / 1024 / 1024, disk.Bavail * uint64(disk.Bsize) / 1024 / 1024, nil
}
