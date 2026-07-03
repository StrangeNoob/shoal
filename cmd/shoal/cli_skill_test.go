package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The embedded copy under cmd/shoal/skill/ must stay byte-identical to the
// canonical skill Claude Code loads from .claude/skills/. If this fails, re-copy:
//
//	cp .claude/skills/shoal-download/SKILL.md cmd/shoal/skill/SKILL.md
func TestEmbeddedSkillMatchesRepoCopy(t *testing.T) {
	repo, err := os.ReadFile(filepath.Join("..", "..", ".claude", "skills", "shoal-download", "SKILL.md"))
	if err != nil {
		t.Fatalf("read repo skill: %v", err)
	}
	if string(repo) != skillContent {
		t.Fatal("embedded cmd/shoal/skill/SKILL.md is out of sync with .claude/skills/shoal-download/SKILL.md")
	}
}

func TestInstallSkill(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "shoal-download")
	var buf bytes.Buffer

	// fresh install writes the embedded content
	if code := installSkill(&buf, dir, false); code != 0 {
		t.Fatalf("fresh install exit = %d", code)
	}
	got, err := os.ReadFile(filepath.Join(dir, "SKILL.md"))
	if err != nil || string(got) != skillContent {
		t.Fatalf("skill not written correctly: err=%v", err)
	}

	// re-install without --force leaves it in place, exit 0, with a notice
	buf.Reset()
	if code := installSkill(&buf, dir, false); code != 0 {
		t.Fatalf("re-install exit = %d", code)
	}
	if !strings.Contains(strings.ToLower(buf.String()), "already") {
		t.Fatalf("expected an 'already installed' notice, got: %s", buf.String())
	}

	// --force overwrites a modified file back to the embedded content
	if err := os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte("stale"), 0o644); err != nil {
		t.Fatal(err)
	}
	buf.Reset()
	if code := installSkill(&buf, dir, true); code != 0 {
		t.Fatalf("force install exit = %d", code)
	}
	got, _ = os.ReadFile(filepath.Join(dir, "SKILL.md"))
	if string(got) != skillContent {
		t.Fatal("--force did not restore the embedded skill content")
	}
}
