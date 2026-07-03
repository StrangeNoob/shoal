package main

import "testing"

func TestActiveRoundTripAndClear(t *testing.T) {
	base := t.TempDir()
	if err := writeActive(base, Active{ID: "aaaa1111", Name: "One", Total: 100, Completed: 50}); err != nil {
		t.Fatal(err)
	}
	if err := writeActive(base, Active{ID: "bbbb2222", Name: "Two", Total: 100, Completed: 100, Done: true}); err != nil {
		t.Fatal(err)
	}
	got, err := readActive(base, "aaaa1111")
	if err != nil || got.Name != "One" || got.Completed != 50 {
		t.Fatalf("readActive = %+v, err %v", got, err)
	}
	items, err := listActive(base)
	if err != nil || len(items) != 2 {
		t.Fatalf("listActive len = %d, err %v", len(items), err)
	}
	n, err := clearFinished(base)
	if err != nil || n != 1 {
		t.Fatalf("clearFinished removed %d, err %v", n, err)
	}
	items, _ = listActive(base)
	if len(items) != 1 || items[0].ID != "aaaa1111" {
		t.Fatalf("after clear = %+v", items)
	}
}

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
