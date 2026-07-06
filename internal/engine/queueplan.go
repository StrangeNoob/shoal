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
	Order      int // user priority (lower = promoted first); ties break by AddedAt
	Done       bool
	UserPaused bool // paused by the user (not the scheduler)
	Queued     bool // currently held by the scheduler
}

// removeHash returns order without hash (a fresh slice).
func removeHash(order []metainfo.Hash, hash metainfo.Hash) []metainfo.Hash {
	out := make([]metainfo.Hash, 0, len(order))
	for _, h := range order {
		if h != hash {
			out = append(out, h)
		}
	}
	return out
}

// moveInOrder returns order with hash shifted by delta positions (delta<0 =
// earlier/higher priority, >0 = later), clamped to the ends. A hash not present,
// or a no-op move, returns order unchanged (a fresh copy).
func moveInOrder(order []metainfo.Hash, hash metainfo.Hash, delta int) []metainfo.Hash {
	out := append([]metainfo.Hash(nil), order...)
	i := -1
	for j, h := range out {
		if h == hash {
			i = j
			break
		}
	}
	if i < 0 {
		return out
	}
	j := i + delta
	if j < 0 {
		j = 0
	}
	if j >= len(out) {
		j = len(out) - 1
	}
	if j == i {
		return out
	}
	out[i], out[j] = out[j], out[i]
	return out
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
	// User priority first (lower Order = promoted sooner), then oldest-added.
	sort.SliceStable(candidates, func(i, j int) bool {
		if candidates[i].Order != candidates[j].Order {
			return candidates[i].Order < candidates[j].Order
		}
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
