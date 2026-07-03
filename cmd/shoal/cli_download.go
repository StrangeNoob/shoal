package main

import (
	"crypto/sha1"
	"encoding/hex"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/StrangeNoob/shoal/internal/engine"
	"github.com/StrangeNoob/shoal/internal/history"
	"github.com/StrangeNoob/shoal/internal/source"
)

var (
	hex40RE = regexp.MustCompile(`^[0-9a-fA-F]{40}$`)
	hex8RE  = regexp.MustCompile(`^[0-9a-fA-F]{8}$`)
)

// dlTarget is a resolved download: a magnet or a .torrent URL, plus the handle
// used as the state-file id.
type dlTarget struct {
	Magnet string // set for magnet / hash / short-id
	URL    string // set for a .torrent URL
	Handle string // 8-hex state-file id
}

// resolveTarget turns a user argument into a download target. lookup resolves a
// short id to a magnet; pass nil to disable short-id resolution.
func resolveTarget(arg string, lookup func(id string) (string, bool)) (dlTarget, error) {
	s := strings.TrimSpace(arg)
	switch {
	case strings.HasPrefix(strings.ToLower(s), "magnet:"):
		ih := source.ParseMagnetInfoHash(s)
		if ih == "" {
			return dlTarget{}, fmt.Errorf("magnet has no infohash: %s", s)
		}
		return dlTarget{Magnet: s, Handle: ih[:8]}, nil
	case strings.HasPrefix(s, "http://"), strings.HasPrefix(s, "https://"):
		sum := sha1.Sum([]byte(s))
		return dlTarget{URL: s, Handle: hex.EncodeToString(sum[:])[:8]}, nil
	case hex40RE.MatchString(s):
		ih := strings.ToLower(s)
		return dlTarget{Magnet: "magnet:?xt=urn:btih:" + ih, Handle: ih[:8]}, nil
	case hex8RE.MatchString(s):
		id := strings.ToLower(s)
		if lookup != nil {
			if magnet, ok := lookup(id); ok {
				return dlTarget{Magnet: magnet, Handle: id}, nil
			}
		}
		return dlTarget{}, fmt.Errorf("no recent search contains id %s; run `shoal search` first", id)
	default:
		return dlTarget{}, fmt.Errorf("unrecognized download target: %s", s)
	}
}

func firstStatus(ss []engine.Status) *engine.Status {
	if len(ss) == 0 {
		return nil
	}
	return &ss[0]
}

// stepWorker performs one poll: refreshes a from the engine and writes the state
// file. Returns true once the (single) torrent is done.
func stepWorker(eng engine.Engine, base string, a *Active) (done bool) {
	st := firstStatus(eng.Statuses())
	if st == nil {
		return false
	}
	a.InfoHash = st.InfoHash
	if st.Name != "" {
		a.Name = st.Name
	}
	a.Total = st.TotalBytes
	a.Completed = st.CompletedBytes
	a.Peers = st.Peers
	a.Seeding = st.Seeding
	a.Path = st.Path
	a.Done = st.Done
	a.UpdatedAt = time.Now()
	_ = writeActive(base, *a)
	return st.Done
}

// runWorker polls until the download completes, then records it to history.
func runWorker(eng engine.Engine, base string, a Active, hist *history.Store, interval time.Duration) {
	_ = writeActive(base, a) // show up in `status` immediately
	tick := time.NewTicker(interval)
	defer tick.Stop()
	for range tick.C {
		if stepWorker(eng, base, &a) {
			hist.Append(history.Entry{
				InfoHash:    a.InfoHash,
				Name:        a.Name,
				Size:        a.Total,
				CompletedAt: time.Now(),
				Path:        a.Path,
			})
			return
		}
	}
}
