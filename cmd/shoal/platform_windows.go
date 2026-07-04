//go:build windows

package main

import (
	"fmt"
	"os"
	"syscall"
)

// detachSysProcAttr detaches the worker from the console.
// 0x00000008 = DETACHED_PROCESS, 0x00000200 = CREATE_NEW_PROCESS_GROUP.
func detachSysProcAttr() *syscall.SysProcAttr {
	return &syscall.SysProcAttr{CreationFlags: 0x00000008 | 0x00000200}
}

// flockExclusive is unsupported on Windows; runDaemon guards GOOS=="windows"
// before any path that would reach it.
func flockExclusive(path string) (*os.File, error) {
	return nil, fmt.Errorf("file locking is not supported on windows")
}
