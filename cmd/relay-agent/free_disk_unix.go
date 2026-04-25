//go:build !windows

package main

import "syscall"

func freeDiskGB(path string) (int64, error) {
	var s syscall.Statfs_t
	if err := syscall.Statfs(path, &s); err != nil {
		return 0, err
	}
	return int64(s.Bavail) * int64(s.Bsize) / (1024 * 1024 * 1024), nil
}
