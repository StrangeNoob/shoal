package engine

import (
	"sort"
	"time"

	"github.com/anacrolix/torrent/metainfo"
)

// queueItem is the scheduler's view of one torrent.
type queueItem struct {
	Hash       metainfo.Hash
	AddedAt    time.Time
	Done       bool
	UserPaused bool // paused by the user (not the scheduler)
	Queued     bool // currently held by the scheduler
}

// planQueue decides which torrents to release (let download) and which to hold
// (queue) so that at most maxActive incomplete, un-paused torrents download at
// once. Active slots go to the oldest torrents first (FIFO by AddedAt). It is
// pure so the promotion/demotion logic is unit-testable without a real client.
//
// maxActive <= 0 means unlimited: every scheduler-held torrent is released.
func planQueue(items []queueItem, maxActive int) (release, hold []metainfo.Hash) {
	// Torrents the scheduler should never hold: done or user-paused. If one is
	// currently queued, release it so it isn't stuck.
	var candidates []queueItem
	for _, it := range items {
		if it.Done || it.UserPaused {
			if it.Queued {
				release = append(release, it.Hash)
			}
			continue
		}
		candidates = append(candidates, it)
	}
	if maxActive <= 0 {
		for _, it := range candidates {
			if it.Queued {
				release = append(release, it.Hash)
			}
		}
		return release, hold
	}
	// Oldest first: the earliest-added torrents keep the active slots.
	sort.SliceStable(candidates, func(i, j int) bool {
		return candidates[i].AddedAt.Before(candidates[j].AddedAt)
	})
	for i, it := range candidates {
		wantActive := i < maxActive
		switch {
		case wantActive && it.Queued:
			release = append(release, it.Hash)
		case !wantActive && !it.Queued:
			hold = append(hold, it.Hash)
		}
	}
	return release, hold
}
