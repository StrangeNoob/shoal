package engine

import (
	"testing"
	"time"

	"github.com/anacrolix/torrent/metainfo"
)

func h(b byte) metainfo.Hash {
	var x metainfo.Hash
	x[0] = b
	return x
}

func hashesOf(hs []metainfo.Hash) map[byte]bool {
	m := map[byte]bool{}
	for _, x := range hs {
		m[x[0]] = true
	}
	return m
}

func TestPlanQueue(t *testing.T) {
	base := time.Unix(1000, 0)
	at := func(sec int) time.Time { return base.Add(time.Duration(sec) * time.Second) }

	// Three incomplete downloads, limit 2 → the newest (added last) is held.
	items := []queueItem{
		{Hash: h(1), AddedAt: at(0)},
		{Hash: h(2), AddedAt: at(1)},
		{Hash: h(3), AddedAt: at(2)},
	}
	release, hold := planQueue(items, 2)
	if len(release) != 0 || len(hold) != 1 || !hashesOf(hold)[3] {
		t.Fatalf("limit 2: release=%v hold=%v, want hold h3", release, hold)
	}

	// A slot frees (h1 done) → the queued h3 is released.
	items = []queueItem{
		{Hash: h(1), AddedAt: at(0), Done: true},
		{Hash: h(2), AddedAt: at(1)},
		{Hash: h(3), AddedAt: at(2), Queued: true},
	}
	release, hold = planQueue(items, 2)
	if len(hold) != 0 || !hashesOf(release)[3] {
		t.Fatalf("slot freed: release=%v hold=%v, want release h3", release, hold)
	}

	// User-paused torrents don't consume a slot; a queued one behind them promotes.
	items = []queueItem{
		{Hash: h(1), AddedAt: at(0), UserPaused: true},
		{Hash: h(2), AddedAt: at(1)},
		{Hash: h(3), AddedAt: at(2), Queued: true},
	}
	release, _ = planQueue(items, 2)
	if !hashesOf(release)[3] {
		t.Fatalf("paused frees a slot: release=%v, want release h3", release)
	}

	// maxActive 0 (unlimited) releases everything held.
	items = []queueItem{{Hash: h(3), AddedAt: at(2), Queued: true}}
	release, hold = planQueue(items, 0)
	if len(hold) != 0 || !hashesOf(release)[3] {
		t.Fatalf("unlimited: release=%v hold=%v, want release h3", release, hold)
	}
}
