package main

import (
	"bytes"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
)

func TestRunSourcesJSON(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(tmp, ".config"))
	var buf bytes.Buffer
	if code := runSources([]string{"--json"}, &buf); code != 0 {
		t.Fatalf("exit = %d", code)
	}
	var rows []struct {
		Name    string `json:"name"`
		Enabled bool   `json:"enabled"`
	}
	if err := json.Unmarshal(buf.Bytes(), &rows); err != nil {
		t.Fatalf("bad json: %v\n%s", err, buf.String())
	}
	found := false
	for _, r := range rows {
		if r.Name == "Internet Archive" && r.Enabled {
			found = true
		}
	}
	if !found || len(rows) < 10 {
		t.Fatalf("expected the full provider list incl. enabled Internet Archive, got %v", rows)
	}
}

func TestRunSourcesPlain(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(tmp, ".config"))
	var buf bytes.Buffer
	if code := runSources(nil, &buf); code != 0 {
		t.Fatalf("exit = %d", code)
	}
	if !strings.Contains(buf.String(), "Internet Archive\n") {
		t.Fatalf("plain output should list one provider per line:\n%s", buf.String())
	}
}

func TestMatchSourceName(t *testing.T) {
	if got, ok := matchSourceName("eztv"); !ok || got != "EZTV" {
		t.Fatalf("matchSourceName(eztv) = %q,%v want EZTV,true", got, ok)
	}
	if _, ok := matchSourceName("nope"); ok {
		t.Fatal("unknown name should not match")
	}
}

func TestSourcesEnableDisableAndStatus(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(tmp, ".config"))

	var buf bytes.Buffer
	if code := runSources([]string{"disable", "eztv"}, &buf); code != 0 {
		t.Fatalf("disable exit %d: %s", code, buf.String())
	}

	buf.Reset()
	if code := runSources([]string{"--json"}, &buf); code != 0 {
		t.Fatalf("list exit %d", code)
	}
	var rows []struct {
		Name    string `json:"name"`
		Enabled bool   `json:"enabled"`
	}
	if err := json.Unmarshal(buf.Bytes(), &rows); err != nil {
		t.Fatalf("bad json: %v\n%s", err, buf.String())
	}
	sawEZTVoff := false
	for _, r := range rows {
		if r.Name == "EZTV" {
			if r.Enabled {
				t.Fatal("EZTV should be disabled after `disable`")
			}
			sawEZTVoff = true
		}
	}
	if !sawEZTVoff {
		t.Fatal("EZTV row missing from status list")
	}

	// unknown name errors
	buf.Reset()
	if code := runSources([]string{"disable", "not-a-source"}, &buf); code != 1 {
		t.Fatalf("unknown source should exit 1, got %d", code)
	}

	// re-enable
	buf.Reset()
	if code := runSources([]string{"enable", "EZTV"}, &buf); code != 0 {
		t.Fatalf("enable exit %d", code)
	}
}
