//go:build !windows

package daemon

import (
	"fmt"
	"os"
	"syscall"
)

// checkDirOwner refuses a directory owned by another user. Skipped when the OS
// doesn't expose a Unix owner uid.
func checkDirOwner(dir string, fi os.FileInfo) error {
	st, ok := fi.Sys().(*syscall.Stat_t)
	if !ok {
		return nil
	}
	if int(st.Uid) != os.Getuid() {
		return fmt.Errorf("refusing socket dir %s: owned by uid %d, not %d", dir, st.Uid, os.Getuid())
	}
	return nil
}
