package main

import (
	"bytes"
	"strings"
	"testing"

	"github.com/StrangeNoob/shoal/internal/engine"
)

func TestFilesListAndOnly(t *testing.T) {
	fake := &fakeEngine{
		statuses: []engine.Status{{InfoHash: "abc" + strings.Repeat("0", 37), Name: "test torrent"}},
		detail: engine.Detail{Files: []engine.FileDetail{
			{Path: "movie.mkv", Length: 100, Completed: 50, Selected: true},
			{Path: "readme.txt", Length: 10, Selected: true},
		}},
	}
	serveFakeDaemon(t, fake)

	var buf bytes.Buffer
	if code := runFiles([]string{"abc"}, &buf); code != 0 {
		t.Fatalf("exit = %d: %s", code, buf.String())
	}
	if !strings.Contains(buf.String(), "movie.mkv") || !strings.Contains(buf.String(), "readme.txt") {
		t.Fatalf("files listing missing entries:\n%s", buf.String())
	}

	// --only '*.mkv' should deselect readme.txt (SetFiles paths=[readme.txt] selected=false)
	if code := runFiles([]string{"abc", "--only", "*.mkv"}, &buf); code != 0 {
		t.Fatalf("--only exit = %d", code)
	}
	off := fake.setFilesDeselected()
	if len(off) != 1 || off[0] != "readme.txt" {
		t.Fatalf("--only deselected %v, want [readme.txt]", off)
	}
}

func TestFilesOnlyNoMatch(t *testing.T) {
	fake := &fakeEngine{
		statuses: []engine.Status{{InfoHash: "abc" + strings.Repeat("0", 37), Name: "test torrent"}},
		detail: engine.Detail{Files: []engine.FileDetail{
			{Path: "movie.mkv", Length: 100, Completed: 50, Selected: true},
			{Path: "readme.txt", Length: 10, Selected: true},
		}},
	}
	serveFakeDaemon(t, fake)

	var buf bytes.Buffer
	if code := runFiles([]string{"abc", "--only", "*.nope"}, &buf); code == 0 {
		t.Fatalf("--only with no matches should fail, got exit 0")
	}
	if len(fake.filesCalls) != 0 {
		t.Fatalf("--only with no matches must not call SetFiles, got %v", fake.filesCalls)
	}
}

func TestFilesNoID(t *testing.T) {
	var buf bytes.Buffer
	if code := runFiles(nil, &buf); code != 2 {
		t.Fatalf("exit = %d, want 2", code)
	}
}
