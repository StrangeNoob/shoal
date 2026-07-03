package main

import (
	"flag"
	"testing"
)

func TestParseArgsFlagPositions(t *testing.T) {
	newFS := func() (*flag.FlagSet, *string, *bool) {
		fs := flag.NewFlagSet("t", flag.ContinueOnError)
		out := fs.String("out", "", "")
		j := fs.Bool("json", false, "")
		return fs, out, j
	}

	// flags AFTER the positional (the case stdlib flag drops)
	fs, out, j := newFS()
	pos, err := parseArgs(fs, []string{"myid", "--out", "/x", "--json"})
	if err != nil {
		t.Fatal(err)
	}
	if len(pos) != 1 || pos[0] != "myid" || *out != "/x" || !*j {
		t.Fatalf("after-case: pos=%v out=%q json=%v", pos, *out, *j)
	}

	// flags BEFORE the positional still work
	fs2, out2, _ := newFS()
	pos2, _ := parseArgs(fs2, []string{"--out", "/y", "myid"})
	if len(pos2) != 1 || pos2[0] != "myid" || *out2 != "/y" {
		t.Fatalf("before-case: pos=%v out=%q", pos2, *out2)
	}

	// multi-word positionals with a flag interleaved (search query)
	fs3, _, j3 := newFS()
	pos3, _ := parseArgs(fs3, []string{"big", "buck", "--json", "bunny"})
	if len(pos3) != 3 || pos3[0] != "big" || pos3[2] != "bunny" || !*j3 {
		t.Fatalf("multiword: pos=%v json=%v", pos3, *j3)
	}
}
