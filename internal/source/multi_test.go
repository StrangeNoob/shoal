package source

import (
	"context"
	"errors"
	"testing"
)

type stubSource struct {
	name    string
	results []Result
	err     error
}

func (s stubSource) Name() string { return s.name }
func (s stubSource) Search(ctx context.Context, q string) ([]Result, error) {
	return s.results, s.err
}

func TestMultiMergesAndSortsByPopularity(t *testing.T) {
	a := stubSource{name: "A", results: []Result{{Title: "low", Popularity: 1}, {Title: "high", Popularity: 100}}}
	b := stubSource{name: "B", results: []Result{{Title: "mid", Popularity: 50}}}

	got, err := NewMulti(a, b).Search(context.Background(), "q")
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(got) != 3 {
		t.Fatalf("merged %d results, want 3", len(got))
	}
	if got[0].Title != "high" || got[1].Title != "mid" || got[2].Title != "low" {
		t.Errorf("order = %s/%s/%s, want high/mid/low", got[0].Title, got[1].Title, got[2].Title)
	}
}

func TestMultiToleratesPartialFailure(t *testing.T) {
	good := stubSource{name: "good", results: []Result{{Title: "ok"}}}
	bad := stubSource{name: "bad", err: errors.New("boom")}

	got, err := NewMulti(bad, good).Search(context.Background(), "q")
	if err != nil {
		t.Fatalf("partial failure should not error, got %v", err)
	}
	if len(got) != 1 || got[0].Title != "ok" {
		t.Errorf("got %v, want the one healthy result", got)
	}
}

func TestMultiErrorsOnlyWhenAllFail(t *testing.T) {
	a := stubSource{name: "a", err: errors.New("a down")}
	b := stubSource{name: "b", err: errors.New("b down")}
	if _, err := NewMulti(a, b).Search(context.Background(), "q"); err == nil {
		t.Fatal("expected an error when every source fails")
	}
}

func TestMultiName(t *testing.T) {
	if got := NewMulti(stubSource{name: "Only"}).Name(); got != "Only" {
		t.Errorf("single-source Name = %q, want Only", got)
	}
	if got := NewMulti(stubSource{name: "A"}, stubSource{name: "B"}).Name(); got != "2 sources" {
		t.Errorf("multi Name = %q, want '2 sources'", got)
	}
}

func TestMultiEmpty(t *testing.T) {
	got, err := NewMulti().Search(context.Background(), "q")
	if err != nil || len(got) != 0 {
		t.Errorf("empty MultiSource: got %v err %v, want empty/nil", got, err)
	}
}

func TestSearchStreamEmitsPerSourceAndCloses(t *testing.T) {
	ok1 := stubSource{name: "A", results: []Result{{Title: "a1", Popularity: 5}}}
	ok2 := stubSource{name: "B", results: []Result{{Title: "b1", Popularity: 9}}}
	bad := stubSource{name: "C", err: errors.New("boom")}
	m := NewMulti(ok1, ok2, bad)

	ch := make(chan SourceUpdate)
	go m.SearchStream(context.Background(), "q", ch)

	var updates []SourceUpdate
	for up := range ch {
		updates = append(updates, up)
	}
	if len(updates) != 3 {
		t.Fatalf("updates = %d, want 3", len(updates))
	}
	titles := map[string]bool{}
	sawErr := false
	maxDone := 0
	for _, up := range updates {
		if up.Total != 3 {
			t.Fatalf("Total = %d, want 3", up.Total)
		}
		if up.Done > maxDone {
			maxDone = up.Done
		}
		if up.Err != nil {
			sawErr = true
		}
		for _, r := range up.Results {
			titles[r.Title] = true
		}
	}
	if maxDone != 3 {
		t.Fatalf("max Done = %d, want 3", maxDone)
	}
	if !sawErr {
		t.Fatalf("expected the failing source to report Err")
	}
	if !titles["a1"] || !titles["b1"] {
		t.Fatalf("merged titles = %v, want a1 and b1", titles)
	}
}
