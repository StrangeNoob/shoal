// Package opener launches the OS file manager to reveal a path.
package opener

import (
	"context"
	"os/exec"
	"runtime"
)

// Command returns the file-manager command + args to open path on goos.
func Command(goos, path string) (name string, args []string) {
	switch goos {
	case "darwin":
		return "open", []string{path}
	case "windows":
		return "explorer", []string{path}
	default:
		return "xdg-open", []string{path}
	}
}

// Open reveals path in the OS file manager, detached so a slow/failed manager
// never blocks the caller. Returns an error if the command can't be started
// (e.g. headless with no opener installed).
func Open(path string) error {
	name, args := Command(runtime.GOOS, path)
	cmd := exec.CommandContext(context.Background(), name, args...)
	if err := cmd.Start(); err != nil {
		return err
	}
	return cmd.Process.Release()
}
