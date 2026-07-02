package main

import (
	"bytes"
	"strings"
	"testing"
)

func TestPickVersion(t *testing.T) {
	cases := []struct{ ldflags, buildInfo, want string }{
		{"0.4.2", "v0.4.2", "0.4.2"},                                 // release (GoReleaser) build: ldflags wins
		{"dev", "v0.4.2", "v0.4.2"},                                  // `go install pkg@v0.4.2`: fall back to build info
		{"", "v0.4.2", "v0.4.2"},                                     // ldflags unset: build info
		{"dev", "v0.5.0-rc1", "v0.5.0-rc1"},                          // a real prerelease tag is still a release
		{"dev", "(devel)", "dev"},                                    // local `go build` in a checkout
		{"dev", "v0.4.1-0.20260702143006-aa296a01f28f", "dev"},       // pseudo-version → dev
		{"dev", "v0.4.1-0.20260702143006-aa296a01f28f+dirty", "dev"}, // pseudo + dirty → dev
		{"dev", "v0.4.2+dirty", "dev"},                               // dirty tag → dev
		{"dev", "", "dev"},                                           // no version anywhere
	}
	for _, c := range cases {
		if got := pickVersion(c.ldflags, c.buildInfo); got != c.want {
			t.Errorf("pickVersion(%q, %q) = %q, want %q", c.ldflags, c.buildInfo, got, c.want)
		}
	}
}

func TestCLIVersionAndHelp(t *testing.T) {
	var out bytes.Buffer
	if handled, code := cli([]string{"shoal", "version"}, "0.3.0", &out); !handled || code != 0 {
		t.Fatalf("version: handled=%v code=%d", handled, code)
	}
	if !strings.Contains(out.String(), "shoal v0.3.0") {
		t.Fatalf("version output = %q", out.String())
	}

	out.Reset()
	if handled, _ := cli([]string{"shoal", "help"}, "0.3.0", &out); !handled || !strings.Contains(out.String(), "update") {
		t.Fatalf("help output = %q", out.String())
	}

	// no subcommand → not handled (caller launches the TUI)
	if handled, _ := cli([]string{"shoal"}, "0.3.0", &out); handled {
		t.Fatal("no-args should not be handled by cli()")
	}
}
