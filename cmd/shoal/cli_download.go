package main

import (
	"crypto/sha1"
	"encoding/hex"
	"fmt"
	"regexp"
	"strings"

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
