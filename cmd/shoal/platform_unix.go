//go:build !windows

package main

import "syscall"

// detachSysProcAttr detaches a spawned worker into its own session so it
// survives the parent process and terminal closing.
func detachSysProcAttr() *syscall.SysProcAttr {
	return &syscall.SysProcAttr{Setsid: true}
}
