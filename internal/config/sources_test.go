// internal/config/sources_test.go
package config

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestSourceEnabledToggle(t *testing.T) {
	var c Config
	if !c.SourceEnabled("EZTV") {
		t.Fatal("a provider not in DisabledSources should be enabled by default")
	}
	c.SetSourceEnabled("EZTV", false)
	if c.SourceEnabled("EZTV") {
		t.Fatal("EZTV should now be disabled")
	}
	if c.SourceEnabled("eztv") {
		t.Fatal("lookup should be case-insensitive")
	}
	c.SetSourceEnabled("EZTV", false) // idempotent, no duplicate
	if len(c.DisabledSources) != 1 {
		t.Fatalf("disable should be idempotent, got %v", c.DisabledSources)
	}
	c.SetSourceEnabled("eztv", true) // case-insensitive re-enable
	if len(c.DisabledSources) != 0 || !c.SourceEnabled("EZTV") {
		t.Fatalf("re-enable should clear the entry, got %v", c.DisabledSources)
	}
}

func TestSetSourceEnabledOrderStable(t *testing.T) {
	c := Config{DisabledSources: []string{"Alpha", "Beta"}}
	c.SetSourceEnabled("Alpha", false) // redundant disable must not reorder
	if len(c.DisabledSources) != 2 || c.DisabledSources[0] != "Alpha" || c.DisabledSources[1] != "Beta" {
		t.Fatalf("redundant disable reordered: %v", c.DisabledSources)
	}
	c.SetSourceEnabled("Alpha", true) // enable removes Alpha, preserves Beta
	if len(c.DisabledSources) != 1 || c.DisabledSources[0] != "Beta" {
		t.Fatalf("enable didn't preserve order: %v", c.DisabledSources)
	}
}

func TestDisabledSourcesJSONRoundTrip(t *testing.T) {
	c := Config{DisabledSources: []string{"EZTV", "FitGirl"}}
	b, _ := json.Marshal(c)
	var back Config
	if err := json.Unmarshal(b, &back); err != nil {
		t.Fatal(err)
	}
	if len(back.DisabledSources) != 2 || back.DisabledSources[0] != "EZTV" {
		t.Fatalf("round-trip lost data: %v", back.DisabledSources)
	}
	if empty, _ := json.Marshal(Config{}); strings.Contains(string(empty), "disabled_sources") {
		t.Fatalf("empty DisabledSources must be omitted: %s", empty)
	}
}
