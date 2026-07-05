//go:build windows

package main

import (
	"os"
	"syscall"
)

// detachSysProcAttr detaches the worker from the console.
// 0x00000008 = DETACHED_PROCESS, 0x00000200 = CREATE_NEW_PROCESS_GROUP.
func detachSysProcAttr() *syscall.SysProcAttr {
	return &syscall.SysProcAttr{CreationFlags: 0x00000008 | 0x00000200}
}

// flockExclusive on Windows opens the lock file but does NOT take a cross-process
// lock — Go's stdlib has no advisory file lock on Windows (a real one needs
// golang.org/x/sys/windows, a new dependency). The stale-socket reclaim race is
// left unserialized here; bind-first still prevents two live daemons.
// ponytail: acceptable — the race needs a crash-leftover socket AND two
// simultaneous cold-starts; upgrade to LockFileEx if it ever bites on Windows.
func flockExclusive(path string) (*os.File, error) {
	return os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
}
