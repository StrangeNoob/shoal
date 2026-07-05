package main

import (
	"bytes"
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/StrangeNoob/shoal/internal/engine"
)

func TestFollowStatusRendersThenStops(t *testing.T) {
	old := statusPollInterval
	statusPollInterval = time.Millisecond
	t.Cleanup(func() { statusPollInterval = old })

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // already canceled → render once, then return

	var buf bytes.Buffer
	rows := []statusRow{{ID: "abcd1234", Name: "Movie", State: "downloading"}}
	code := followStatus(ctx, func() []statusRow { return rows }, &buf)
	if code != 0 {
		t.Fatalf("exit = %d, want 0", code)
	}
	if !strings.Contains(buf.String(), "Movie") {
		t.Fatalf("follow did not render the row:\n%q", buf.String())
	}
}

func TestFilterStatuses(t *testing.T) {
	in := []engine.Status{
		{InfoHash: "abcdef0123"}, {InfoHash: "abc999"}, {InfoHash: " zzz"},
	}
	got := filterStatuses(in, "abc")
	if len(got) != 2 {
		t.Fatalf("prefix abc matched %d, want 2", len(got))
	}
	if all := filterStatuses(in, ""); len(all) != 3 {
		t.Fatalf("empty prefix should keep all, got %d", len(all))
	}
}

func TestStatusState(t *testing.T) {
	cases := []struct {
		s    engine.Status
		want string
	}{
		{engine.Status{Done: true, Seeding: true}, "seeding"},
		{engine.Status{Done: true}, "done"},
		{engine.Status{Paused: true}, "paused"},
		{engine.Status{}, "downloading"},
	}
	for _, c := range cases {
		if got := statusState(c.s); got != c.want {
			t.Errorf("statusState(%+v) = %q, want %q", c.s, got, c.want)
		}
	}
}

func TestStatusFromDaemonJSON(t *testing.T) {
	fake := &fakeEngine{statuses: []engine.Status{
		{Name: "Movie", InfoHash: "abcdef0123456789", TotalBytes: 200, CompletedBytes: 100, Peers: 4},
	}}
	serveFakeDaemon(t, fake)
	var buf bytes.Buffer
	if code := runStatus([]string{"--json"}, &buf); code != 0 {
		t.Fatalf("exit = %d", code)
	}
	var rows []map[string]any
	if err := json.Unmarshal(buf.Bytes(), &rows); err != nil {
		t.Fatalf("bad json: %v\n%s", err, buf.String())
	}
	if len(rows) != 1 || rows[0]["state"] != "downloading" || rows[0]["id"] != "abcdef01" || rows[0]["percent"].(float64) != 0.5 {
		t.Fatalf("row = %+v", rows[0])
	}
}

func TestStatusNoDaemon(t *testing.T) {
	// point at a socket with no listener → no daemon → "no downloads", exit 0
	t.Setenv("SHOAL_DAEMON_SOCK", filepath.Join(t.TempDir(), "absent.sock"))
	var buf bytes.Buffer
	if code := runStatus(nil, &buf); code != 0 {
		t.Fatalf("exit = %d", code)
	}
	if !strings.Contains(buf.String(), "no downloads") {
		t.Fatalf("expected 'no downloads', got %q", buf.String())
	}
}

func TestStatusNoDaemonJSON(t *testing.T) {
	t.Setenv("SHOAL_DAEMON_SOCK", filepath.Join(t.TempDir(), "absent.sock"))
	var buf bytes.Buffer
	if code := runStatus([]string{"--json"}, &buf); code != 0 {
		t.Fatalf("exit = %d", code)
	}
	out := strings.TrimSpace(buf.String())
	if out != "[]" {
		t.Fatalf("expected empty JSON array '[]', got %q", out)
	}
}

func TestStatusClearRemovesDone(t *testing.T) {
	fake := &fakeEngine{statuses: []engine.Status{
		{Name: "Done", InfoHash: "aaaa1111bbbb2222", Done: true},
	}}
	serveFakeDaemon(t, fake)
	var buf bytes.Buffer
	if code := runStatus([]string{"--clear"}, &buf); code != 0 {
		t.Fatalf("exit = %d", code)
	}
	if r := fake.gotRemoved(); len(r) != 1 || r[0] != "aaaa1111bbbb2222" {
		t.Fatalf("--clear should Remove the done torrent, got %v", r)
	}
}

func TestStatusClearKeepsSeeding(t *testing.T) {
	fake := &fakeEngine{statuses: []engine.Status{
		{InfoHash: "seed1", Done: true, Seeding: true},
		{InfoHash: "done1", Done: true},
	}}
	serveFakeDaemon(t, fake)
	var buf bytes.Buffer
	if code := runStatus([]string{"--clear"}, &buf); code != 0 {
		t.Fatalf("exit = %d", code)
	}
	if r := fake.gotRemoved(); len(r) != 1 || r[0] != "done1" {
		t.Fatalf("--clear must keep seeding torrents; removed=%v", r)
	}
}
