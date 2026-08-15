package engine

import "github.com/anacrolix/torrent"

// pieceSpan is one selected file's piece range [begin, end).
type pieceSpan struct{ begin, end int }

// seqWindowBytes is the rolling high-priority window for sequential mode.
const seqWindowBytes = int64(32) << 20

// clearPieceBumps drops the piece-level priorities applySequential raised
// across the given files' piece spans. anacrolix's effective piece priority is
// max(file, piece), so piece-level None hands the decision back to the file's
// own priority — it never mutes a still-selected file. Without this, turning
// sequential off (or deselecting a bumped file) would leave a window of
// High/Now pieces downloading forever.
func clearPieceBumps(t *torrent.Torrent, files []*torrent.File) {
	for _, f := range files {
		for i := f.BeginPieceIndex(); i < f.EndPieceIndex(); i++ {
			t.Piece(i).SetPriority(torrent.PiecePriorityNone)
		}
	}
}

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
