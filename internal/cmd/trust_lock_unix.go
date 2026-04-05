//go:build !windows

package cmd

import "syscall"

// flockExclusive acquires an exclusive advisory lock on the given file descriptor.
// Blocks until the lock is available.
func flockExclusive(fd uintptr) error {
	return syscall.Flock(int(fd), syscall.LOCK_EX)
}

// flockUnlock releases any advisory lock held on the file descriptor.
func flockUnlock(fd uintptr) error {
	return syscall.Flock(int(fd), syscall.LOCK_UN)
}
