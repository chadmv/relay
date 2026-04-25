//go:build windows

package main

import (
	"golang.org/x/sys/windows"
)

func freeDiskGB(path string) (int64, error) {
	p, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return 0, err
	}
	var freeBytes uint64
	if err := windows.GetDiskFreeSpaceEx(p, &freeBytes, nil, nil); err != nil {
		return 0, err
	}
	return int64(freeBytes / (1024 * 1024 * 1024)), nil
}
