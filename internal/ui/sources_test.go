package ui

import (
	"testing"

	"github.com/StrangeNoob/shoal/internal/config"
	"github.com/StrangeNoob/shoal/internal/source"
)

func TestSourceSettingToggles(t *testing.T) {
	m := &Model{cfg: config.Config{}}
	var eztv *setItem
	items := settingItems()
	for i := range items {
		if items[i].group == "SOURCES" && items[i].label == "EZTV" {
			eztv = &items[i]
		}
	}
	if eztv == nil {
		t.Fatal("expected a SOURCES row for EZTV")
	}
	if eztv.get(m) != "on" {
		t.Fatalf("EZTV should default to on, got %q", eztv.get(m))
	}
	eztv.set(m, "off")
	if eztv.get(m) != "off" || m.cfg.SourceEnabled("EZTV") {
		t.Fatalf("toggling off failed: get=%q disabled=%v", eztv.get(m), m.cfg.DisabledSources)
	}
}

func TestEnabledSourceHonorsConfig(t *testing.T) {
	// all providers disabled → empty multi-source
	var names []string
	for _, s := range source.DefaultSources() {
		names = append(names, s.Name())
	}
	m := &Model{cfg: config.Config{DisabledSources: names}}
	if got := m.enabledSource().Name(); got != "no sources" {
		t.Fatalf("all-disabled should build an empty source set, got %q", got)
	}
	// one disabled → the rest remain
	m2 := &Model{cfg: config.Config{DisabledSources: []string{"EZTV"}}}
	if m2.enabledSource().Name() == "no sources" {
		t.Fatal("only one disabled — the set should be non-empty")
	}
}

func TestStartSearchRebuildsFromConfig(t *testing.T) {
	var names []string
	for _, s := range source.DefaultSources() {
		names = append(names, s.Name())
	}
	// production-shaped: m.src is a real *MultiSource; every provider disabled
	m := &Model{src: source.NewDefault(), cfg: config.Config{DisabledSources: names}}
	_ = m.startSearch("anything")
	if m.src.Name() != "no sources" {
		t.Fatalf("startSearch should rebuild m.src from config; got %q", m.src.Name())
	}
	if m.searchCancel != nil {
		m.searchCancel() // clean up the context startSearch created
	}
}
