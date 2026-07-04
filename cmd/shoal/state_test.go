package main

import "testing"

func TestCacheRoundTripAndMiss(t *testing.T) {
	base := t.TempDir()
	if _, ok := lookupCache(base, "nope0000"); ok {
		t.Fatal("expected miss on empty cache")
	}
	m := map[string]cacheEntry{"01234567": {Magnet: "magnet:?xt=urn:btih:x", Title: "Movie"}}
	if err := writeCache(base, m); err != nil {
		t.Fatal(err)
	}
	e, ok := lookupCache(base, "01234567")
	if !ok || e.Magnet != "magnet:?xt=urn:btih:x" || e.Title != "Movie" {
		t.Fatalf("lookupCache = %+v, ok %v", e, ok)
	}
}
