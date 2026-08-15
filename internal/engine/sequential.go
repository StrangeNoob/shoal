package engine

import "github.com/anacrolix/torrent"

// pieceSpan is one selected file's piece range [begin, end).
type pieceSpan struct{ begin, end int }

// seqWindowBytes is the rolling high-priority window for sequential mode.
const seqWindowBytes = int64(32) << 20

// planSequential returns the priorities to apply for a sequential torrent.
// Per selected file: the first and last incomplete pieces get PiecePriorityNow
// (video containers keep metadata at either end), then the earliest incomplete
// pieces up to windowBytes get PiecePriorityHigh. Complete pieces and pieces
// beyond the window are omitted — the engine leaves them untouched.
func planSequential(spans []pieceSpan, complete func(i int) bool, pieceLen, windowBytes int64) map[int]torrent.PiecePriority {
	plan := make(map[int]torrent.PiecePriority)
	for _, sp := range spans {
		if sp.end <= sp.begin {
			continue
		}
		if !complete(sp.begin) {
			plan[sp.begin] = torrent.PiecePriorityNow
		}
		if last := sp.end - 1; last != sp.begin && !complete(last) {
			plan[last] = torrent.PiecePriorityNow
		}
		var budget int64
		for i := sp.begin; i < sp.end && budget < windowBytes; i++ {
			if complete(i) {
				continue
			}
			if _, boundary := plan[i]; !boundary {
				plan[i] = torrent.PiecePriorityHigh
			}
			budget += pieceLen
		}
	}
	return plan
}
