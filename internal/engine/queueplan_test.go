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

func TestMoveInOrderAndRemove(t *testing.T) {
	order := []metainfo.Hash{h(1), h(2), h(3)}

	// Move h3 earlier by one → swaps with h2.
	got := moveInOrder(order, h(3), -1)
	if got[1][0] != 3 || got[2][0] != 2 {
		t.Fatalf("move earlier: %v", hashesSeq(got))
	}
	// Multi-step move shifts, preserving the order of the elements in between:
	// [1,2,3] move h1 by +2 → [2,3,1].
	if got = moveInOrder(order, h(1), +2); got[0][0] != 2 || got[1][0] != 3 || got[2][0] != 1 {
		t.Fatalf("multi-step move: %v", hashesSeq(got))
	}
	// Clamp at the front.
	got = moveInOrder(order, h(1), -1)
	if got[0][0] != 1 {
		t.Fatalf("clamp front changed order: %v", hashesSeq(got))
	}
	// Move later past the end clamps at the back.
	got = moveInOrder(order, h(1), +5)
	if got[2][0] != 1 {
		t.Fatalf("move to back: %v", hashesSeq(got))
	}
	// A missing hash is a no-op.
	if got = moveInOrder(order, h(9), -1); len(got) != 3 || got[0][0] != 1 {
		t.Fatalf("missing hash changed order: %v", hashesSeq(got))
	}
	// removeHash drops the entry.
	if got = removeHash(order, h(2)); len(got) != 2 || got[0][0] != 1 || got[1][0] != 3 {
		t.Fatalf("removeHash: %v", hashesSeq(got))
	}
}

func hashesSeq(hs []metainfo.Hash) []byte {
	out := make([]byte, len(hs))
	for i, x := range hs {
		out[i] = x[0]
	}
	return out
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

	// A user-paused torrent that is still scheduler-queued is put in release so
	// reconcileQueue clears the hold bookkeeping — but reconcile must NOT resume
	// it (guarded by a.paused there); it stays paused.
	items = []queueItem{
		{Hash: h(1), AddedAt: at(0)},
		{Hash: h(2), AddedAt: at(1), UserPaused: true, Queued: true},
	}
	release, hold = planQueue(items, 2)
	if len(hold) != 0 || !hashesOf(release)[2] {
		t.Fatalf("paused+queued: release=%v hold=%v, want release h2 (clear hold)", release, hold)
	}

	// User priority overrides add-order: h3 (newest) prioritized to the front
	// keeps its active slot; the older h2 is held instead.
	items = []queueItem{
		{Hash: h(1), AddedAt: at(0), Order: 1},
		{Hash: h(2), AddedAt: at(1), Order: 2},
		{Hash: h(3), AddedAt: at(2), Order: 0}, // moved to front
	}
	release, hold = planQueue(items, 2)
	if len(release) != 0 || len(hold) != 1 || !hashesOf(hold)[2] {
		t.Fatalf("priority: release=%v hold=%v, want hold h2", release, hold)
	}

	// maxActive 0 (unlimited) releases everything held.
	items = []queueItem{{Hash: h(3), AddedAt: at(2), Queued: true}}
	release, hold = planQueue(items, 0)
	if len(hold) != 0 || !hashesOf(release)[3] {
		t.Fatalf("unlimited: release=%v hold=%v, want release h3", release, hold)
	}
}
