package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/StrangeNoob/shoal/internal/config"
	"github.com/StrangeNoob/shoal/internal/engine"
	"github.com/StrangeNoob/shoal/internal/history"
)

func TestPauseResumeRemoveForwardRPC(t *testing.T) {
	fake := &fakeEngine{statuses: []engine.Status{{InfoHash: "abcdef0123", Name: "M"}}}
	serveFakeDaemon(t, fake)

	var buf bytes.Buffer
	if code := runPause([]string{"abcdef"}, &buf); code != 0 {
		t.Fatalf("pause exit = %d", code)
	}
	if p := fake.gotPaused(); len(p) != 1 || p[0] != "abcdef0123" {
		t.Fatalf("pause did not forward: %v", p)
	}
	if code := runResume([]string{"abcdef"}, &buf); code != 0 {
		t.Fatalf("resume exit = %d", code)
	}
	if r := fake.gotResumed(); len(r) != 1 {
		t.Fatalf("resume did not forward: %v", r)
	}
	if code := runRemove([]string{"abcdef", "--delete-files"}, &buf); code != 0 {
		t.Fatalf("remove exit = %d", code)
	}
	r, d := fake.gotRemoved(), fake.gotRemovedDeleteFlags()
	if len(r) != 1 || r[0] != "abcdef0123" || len(d) != 1 || !d[0] {
		t.Fatalf("remove did not forward deleteData=true: %v %v", r, d)
	}
}

func TestControlNoDaemon(t *testing.T) {
	t.Setenv("SHOAL_DAEMON_SOCK", t.TempDir()+"/absent.sock")
	var buf bytes.Buffer
	if code := runPause([]string{"x"}, &buf); code != 0 {
		t.Fatalf("exit = %d", code)
	}
	if !strings.Contains(buf.String(), "no active downloads") {
		t.Fatalf("output = %q", buf.String())
	}
}

func TestOpenAmbiguous(t *testing.T) {
	fake := &fakeEngine{statuses: []engine.Status{
		{InfoHash: "aa11", Path: "/d/one"},
		{InfoHash: "aa22", Path: "/d/two"},
	}}
	serveFakeDaemon(t, fake)
	var buf bytes.Buffer
	if code := runOpen([]string{"aa"}, &buf); code == 0 {
		t.Fatal("open with an ambiguous prefix should exit non-zero")
	}
}

func TestOpenRefusesEscapingPath(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("HOME", dir)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(dir, ".config"))
	// A history entry whose name escapes the data dir, with the escape target existing.
	seed := history.Load()
	seed.Append(history.Entry{InfoHash: "esc1", Name: "../evil"})
	dataDir := config.Load().DataDir
	escape := filepath.Join(filepath.Dir(dataDir), "evil")
	if err := os.MkdirAll(escape, 0o755); err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	if code := runOpen([]string{"esc1"}, &buf); code == 0 {
		t.Fatal("open must not resolve a path escaping the data dir")
	}
}

func TestControlAmbiguousAndMissing(t *testing.T) {
	fake := &fakeEngine{statuses: []engine.Status{
		{InfoHash: "aa11"}, {InfoHash: "aa22"},
	}}
	serveFakeDaemon(t, fake)
	var buf bytes.Buffer
	if code := runPause([]string{"aa"}, &buf); code == 0 {
		t.Fatal("ambiguous id should be a non-zero exit")
	}
	if code := runPause([]string{"zz"}, &buf); code == 0 {
		t.Fatal("missing id should be a non-zero exit")
	}
}
