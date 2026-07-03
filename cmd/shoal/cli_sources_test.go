package main

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
)

func TestRunSourcesJSON(t *testing.T) {
	var buf bytes.Buffer
	if code := runSources([]string{"--json"}, &buf); code != 0 {
		t.Fatalf("exit = %d", code)
	}
	var names []string
	if err := json.Unmarshal(buf.Bytes(), &names); err != nil {
		t.Fatalf("bad json: %v\n%s", err, buf.String())
	}
	found := false
	for _, n := range names {
		if n == "Internet Archive" {
			found = true
		}
	}
	if !found || len(names) < 10 {
		t.Fatalf("expected the full provider list incl. Internet Archive, got %v", names)
	}
}

func TestRunSourcesPlain(t *testing.T) {
	var buf bytes.Buffer
	if code := runSources(nil, &buf); code != 0 {
		t.Fatalf("exit = %d", code)
	}
	if !strings.Contains(buf.String(), "Internet Archive\n") {
		t.Fatalf("plain output should list one provider per line:\n%s", buf.String())
	}
}
