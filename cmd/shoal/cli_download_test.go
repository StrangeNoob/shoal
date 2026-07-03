package main

import "testing"

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
