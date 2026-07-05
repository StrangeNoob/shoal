package daemon

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestSecureSocketDirCreates0700(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "shoal-sock")
	if err := SecureSocketDir(dir); err != nil {
		t.Fatal(err)
	}
	fi, err := os.Stat(dir)
	if err != nil {
		t.Fatal(err)
	}
	if runtime.GOOS != "windows" && fi.Mode().Perm() != 0o700 {
		t.Fatalf("mode = %o, want 700", fi.Mode().Perm())
	}
}

func TestSecureSocketDirTightensLoosePerms(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("unix perms only")
	}
	dir := filepath.Join(t.TempDir(), "loose")
	if err := os.Mkdir(dir, 0o777); err != nil {
		t.Fatal(err)
	}
	if err := SecureSocketDir(dir); err != nil {
		t.Fatal(err)
	}
	fi, _ := os.Stat(dir)
	if fi.Mode().Perm()&0o077 != 0 {
		t.Fatalf("group/world bits not cleared: %o", fi.Mode().Perm())
	}
}

func TestSecureSocketDirRejectsSymlink(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlink perms differ on windows")
	}
	base := t.TempDir()
	target := filepath.Join(base, "real")
	if err := os.Mkdir(target, 0o700); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(base, "link")
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
	if err := SecureSocketDir(link); err == nil {
		t.Fatal("expected refusal of a symlinked socket dir")
	}
}

func TestSocketPathHonorsEnv(t *testing.T) {
	t.Setenv("SHOAL_DAEMON_SOCK", "/custom/x.sock")
	if got := SocketPath(); got != "/custom/x.sock" {
		t.Fatalf("SocketPath = %q, want the env override", got)
	}
}
