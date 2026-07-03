package main

import (
	"crypto/sha1"
	"encoding/hex"
	"flag"
	"fmt"
	"io"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/StrangeNoob/shoal/internal/config"
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
			// This worker may have started hours ago; reload just before appending
			// so a concurrent worker/TUI completion isn't clobbered by Save (which
			// overwrites the whole file). Shrinks the race window to milliseconds.
			fresh := history.LoadFrom(hist.Path)
			fresh.Append(history.Entry{
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

// runDownload is the `download` entrypoint. The parent resolves the target and
// spawns a detached worker; --worker runs the actual download loop.
func runDownload(args []string, out io.Writer) int {
	fs := flag.NewFlagSet("download", flag.ContinueOnError)
	worker := fs.Bool("worker", false, "") // hidden: marks the detached child
	id := fs.String("id", "", "")          // hidden: worker handle
	outDir := fs.String("out", "", "download directory")
	positionals, err := parseArgs(fs, args)
	if err != nil {
		return 2
	}
	var arg string
	if len(positionals) > 0 {
		arg = positionals[0]
	}
	if arg == "" {
		fmt.Fprintln(os.Stderr, "usage: shoal download <magnet|url|infohash|id> [--out dir]")
		return 2
	}

	cfg := config.Load()
	base := configDir()
	dir := cfg.DataDir
	if *outDir != "" {
		dir = *outDir
	}

	if *worker {
		return downloadWorker(*id, arg, dir, base)
	}

	tgt, err := resolveTarget(arg, func(sid string) (string, bool) {
		e, ok := lookupCache(base, sid)
		return e.Magnet, ok
	})
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	target := tgt.Magnet
	if target == "" {
		target = tgt.URL
	}
	if err := spawnWorker(tgt.Handle, target, dir, base); err != nil {
		fmt.Fprintln(os.Stderr, "failed to start download:", err)
		return 1
	}
	fmt.Fprintf(out, "started: %s (%s)\n", displayName(tgt), tgt.Handle)
	return 0
}

// downloadWorker is the detached child: build the engine, add the one torrent,
// and run the progress loop to completion.
func downloadWorker(id, target, dir, base string) int {
	cfg := config.Load()
	eng, err := engine.NewAnacrolix(engine.Config{
		DataDir:    dir,
		ListenPort: -1, // OS-assigned ephemeral port: never collide with the TUI (6881) or another worker
		MaxPeers:   cfg.MaxPeers,
		Seed:       false, // one-shot: stop at 100%, don't seed forever
		SeedRatio:  0,
		QueuePath:  "", // never touch the TUI's persistent queue
	})
	a := Active{ID: id, Out: dir, Pid: os.Getpid(), UpdatedAt: time.Now()}
	if err != nil {
		a.Error = err.Error()
		_ = writeActive(base, a)
		return 1
	}
	defer eng.Close()

	if strings.HasPrefix(strings.ToLower(target), "magnet:") {
		err = eng.AddMagnet(target)
	} else {
		err = eng.AddTorrentURL(target, "")
	}
	if err != nil {
		a.Error = err.Error()
		_ = writeActive(base, a)
		return 1
	}

	hist := history.Load()
	runWorker(eng, base, a, &hist, time.Second)
	return 0
}

// spawnWorker launches a detached copy of this binary to run the download.
func spawnWorker(handle, target, dir, base string) error {
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Join(base, "logs"), 0o700); err != nil {
		return err
	}
	logf, err := os.OpenFile(filepath.Join(base, "logs", handle+".log"),
		os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return err
	}
	defer logf.Close()
	cmd := exec.Command(exe, "download", "--worker", "--id", handle, "--out", dir, target)
	cmd.Stdout = logf
	cmd.Stderr = logf
	cmd.SysProcAttr = detachSysProcAttr()
	return cmd.Start() // do not Wait: the child outlives us
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
