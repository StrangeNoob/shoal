package main

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/StrangeNoob/shoal/internal/source"
)

type fakeSource struct{ res []source.Result }

func (f fakeSource) Name() string                                            { return "fake" }
func (f fakeSource) Search(context.Context, string) ([]source.Result, error) { return f.res, nil }

func TestToRowsSortsAndDerivesID(t *testing.T) {
	const ih = "0123456789abcdef0123456789abcdef01234567"
	in := []source.Result{
		{Title: "Low", Seeders: 2, Magnet: "magnet:?xt=urn:btih:" + ih},
		{Title: "High", Seeders: 40, Magnet: "magnet:?xt=urn:btih:" + ih},
		{Title: "NoMagnet", Seeders: 1}, // no magnet -> id "—", fewest seeders
	}
	rows := toRows(in, 30)
	if rows[0].Title != "High" {
		t.Errorf("expected most-seeded first, got %q", rows[0].Title)
	}
	if rows[0].ID != "01234567" {
		t.Errorf("id = %q, want 01234567", rows[0].ID)
	}
	var noMag searchRow
	for _, r := range rows {
		if r.Title == "NoMagnet" {
			noMag = r
		}
	}
	if noMag.ID != "—" {
		t.Errorf("magnet-less id = %q, want —", noMag.ID)
	}
	if got := toRows(in, 1); len(got) != 1 {
		t.Errorf("limit not applied: len %d", len(got))
	}
}

func TestSearchCoreWritesCacheAndJSON(t *testing.T) {
	base := t.TempDir()
	const ih = "abcdef0123456789abcdef0123456789abcdef01"
	src := fakeSource{res: []source.Result{
		{Title: "Movie", Seeders: 10, SizeBytes: 1024, Source: "fake", Magnet: "magnet:?xt=urn:btih:" + ih},
	}}
	rows, err := searchCore(context.Background(), src, "movie", 30, base)
	if err != nil || len(rows) != 1 {
		t.Fatalf("searchCore rows %d err %v", len(rows), err)
	}
	// cache resolvable by the derived id
	e, ok := lookupCache(base, "abcdef01")
	if !ok || e.Title != "Movie" {
		t.Fatalf("cache miss: %+v ok %v", e, ok)
	}
	// JSON shape round-trips
	b, _ := json.Marshal(rows)
	var back []map[string]any
	if json.Unmarshal(b, &back) != nil || back[0]["magnet"] == "" {
		t.Fatalf("bad json: %s", b)
	}
}
