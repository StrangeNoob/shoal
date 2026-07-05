package main

import (
	"bytes"
	"strings"
	"testing"
)

func TestRunCompletion(t *testing.T) {
	cases := map[string][]string{ // shell -> substrings the script must contain
		"bash": {"complete -o bashdefault -o default -F _shoal shoal", "download", "shoal status"},
		"zsh":  {"#compdef shoal", "compdef _shoal shoal", "download"},
		"fish": {"complete -c shoal", "__fish_use_subcommand", "download"},
	}
	for shell, wants := range cases {
		var buf bytes.Buffer
		if code := runCompletion([]string{shell}, &buf); code != 0 {
			t.Fatalf("%s: exit = %d", shell, code)
		}
		for _, w := range wants {
			if !strings.Contains(buf.String(), w) {
				t.Errorf("%s script missing %q", shell, w)
			}
		}
	}
}

func TestRunCompletionRejectsUnknownShell(t *testing.T) {
	var buf bytes.Buffer
	if code := runCompletion([]string{"tcsh"}, &buf); code != 2 {
		t.Errorf("unknown shell: exit = %d, want 2", code)
	}
	if code := runCompletion(nil, &buf); code != 2 {
		t.Errorf("missing shell: exit = %d, want 2", code)
	}
}
