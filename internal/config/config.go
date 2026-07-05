// Package config loads and persists shoal's user settings (theme, colour mode,
// download location, and engine tunables). It is plain stdlib JSON stored under
// the OS user-config dir (e.g. ~/.config/shoal/config.json on Linux). First run
// returns built-in defaults; the file is created the first time a setting is
// saved from the Settings pane.
package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
)

// Config is the persisted user configuration. Path is where it was loaded from
// (not serialized); Save() writes back there.
type Config struct {
	Path string `json:"-"`

	// Appearance
	Theme     string `json:"theme"`      // "Twilight" | "Tide"
	ColorMode string `json:"color_mode"` // "auto" | "truecolor" | "256" | "off"

	// Downloads / engine
	DataDir    string  `json:"data_dir"`    // where files land
	Seed       bool    `json:"seed"`        // keep seeding when complete
	SeedRatio  float64 `json:"seed_ratio"`  // target share ratio (see HANDOFF — not yet enforced)
	MaxPeers   int     `json:"max_peers"`   // max connections per torrent
	ListenPort int     `json:"listen_port"` // BitTorrent listen port

	// Rate limits in KiB/s; 0 = unlimited. Applied when the daemon (re)starts.
	DownloadRateKB int `json:"download_rate_kb"`
	UploadRateKB   int `json:"upload_rate_kb"`

	// Daemon
	DaemonIdleMinutes int `json:"daemon_idle_minutes"` // minutes idle (no torrents, no client) before auto-shutdown; 0 disables

	// Search
	DisabledSources []string `json:"disabled_sources,omitempty"` // provider Name()s the user turned off

	// Updates
	AutoUpdate bool `json:"auto_update"` // apply the latest release automatically on launch
}

// Default returns the built-in configuration.
func Default() Config {
	return Config{
		Path:              defaultPath(),
		Theme:             "Twilight",
		ColorMode:         "auto",
		DataDir:           defaultDataDir(),
		Seed:              true,
		SeedRatio:         2.0,
		MaxPeers:          200,
		ListenPort:        6881,
		DaemonIdleMinutes: 10,
	}
}

// SourceEnabled reports whether a provider name is enabled (i.e. not present in
// DisabledSources). Matching is case-insensitive.
func (c Config) SourceEnabled(name string) bool {
	for _, d := range c.DisabledSources {
		if strings.EqualFold(d, name) {
			return false
		}
	}
	return true
}

// SetSourceEnabled turns a provider on or off, updating DisabledSources
// (case-insensitive, deduped, order-stable). Stores the given name verbatim
// when disabling.
func (c *Config) SetSourceEnabled(name string, enabled bool) {
	idx := -1
	for i, d := range c.DisabledSources {
		if strings.EqualFold(d, name) {
			idx = i
			break
		}
	}
	switch {
	case enabled && idx >= 0: // remove, preserving the order of the rest
		c.DisabledSources = append(c.DisabledSources[:idx], c.DisabledSources[idx+1:]...)
	case !enabled && idx < 0: // new disable → append
		c.DisabledSources = append(c.DisabledSources, name)
	}
	// enabled && idx<0, and !enabled && idx>=0, are both no-ops (order untouched)
}

// Load reads the config file, falling back to defaults for the whole file (first
// run) or for any individual missing key.
func Load() Config {
	cfg := Default()
	if cfg.Path == "" {
		return cfg
	}
	b, err := os.ReadFile(cfg.Path)
	if err != nil {
		return cfg // first run / unreadable: defaults
	}
	// Unmarshal over the defaults so absent keys keep their default values.
	_ = json.Unmarshal(b, &cfg)
	if cfg.DataDir == "" {
		cfg.DataDir = defaultDataDir()
	}
	if cfg.Theme == "" {
		cfg.Theme = "Twilight"
	}
	if cfg.ColorMode == "" {
		cfg.ColorMode = "auto"
	}
	cfg.Path = defaultPath()
	return cfg
}

// Save writes the config back to its Path (creating the directory). It is a
// no-op when Path is empty (e.g. in tests).
func (c Config) Save() error {
	if c.Path == "" {
		return nil
	}
	dir := filepath.Dir(c.Path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	// Keep the config dir owner-only — it also holds history.json / queue.json.
	// MkdirAll won't chmod an existing dir, so tighten it explicitly.
	if err := os.Chmod(dir, 0o700); err != nil {
		return err
	}
	b, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(c.Path, b, 0o600)
}

func defaultPath() string {
	if dir, err := os.UserConfigDir(); err == nil && dir != "" {
		return filepath.Join(dir, "shoal", "config.json")
	}
	return ""
}

func defaultDataDir() string {
	if home, err := os.UserHomeDir(); err == nil && home != "" {
		return filepath.Join(home, "Downloads", "shoal")
	}
	return filepath.Join(".", "downloads")
}
