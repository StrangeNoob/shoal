package main

import (
	_ "embed"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
)

// skillContent is the shoal-download Claude Code skill, embedded so
// `shoal skill install` works offline and always matches this binary. A test
// keeps it byte-identical to .claude/skills/shoal-download/SKILL.md.
//
//go:embed skill/SKILL.md
var skillContent string

// runSkill handles the `shoal skill …` subcommand group.
func runSkill(args []string, out io.Writer) int {
	if len(args) == 0 || args[0] != "install" {
		fmt.Fprintln(os.Stderr, "usage: shoal skill install [--force]")
		return 2
	}
	fs := flag.NewFlagSet("skill install", flag.ContinueOnError)
	force := fs.Bool("force", false, "overwrite an existing skill file")
	if err := fs.Parse(args[1:]); err != nil {
		return 2
	}
	return installSkill(out, skillInstallDir(), *force)
}

// skillInstallDir is the personal Claude Code skills location for this skill:
// ~/.claude/skills/shoal-download.
func skillInstallDir() string {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return filepath.Join(".claude", "skills", "shoal-download")
	}
	return filepath.Join(home, ".claude", "skills", "shoal-download")
}

// installSkill writes the embedded SKILL.md into dir. An existing file is left
// untouched unless force is set.
func installSkill(out io.Writer, dir string, force bool) int {
	target := filepath.Join(dir, "SKILL.md")
	if _, err := os.Stat(target); err == nil && !force {
		fmt.Fprintf(out, "shoal-download skill already installed at %s (use --force to overwrite)\n", target)
		return 0
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		fmt.Fprintln(os.Stderr, "shoal: skill install:", err)
		return 1
	}
	if err := os.WriteFile(target, []byte(skillContent), 0o644); err != nil {
		fmt.Fprintln(os.Stderr, "shoal: skill install:", err)
		return 1
	}
	fmt.Fprintf(out, "installed → %s\nRestart Claude Code once to pick it up, then ask it to find and download something.\n", target)
	return 0
}
