//go:build !windows

package main

import (
	"os"
	"syscall"
)

// detachSysProcAttr detaches a spawned worker into its own session so it
// survives the parent process and terminal closing.
func detachSysProcAttr() *syscall.SysProcAttr {
	return &syscall.SysProcAttr{Setsid: true}
}

// flockExclusive takes a blocking exclusive advisory lock on path (created if
// needed). Close the returned file to release it; the lock also releases if the
// process dies, so a crash can't leave it stuck.
func flockExclusive(path string) (*os.File, error) {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, err
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX); err != nil {
		f.Close()
		return nil, err
	}
	return f, nil
}
