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
	rows := toRows(in, 30, "")
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
	if got := toRows(in, 1, ""); len(got) != 1 {
		t.Errorf("limit not applied: len %d", len(got))
	}
}

func TestToRowsSortKeys(t *testing.T) {
	const ih = "0123456789abcdef0123456789abcdef01234567"
	mag := "magnet:?xt=urn:btih:" + ih
	in := []source.Result{
		{Title: "Banana", Seeders: 5, Leechers: 1, SizeBytes: 300, Magnet: mag},
		{Title: "Apple", Seeders: 9, Leechers: 8, SizeBytes: 100, Magnet: mag},
		{Title: "Cherry", Seeders: 1, Leechers: 4, SizeBytes: 200, Magnet: mag},
	}
	cases := map[string]string{ // sortKey -> expected first Title
		"size":     "Banana", // largest first
		"leechers": "Apple",  // most leechers first
		"name":     "Apple",  // alphabetical
		"seeders":  "Apple",  // most seeders first
	}
	for key, want := range cases {
		// copy so each sort starts from the same input order
		cp := append([]source.Result(nil), in...)
		if got := toRows(cp, 30, key); got[0].Title != want {
			t.Errorf("sort %q: first = %q, want %q", key, got[0].Title, want)
		}
	}
}

func TestSearchCoreMinSeeders(t *testing.T) {
	base := t.TempDir()
	const ih = "abcdef0123456789abcdef0123456789abcdef01"
	src := fakeSource{res: []source.Result{
		{Title: "Healthy", Seeders: 50, Magnet: "magnet:?xt=urn:btih:" + ih},
		{Title: "Dead", Seeders: 0, Magnet: "magnet:?xt=urn:btih:" + ih},
	}}
	rows, err := searchCore(context.Background(), src, "x", 30, base, "", 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 || rows[0].Title != "Healthy" {
		t.Fatalf("min-seeders=1 should drop the 0-seed result, got %+v", rows)
	}
}

func TestSearchCoreWritesCacheAndJSON(t *testing.T) {
	base := t.TempDir()
	const ih = "abcdef0123456789abcdef0123456789abcdef01"
	src := fakeSource{res: []source.Result{
		{Title: "Movie", Seeders: 10, SizeBytes: 1024, Source: "fake", Magnet: "magnet:?xt=urn:btih:" + ih},
	}}
	rows, err := searchCore(context.Background(), src, "movie", 30, base, "", 0)
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

func TestSelectSearchSources(t *testing.T) {
	all := source.DefaultSources() // offline: constructors only build structs

	// --source overrides a disabled provider (searches the full set)
	srcs, unknown, allDis := selectSearchSources(all, "eztv", []string{"EZTV"})
	if unknown || allDis || len(srcs) == 0 {
		t.Fatalf("--source should override disabled: srcs=%d unknown=%v allDis=%v", len(srcs), unknown, allDis)
	}

	// default (no --source) respects the disabled set
	srcs, _, _ = selectSearchSources(all, "", []string{"EZTV"})
	for _, s := range srcs {
		if s.Name() == "EZTV" {
			t.Fatal("disabled EZTV should not be searched by default")
		}
	}

	// all disabled → allDisabled flag
	var names []string
	for _, s := range all {
		names = append(names, s.Name())
	}
	if _, _, allDis = selectSearchSources(all, "", names); !allDis {
		t.Fatal("disabling every source should report allDisabled")
	}

	// unknown --source
	if _, unknown, _ = selectSearchSources(all, "zzzz", nil); !unknown {
		t.Fatal("an unmatched --source should report unknownSource")
	}
}
