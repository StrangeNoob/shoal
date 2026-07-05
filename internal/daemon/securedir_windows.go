//go:build windows

package daemon

import "os"

// checkDirOwner is a no-op on Windows: NTFS ACLs don't map to a Unix owner uid,
// and the socket lives under the user's own profile dir anyway.
func checkDirOwner(string, os.FileInfo) error { return nil }
