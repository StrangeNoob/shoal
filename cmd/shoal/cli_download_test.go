package main

import (
	"bytes"
	"strings"
	"testing"
)

func TestResolveTarget(t *testing.T) {
	lookup := func(id string) (string, bool) {
		if id == "a1b2c3d4" {
			return "magnet:?xt=urn:btih:a1b2c3d4a1b2c3d4a1b2c3d4a1b2c3d4a1b2c3d4&dn=Cached", true
		}
		return "", false
	}
	const ih = "0123456789abcdef0123456789abcdef01234567"
	cases := []struct {
		name       string
		arg        string
		wantMagnet string
		wantURL    string
		wantHandle string
		wantErr    bool
	}{
		{"magnet", "magnet:?xt=urn:btih:" + ih + "&dn=X", "magnet:?xt=urn:btih:" + ih + "&dn=X", "", "01234567", false},
		{"http url", "https://example.com/x.torrent", "", "https://example.com/x.torrent", "", false},
		{"infohash40", ih, "magnet:?xt=urn:btih:" + ih, "", "01234567", false},
		{"short id hit", "a1b2c3d4", "magnet:?xt=urn:btih:a1b2c3d4a1b2c3d4a1b2c3d4a1b2c3d4a1b2c3d4&dn=Cached", "", "a1b2c3d4", false},
		{"short id miss", "deadbeef", "", "", "", true},
		{"garbage", "not a torrent", "", "", "", true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := resolveTarget(c.arg, lookup)
			if c.wantErr {
				if err == nil {
					t.Fatalf("want error, got %+v", got)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got.Magnet != c.wantMagnet || got.URL != c.wantURL {
				t.Errorf("target = %+v, want magnet=%q url=%q", got, c.wantMagnet, c.wantURL)
			}
			if c.name == "http url" {
				if len(got.Handle) != 8 {
					t.Errorf("url handle = %q, want 8 hex chars", got.Handle)
				}
			} else if got.Handle != c.wantHandle {
				t.Errorf("handle = %q, want %q", got.Handle, c.wantHandle)
			}
		})
	}
}

func TestResolveTargetNilLookup(t *testing.T) {
	got, err := resolveTarget("deadbeef", nil)
	if err == nil {
		t.Fatalf("nil lookup should error, got %+v", got)
	}
}

func TestDownloadForwardsMagnetToDaemon(t *testing.T) {
	fake := &fakeEngine{}
	serveFakeDaemon(t, fake)
	const ih = "0123456789abcdef0123456789abcdef01234567"
	var buf bytes.Buffer
	if code := runDownload([]string{"magnet:?xt=urn:btih:" + ih}, &buf); code != 0 {
		t.Fatalf("exit = %d", code)
	}
	if m := fake.gotMagnets(); len(m) != 1 || !strings.Contains(m[0], ih) {
		t.Fatalf("daemon did not receive the magnet: %v", m)
	}
	if !strings.Contains(buf.String(), "started:") {
		t.Fatalf("expected a 'started:' line, got %q", buf.String())
	}
}

func TestDownloadForwardsURLToDaemon(t *testing.T) {
	fake := &fakeEngine{}
	serveFakeDaemon(t, fake)
	var buf bytes.Buffer
	if code := runDownload([]string{"https://example.com/x.torrent"}, &buf); code != 0 {
		t.Fatalf("exit = %d", code)
	}
	if u := fake.gotURLs(); len(u) != 1 || u[0][0] != "https://example.com/x.torrent" {
		t.Fatalf("daemon did not receive the URL: %v", u)
	}
}

func TestDownloadURLPrintsNoHandle(t *testing.T) {
	fake := &fakeEngine{}
	serveFakeDaemon(t, fake)
	var buf bytes.Buffer
	if code := runDownload([]string{"https://example.com/x.torrent"}, &buf); code != 0 {
		t.Fatalf("exit = %d", code)
	}
	// a URL download's sha1 handle is not queryable via status, so it must not be printed
	if strings.Contains(buf.String(), "(") {
		t.Fatalf("URL download should not print a handle, got %q", buf.String())
	}
}
