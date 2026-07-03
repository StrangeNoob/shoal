package source

import (
	"context"
	"testing"
)

// queryRecorder captures the exact query string it is handed.
type queryRecorder struct{ got string }

func (r *queryRecorder) Name() string { return "rec" }
func (r *queryRecorder) Search(ctx context.Context, q string) ([]Result, error) {
	r.got = q
	return nil, nil
}

// MultiSource normalizes the query to lowercase before fanning out, so a
// case-sensitive upstream (apibay returns nothing for "Her" but 100 for "her")
// behaves consistently. Covers the CLI path (Search).
func TestMultiSearchLowercasesQuery(t *testing.T) {
	rec := &queryRecorder{}
	if _, err := NewMulti(rec).Search(context.Background(), "Her 2013"); err != nil {
		t.Fatalf("Search: %v", err)
	}
	if rec.got != "her 2013" {
		t.Fatalf("sub-source received %q, want %q", rec.got, "her 2013")
	}
}

// Covers the TUI path (SearchStream). Draining the channel until it closes
// synchronizes the read of the recorder written in the source goroutine.
func TestMultiSearchStreamLowercasesQuery(t *testing.T) {
	rec := &queryRecorder{}
	ch := make(chan SourceUpdate, 1)
	go NewMulti(rec).SearchStream(context.Background(), "Big BUCK Bunny", ch)
	for range ch { //nolint:revive // drain until closed
	}
	if rec.got != "big buck bunny" {
		t.Fatalf("sub-source received %q, want %q", rec.got, "big buck bunny")
	}
}
