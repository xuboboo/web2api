//go:build windows

package platform

import "golang.org/x/sys/windows"

func dataDiskSpace(path string) (total, free uint64, err error) {
	var available, capacity, freeBytes uint64
	if err = windows.GetDiskFreeSpaceEx(windows.StringToUTF16Ptr(path), &available, &capacity, &freeBytes); err != nil {
		return
	}
	return capacity / 1024 / 1024, available / 1024 / 1024, nil
}
