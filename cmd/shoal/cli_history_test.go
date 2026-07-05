package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/StrangeNoob/shoal/internal/config"
	"github.com/StrangeNoob/shoal/internal/history"
)

// isolateHistory points HOME/XDG at a temp dir so history.Load() uses a scratch file.
func isolateHistory(t *testing.T) history.Store {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("HOME", dir)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(dir, ".config"))
	return history.Load()
}

func TestHistoryListAndRm(t *testing.T) {
	s := isolateHistory(t)
	s.Append(history.Entry{InfoHash: "aaaa1111", Name: "Movie A", Size: 100})
	s.Append(history.Entry{InfoHash: "bbbb2222", Name: "Movie B", Size: 200})

	var buf bytes.Buffer
	if code := runHistory(nil, &buf); code != 0 {
		t.Fatalf("history list exit = %d", code)
	}
	if !strings.Contains(buf.String(), "Movie A") || !strings.Contains(buf.String(), "Movie B") {
		t.Fatalf("history list missing entries:\n%s", buf.String())
	}

	buf.Reset()
	if code := runHistory([]string{"rm", "aaaa"}, &buf); code != 0 {
		t.Fatalf("history rm exit = %d", code)
	}
	if got := history.Load().Entries; len(got) != 1 || got[0].InfoHash != "bbbb2222" {
		t.Fatalf("after rm, entries = %+v, want [bbbb2222]", got)
	}
}

func TestHistoryClear(t *testing.T) {
	s := isolateHistory(t)
	s.Append(history.Entry{InfoHash: "aaaa1111", Name: "A"})
	var buf bytes.Buffer
	if code := runHistory([]string{"clear"}, &buf); code != 0 {
		t.Fatalf("history clear exit = %d", code)
	}
	if got := history.Load().Entries; len(got) != 0 {
		t.Fatalf("after clear, entries = %+v, want none", got)
	}
}

func TestHistoryRmAmbiguous(t *testing.T) {
	s := isolateHistory(t)
	s.Append(history.Entry{InfoHash: "aa11", Name: "A"})
	s.Append(history.Entry{InfoHash: "aa22", Name: "B"})
	var buf bytes.Buffer
	if code := runHistory([]string{"rm", "aa"}, &buf); code == 0 {
		t.Fatal("an ambiguous prefix should exit non-zero")
	}
	if len(history.Load().Entries) != 2 {
		t.Fatal("an ambiguous rm must not remove anything")
	}
}

func TestHistoryRmDeleteFilesRefusesEscape(t *testing.T) {
	s := isolateHistory(t) // isolates HOME/XDG so config.Load().DataDir is under a temp dir too
	s.Append(history.Entry{InfoHash: "cccc3333", Name: "../evil"})

	// Where Name="../evil" would land if the containment guard were bypassed:
	// engine.RemoveUnderDir(dataDir, "../evil") targets <parent-of-dataDir>/evil.
	dataDir := config.Load().DataDir
	escape := filepath.Join(filepath.Dir(dataDir), "evil")
	if err := os.MkdirAll(filepath.Dir(escape), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(escape, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}

	var buf bytes.Buffer
	runHistory([]string{"rm", "cccc", "--delete-files"}, &buf) // no daemon → direct delete path

	if _, err := os.Stat(escape); err != nil {
		t.Fatal("--delete-files deleted a path escaping the data dir; containment guard failed")
	}
}
