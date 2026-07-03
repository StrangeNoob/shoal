package source

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
)

// MultiSource fans a single query out across several providers concurrently and
// returns their merged results. It implements Source itself, so the rest of the
// app (the UI holds one source.Source) needs no changes to gain multi-source
// search — each Result still carries its own .Source label for provenance.
type MultiSource struct {
	sources []Source
}

// NewMulti composes several sources into one. Order is irrelevant; results are
// merged and re-sorted.
func NewMulti(sources ...Source) *MultiSource {
	return &MultiSource{sources: sources}
}

// Name is the providing source's name when there's one, else a count (kept short
// for the sidebar; per-result provenance lives on Result.Source).
func (m *MultiSource) Name() string {
	switch len(m.sources) {
	case 0:
		return "no sources"
	case 1:
		return m.sources[0].Name()
	default:
		return fmt.Sprintf("%d sources", len(m.sources))
	}
}

// Search queries every source concurrently and merges the hits. A source that
// fails is skipped; an error is only returned if *every* source failed, so one
// flaky provider never blanks the results.
// normalizeQuery folds a search query to lowercase before it is fanned out to
// the providers. Torrent search is case-insensitive by intent, but some upstream
// APIs are not: apibay (The Pirate Bay) returns nothing for "Her" yet 100 hits
// for "her". Every source is safe to receive a lowercased query — the local
// matchers (curated, 1337x) already lowercase internally, and the rest hit
// case-insensitive remote APIs.
func normalizeQuery(query string) string {
	return strings.ToLower(query)
}

func (m *MultiSource) Search(ctx context.Context, query string) ([]Result, error) {
	query = normalizeQuery(query)
	results := make([][]Result, len(m.sources))
	errs := make([]error, len(m.sources))

	var wg sync.WaitGroup
	for i, s := range m.sources {
		wg.Add(1)
		go func(i int, s Source) {
			defer wg.Done()
			results[i], errs[i] = s.Search(ctx, query) // each goroutine owns its own index — no shared writes
		}(i, s)
	}
	wg.Wait()

	var merged []Result
	var firstErr error
	failed := 0
	for i := range m.sources {
		if errs[i] != nil {
			failed++
			if firstErr == nil {
				firstErr = errs[i]
			}
			continue
		}
		merged = append(merged, results[i]...)
	}
	if len(m.sources) > 0 && failed == len(m.sources) {
		return nil, firstErr
	}

	// Popularity scales differ between providers, so this is a rough health
	// ordering, not an exact ranking. Stable so same-popularity ties keep their
	// source order.
	sort.SliceStable(merged, func(a, b int) bool { return merged[a].Popularity > merged[b].Popularity })
	return merged, nil
}

// SourceUpdate is one source's contribution to a streaming search.
type SourceUpdate struct {
	Results []Result
	Err     error // this source's error, if any (non-fatal)
	Done    int   // sources finished so far, including this one
	Total   int   // total sources in the search
}

// SearchStream fans out like Search but sends each source's result on ch as it
// arrives (ordered by completion, not source order), then closes ch. A source
// error travels in SourceUpdate.Err and does not abort the others.
func (m *MultiSource) SearchStream(ctx context.Context, query string, ch chan<- SourceUpdate) {
	defer close(ch)
	query = normalizeQuery(query)
	total := len(m.sources)
	if total == 0 {
		return
	}
	var done int64
	var wg sync.WaitGroup
	for _, s := range m.sources {
		wg.Add(1)
		go func(s Source) {
			defer wg.Done()
			res, err := s.Search(ctx, query)
			up := SourceUpdate{
				Results: res,
				Err:     err,
				Done:    int(atomic.AddInt64(&done, 1)),
				Total:   total,
			}
			select {
			case ch <- up:
			case <-ctx.Done(): // reader gave up (search superseded)
			}
		}(s)
	}
	wg.Wait()
}
