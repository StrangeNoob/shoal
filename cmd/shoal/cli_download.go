package main

import (
	"crypto/sha1"
	"encoding/hex"
	"flag"
	"fmt"
	"io"
	"net/url"
	"os"
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
	case strings.HasPrefix(strings.ToLower(s), "http://"), strings.HasPrefix(strings.ToLower(s), "https://"):
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

func runDownload(args []string, out io.Writer) int {
	fs := flag.NewFlagSet("download", flag.ContinueOnError)
	outDir := fs.String("out", "", "(deprecated) downloads use the configured folder")
	positionals, err := parseArgs(fs, args)
	if err != nil {
		return 2
	}
	var arg string
	if len(positionals) > 0 {
		arg = positionals[0]
	}
	if arg == "" {
		fmt.Fprintln(os.Stderr, "usage: shoal download <magnet|url|infohash|id>")
		return 2
	}
	if *outDir != "" {
		fmt.Fprintln(os.Stderr, "note: downloads use the configured folder (change it in Settings or config.json)")
	}

	tgt, err := resolveTarget(arg, func(sid string) (string, bool) {
		e, ok := lookupCache(configDir(), sid)
		return e.Magnet, ok
	})
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}

	eng, err := ensureDaemon()
	if err != nil {
		fmt.Fprintln(os.Stderr, "shoal download:", err)
		return 1
	}
	defer eng.Close()

	if tgt.Magnet != "" {
		err = eng.AddMagnet(tgt.Magnet)
	} else {
		err = eng.AddTorrentURL(tgt.URL, "")
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "shoal download:", err)
		return 1
	}
	if tgt.Magnet != "" && tgt.Handle != "" {
		fmt.Fprintf(out, "started: %s (%s)\n", displayName(tgt), tgt.Handle)
	} else {
		fmt.Fprintf(out, "started: %s\n", displayName(tgt))
	}
	return 0
}

// displayName is a friendly label for the "started:" line.
func displayName(t dlTarget) string {
	if t.URL != "" {
		return t.URL
	}
	if u, err := url.Parse(t.Magnet); err == nil {
		if dn := u.Query().Get("dn"); dn != "" {
			return dn
		}
	}
	return "download"
}
