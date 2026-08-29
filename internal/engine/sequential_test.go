package engine

import (
	"testing"

	"github.com/anacrolix/torrent"
)

func noneComplete(int) bool { return false }

func TestPlanSequentialFirstAndLastPieceNow(t *testing.T) {
	// one file spanning pieces [10, 20)
	got := planSequential([]pieceSpan{{10, 20}}, noneComplete, 1<<20, 4<<20)
	if got[10] != torrent.PiecePriorityNow {
		t.Fatalf("first piece = %v, want Now", got[10])
	}
	if got[19] != torrent.PiecePriorityNow {
		t.Fatalf("last piece = %v, want Now", got[19])
	}
}

func TestPlanSequentialWindowFromEarliestIncomplete(t *testing.T) {
	// pieces 10..13 already complete; window = 4 pieces of 1 MiB
	complete := func(i int) bool { return i >= 10 && i <= 13 }
	got := planSequential([]pieceSpan{{10, 30}}, complete, 1<<20, 4<<20)
	for i := 14; i <= 17; i++ {
		if got[i] != torrent.PiecePriorityHigh {
			t.Fatalf("piece %d = %v, want High (window)", i, got[i])
		}
	}
	if _, ok := got[18]; ok {
		t.Fatalf("piece 18 beyond window should be untouched, got %v", got[18])
	}
	if _, ok := got[10]; ok {
		t.Fatalf("complete piece 10 should be untouched (no first-piece bump needed)")
	}
	if got[29] != torrent.PiecePriorityNow {
		t.Fatalf("incomplete last piece = %v, want Now", got[29])
	}
}

func TestPlanSequentialSingleAndTinyFiles(t *testing.T) {
	// single-piece file: first == last -> one Now entry, no window duplicates
	got := planSequential([]pieceSpan{{5, 6}}, noneComplete, 1<<20, 4<<20)
	if len(got) != 1 || got[5] != torrent.PiecePriorityNow {
		t.Fatalf("single-piece file: got %v, want {5:Now}", got)
	}
	// empty span slice -> empty plan
	if got := planSequential(nil, noneComplete, 1<<20, 4<<20); len(got) != 0 {
		t.Fatalf("no spans should plan nothing, got %v", got)
	}
}

func TestPlanSequentialMultipleFilesIndependentWindows(t *testing.T) {
	got := planSequential([]pieceSpan{{0, 10}, {10, 20}}, noneComplete, 1<<20, 2<<20)
	for _, want := range []int{0, 9, 10, 19} {
		if got[want] != torrent.PiecePriorityNow {
			t.Fatalf("piece %d = %v, want Now (file boundary)", want, got[want])
		}
	}
	// each file gets its own 2-piece window past the boundary pieces
	if got[1] != torrent.PiecePriorityHigh || got[11] != torrent.PiecePriorityHigh {
		t.Fatalf("window pieces 1/11 should be High, got %v/%v", got[1], got[11])
	}
}
