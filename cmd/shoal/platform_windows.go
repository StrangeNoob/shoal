//go:build windows

package main

import "syscall"

// pidAlive is best-effort on Windows (no cheap liveness probe): treat any
// positive pid as alive, so "stalled" detection is simply unavailable here.
func pidAlive(pid int) bool { return pid > 0 }

// detachSysProcAttr detaches the worker from the console.
// 0x00000008 = DETACHED_PROCESS, 0x00000200 = CREATE_NEW_PROCESS_GROUP.
func detachSysProcAttr() *syscall.SysProcAttr {
	return &syscall.SysProcAttr{CreationFlags: 0x00000008 | 0x00000200}
}
