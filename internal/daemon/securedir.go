package daemon

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
)

// SecureSocketDir makes dir a private (0700), current-user-owned directory
// suitable for a unix socket, creating it (and parents) if absent. For a
// pre-existing dir it refuses a symlink, a non-directory, or one owned by
// another user, then tightens the mode to 0700 — closing the "attacker
// pre-creates a world-writable path in /tmp and squats the socket" hole.
func SecureSocketDir(dir string) error {
	fi, err := os.Lstat(dir)
	if errors.Is(err, fs.ErrNotExist) {
		if err := os.MkdirAll(filepath.Dir(dir), 0o700); err != nil {
			return err
		}
		return os.Mkdir(dir, 0o700)
	}
	if err != nil {
		return err
	}
	if fi.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("refusing socket dir %s: it is a symlink", dir)
	}
	if !fi.IsDir() {
		return fmt.Errorf("refusing socket path %s: not a directory", dir)
	}
	if err := checkDirOwner(dir, fi); err != nil {
		return err
	}
	return os.Chmod(dir, 0o700) // tighten away any group/world access
}
