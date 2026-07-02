package main

import (
	"bytes"
	"strings"
	"testing"
)

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
