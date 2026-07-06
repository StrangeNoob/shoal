package main

import (
	"crypto/sha1"
	"encoding/hex"
	"errors"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"net/url"
	"os"
	"regexp"
	"strings"
	"time"

	"github.com/anacrolix/torrent/metainfo"

	"github.com/StrangeNoob/shoal/internal/engine"
	"github.com/StrangeNoob/shoal/internal/source"
)

// errNotTorrentFile means the argument isn't a readable file on disk (so it
// should fall through to the "unrecognized target" error), as opposed to a file
// that exists but fails to parse (a real, reportable error).
var errNotTorrentFile = errors.New("not a local file")

// torrentFileMagnet loads a local .torrent and converts it to a magnet URI
// (infohash + trackers + name). Converting client-side lets `download` accept a
// path without a new daemon RPC — the daemon adds it like any other magnet, and
// the persisted queue entry no longer depends on the file staying on disk.
func torrentFileMagnet(path string) (magnetURI, infoHash string, err error) {
	fi, statErr := os.Stat(path)
	if statErr != nil {
		if errors.Is(statErr, fs.ErrNotExist) {
			return "", "", errNotTorrentFile // not a path → fall through to "unrecognized target"
		}
		return "", "", fmt.Errorf("stat %s: %w", path, statErr) // permission/IO error → surface it
	}
	if fi.IsDir() {
		return "", "", errNotTorrentFile
	}
	mi, err := metainfo.LoadFromFile(path)
	if err != nil {
		return "", "", fmt.Errorf("read .torrent %s: %w", path, err)
	}
	var info *metainfo.Info
	if i, e := mi.UnmarshalInfo(); e == nil {
		info = &i // carries the display name into the magnet
	}
	// ponytail: Magnet (v1) not MagnetV2 — the daemon adds via client.AddMagnet,
	// which speaks v1 btih; a v1 magnet is the widely-compatible choice.
	m := mi.Magnet(nil, info)
	return m.String(), mi.HashInfoBytes().HexString(), nil
}

// waitPollInterval is how often `download --wait` re-checks the daemon. A var so
// tests can shorten it.
var waitPollInterval = time.Second

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
		// A path to a local .torrent file.
		if magnetURI, ih, err := torrentFileMagnet(s); err == nil {
			return dlTarget{Magnet: magnetURI, Handle: ih[:8]}, nil
		} else if !errors.Is(err, errNotTorrentFile) {
			return dlTarget{}, err // it is a file, but not a valid .torrent
		}
		return dlTarget{}, fmt.Errorf("unrecognized download target: %s", s)
	}
}

func runDownload(args []string, out io.Writer) int {
	fs := flag.NewFlagSet("download", flag.ContinueOnError)
	outDir := fs.String("out", "", "(deprecated) downloads use the configured folder")
	wait := fs.Bool("wait", false, "block until the download completes")
	files := fs.String("files", "", "download only files matching this glob (comma-separated)")
	positionals, err := parseArgs(fs, args)
	if err != nil {
		return 2
	}
	var arg string
	if len(positionals) > 0 {
		arg = positionals[0]
	}
	if arg == "" {
		fmt.Fprintln(os.Stderr, "usage: shoal download [--wait] <magnet|url|infohash|id|file.torrent>")
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

	// Snapshot existing infohashes so --wait can identify a URL download (whose
	// handle is a URL hash, not the infohash) as the torrent that newly appears.
	// StatusesErr (not Statuses) so a failed snapshot doesn't masquerade as an
	// empty daemon — an empty pre from an error would let --wait lock onto an
	// unrelated pre-existing torrent.
	pre := map[string]bool{}
	preStatuses, preErr := eng.StatusesErr()
	for _, s := range preStatuses {
		pre[strings.ToLower(s.InfoHash)] = true
	}

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
	if globs := splitGlobs(*files); len(globs) > 0 {
		if fh := fullHashFor(tgt); fh != "" {
			if err := eng.SetFileGlobs(fh, globs); err != nil {
				fmt.Fprintln(os.Stderr, "note: could not set file selection:", err)
			}
		} else {
			fmt.Fprintln(os.Stderr, "note: --files isn't supported for URL downloads yet")
		}
	} else if strings.TrimSpace(*files) != "" {
		fmt.Fprintln(os.Stderr, "note: --files pattern is empty; downloading all files")
	}
	if *wait {
		return awaitDone(eng, tgt, pre, preErr == nil, out)
	}
	return 0
}

// waitResolveTries bounds how long awaitDone looks for a URL download's torrent
// to appear before giving up (so it can't hang on an already-present torrent).
const waitResolveTries = 5

// awaitDone blocks until the target torrent reports Done, printing a progress
// line to stderr as it goes. Returns 0 on completion, 1 if the daemon becomes
// unreachable.
//
// It follows one concrete infohash. A magnet/hash/id target is known up front
// (tgt.Handle is its prefix). A URL target's infohash isn't known until the
// daemon fetches the metainfo, so we lock onto the torrent that appears after
// the add (absent from pre). If none appears within waitResolveTries polls the
// torrent was already present and we can't tell which is ours — return with a
// note rather than hang.
//
// ponytail: the URL "first new torrent" heuristic could latch onto an unrelated
// concurrent add; the clean fix is for AddTorrentURL to return the infohash
// (a daemon-protocol change). Locking onto it once, plus the bounded resolve,
// keeps the current design correct for the common single-download case.
func awaitDone(eng interface {
	StatusesErr() ([]engine.Status, error)
}, tgt dlTarget, pre map[string]bool, preOK bool, out io.Writer) int {
	handle := "" // the infohash (prefix) we're following; "" until resolved
	if tgt.Magnet != "" && tgt.Handle != "" {
		handle = tgt.Handle
	}
	// A URL target relies on diffing against pre to spot its torrent; if that
	// baseline snapshot failed, we can't diff safely — don't guess.
	if handle == "" && !preOK {
		fmt.Fprintln(os.Stderr, "note: could not read daemon state; not waiting")
		return 0
	}
	tries := 0
	for {
		statuses, err := eng.StatusesErr()
		if err != nil {
			fmt.Fprintln(os.Stderr, "\nshoal download: daemon unreachable:", err)
			return 1
		}
		if handle == "" { // URL target: lock onto the newly-appeared torrent
			for _, s := range statuses {
				if !pre[strings.ToLower(s.InfoHash)] {
					handle = strings.ToLower(s.InfoHash)
					break
				}
			}
			if handle == "" {
				if tries++; tries >= waitResolveTries {
					fmt.Fprintln(os.Stderr, "note: download already in progress; not waiting")
					return 0
				}
				time.Sleep(waitPollInterval)
				continue
			}
		}
		for _, s := range statuses {
			if !strings.HasPrefix(strings.ToLower(s.InfoHash), handle) {
				continue
			}
			if s.Done {
				fmt.Fprint(os.Stderr, "\r\033[K") // clear the progress line
				fmt.Fprintf(out, "done: %s\n", s.Name)
				return 0
			}
			fmt.Fprintf(os.Stderr, "\r\033[K%5.1f%%  %s/%s  %d peers",
				s.Percent()*100, humanBytes(s.CompletedBytes), humanBytes(s.TotalBytes), s.Peers)
		}
		time.Sleep(waitPollInterval)
	}
}

// fullHashFor derives the full 40-hex infohash for a target that has one
// up front (magnet/hash/id/.torrent-file), or "" for a URL target — a .torrent
// URL's infohash isn't known until the daemon fetches the metainfo, so --files
// isn't supported for it in v1.
func fullHashFor(t dlTarget) string {
	if t.Magnet == "" {
		return ""
	}
	return source.ParseMagnetInfoHash(t.Magnet)
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
