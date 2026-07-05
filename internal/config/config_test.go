package config

import (
	"os"
	"path/filepath"
	"testing"
)

// TestSaveTightensToOwnerOnly checks that config.json is written owner-only
// (0600) and that Save tightens an already-existing, too-permissive config dir
// to 0700 — it shares that dir with history.json/queue.json.
func TestSaveTightensToOwnerOnly(t *testing.T) {
	isolate(t)
	c := Default()
	dir := filepath.Dir(c.Path)
	if err := os.MkdirAll(dir, 0o777); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(dir, 0o777); err != nil { // MkdirAll respects umask; force it loose
		t.Fatal(err)
	}
	if err := c.Save(); err != nil {
		t.Fatalf("Save: %v", err)
	}
	if fi, _ := os.Stat(c.Path); fi.Mode().Perm() != 0o600 {
		t.Errorf("config.json perms = %o, want 0600", fi.Mode().Perm())
	}
	if di, _ := os.Stat(dir); di.Mode().Perm() != 0o700 {
		t.Errorf("config dir perms = %o, want 0700 (Save must tighten an existing loose dir)", di.Mode().Perm())
	}
}

// isolate points HOME (and so the OS config/data dirs) at a temp directory, so
// Load/Save/Default never touch the real user config. It also sets
// XDG_CONFIG_HOME: os.UserConfigDir honors that over HOME on Linux, so a runner
// with it set (e.g. GitHub Actions) would otherwise escape the sandbox and the
// tests would read/write the real config file.
func isolate(t *testing.T) {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("HOME", dir)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(dir, ".config"))
}

func TestDefaultValues(t *testing.T) {
	isolate(t)
	c := Default()
	if c.Theme != "Twilight" || c.ColorMode != "auto" {
		t.Errorf("appearance defaults = %q/%q, want Twilight/auto", c.Theme, c.ColorMode)
	}
	if !c.Seed || c.SeedRatio != 2.0 || c.MaxPeers != 200 || c.ListenPort != 6881 {
		t.Errorf("engine defaults = seed:%v ratio:%v peers:%d port:%d", c.Seed, c.SeedRatio, c.MaxPeers, c.ListenPort)
	}
	if c.DataDir != defaultDataDir() {
		t.Errorf("DataDir = %q, want %q", c.DataDir, defaultDataDir())
	}
}

func TestSaveLoadRoundTrip(t *testing.T) {
	isolate(t)
	c := Default()
	c.Theme = "Tide"
	c.ColorMode = "256"
	c.DataDir = "/tmp/shoal-test"
	c.Seed = false
	c.SeedRatio = 3.5
	c.MaxPeers = 99
	c.ListenPort = 7000
	c.DownloadRateKB = 500
	c.UploadRateKB = 100
	if err := c.Save(); err != nil {
		t.Fatalf("Save: %v", err)
	}

	got := Load()
	if got.Theme != "Tide" || got.ColorMode != "256" || got.DataDir != "/tmp/shoal-test" ||
		got.Seed != false || got.SeedRatio != 3.5 || got.MaxPeers != 99 || got.ListenPort != 7000 ||
		got.DownloadRateKB != 500 || got.UploadRateKB != 100 {
		t.Errorf("round trip mismatch: %+v", got)
	}
}

func TestLoadFirstRunReturnsDefaults(t *testing.T) {
	isolate(t)
	got := Load() // no file exists yet
	if got.Theme != "Twilight" || got.MaxPeers != 200 {
		t.Errorf("first-run Load = %+v, want defaults", got)
	}
}

func TestLoadMissingKeysFallBackToDefaults(t *testing.T) {
	isolate(t)
	// Write a config with only one key set; everything else must default.
	if err := os.MkdirAll(filepath.Dir(defaultPath()), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(defaultPath(), []byte(`{"theme":"Tide"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	got := Load()
	if got.Theme != "Tide" {
		t.Errorf("Theme = %q, want Tide", got.Theme)
	}
	if got.ColorMode != "auto" || got.MaxPeers != 200 || got.ListenPort != 6881 || got.DataDir == "" {
		t.Errorf("absent keys did not fall back to defaults: %+v", got)
	}
}

func TestSaveEmptyPathIsNoOp(t *testing.T) {
	isolate(t)
	c := Default()
	c.Path = ""
	if err := c.Save(); err != nil {
		t.Errorf("Save with empty Path should be a no-op, got %v", err)
	}
}

func TestAutoUpdateDefaultsFalseAndRoundTrips(t *testing.T) {
	isolate(t)
	if Default().AutoUpdate {
		t.Fatal("AutoUpdate should default to false (opt-in)")
	}
	c := Default()
	c.AutoUpdate = true
	if err := c.Save(); err != nil {
		t.Fatalf("Save: %v", err)
	}
	if !Load().AutoUpdate {
		t.Fatal("AutoUpdate did not survive save/load")
	}
}

func TestDaemonIdleMinutesDefault(t *testing.T) {
	if got := Default().DaemonIdleMinutes; got != 10 {
		t.Fatalf("Default().DaemonIdleMinutes = %d, want 10", got)
	}
}
