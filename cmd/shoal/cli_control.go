package main

import (
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/StrangeNoob/shoal/internal/config"
	"github.com/StrangeNoob/shoal/internal/daemon"
	"github.com/StrangeNoob/shoal/internal/engine"
	"github.com/StrangeNoob/shoal/internal/history"
	"github.com/StrangeNoob/shoal/internal/opener"
)

// withDaemon dials the daemon and resolves idPrefix to exactly one live torrent,
// then runs fn. No daemon → "no active downloads" (exit 0); 0/2+ matches → exit 1.
func withDaemon(idPrefix string, out io.Writer, fn func(c *daemon.Client, s engine.Status) error) int {
	c, err := daemon.Dial(daemon.SocketPath())
	if err != nil {
		fmt.Fprintln(out, "no active downloads")
		return 0
	}
	defer c.Close()
	_ = c.SetDeadline(time.Now().Add(5 * time.Second))
	s, err := resolveOne(c, idPrefix)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	if err := fn(c, s); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	return 0
}

// resolveOne returns the unique live torrent whose infohash starts with idPrefix.
func resolveOne(c *daemon.Client, idPrefix string) (engine.Status, error) {
	prefix := strings.ToLower(idPrefix)
	var matches []engine.Status
	for _, s := range c.Statuses() {
		if strings.HasPrefix(strings.ToLower(s.InfoHash), prefix) {
			matches = append(matches, s)
		}
	}
	switch len(matches) {
	case 0:
		return engine.Status{}, fmt.Errorf("no such download: %s", idPrefix)
	case 1:
		return matches[0], nil
	default:
		return engine.Status{}, fmt.Errorf("ambiguous id %q matches %d downloads", idPrefix, len(matches))
	}
}

func firstArg(name string, args []string, out io.Writer) (string, bool) {
	positionals, err := parseArgs(flag.NewFlagSet(name, flag.ContinueOnError), args)
	if err != nil {
		return "", false
	}
	if len(positionals) == 0 || positionals[0] == "" {
		fmt.Fprintf(os.Stderr, "usage: shoal %s <id>\n", name)
		return "", false
	}
	return positionals[0], true
}

func runPause(args []string, out io.Writer) int {
	id, ok := firstArg("pause", args, out)
	if !ok {
		return 2
	}
	return withDaemon(id, out, func(c *daemon.Client, s engine.Status) error {
		if err := c.Pause(s.InfoHash); err != nil {
			return err
		}
		fmt.Fprintf(out, "paused: %s\n", s.Name)
		return nil
	})
}

func runResume(args []string, out io.Writer) int {
	id, ok := firstArg("resume", args, out)
	if !ok {
		return 2
	}
	return withDaemon(id, out, func(c *daemon.Client, s engine.Status) error {
		if err := c.Resume(s.InfoHash); err != nil {
			return err
		}
		fmt.Fprintf(out, "resumed: %s\n", s.Name)
		return nil
	})
}

func runRemove(args []string, out io.Writer) int {
	fs := flag.NewFlagSet("remove", flag.ContinueOnError)
	del := fs.Bool("delete-files", false, "also delete the downloaded files")
	positionals, err := parseArgs(fs, args)
	if err != nil {
		return 2
	}
	if len(positionals) == 0 || positionals[0] == "" {
		fmt.Fprintln(os.Stderr, "usage: shoal remove <id> [--delete-files]")
		return 2
	}
	return withDaemon(positionals[0], out, func(c *daemon.Client, s engine.Status) error {
		if err := c.Remove(s.InfoHash, *del); err != nil {
			return err
		}
		verb := "removed"
		if *del {
			verb = "removed + deleted files for"
		}
		fmt.Fprintf(out, "%s: %s\n", verb, s.Name)
		return nil
	})
}

func runOpen(args []string, out io.Writer) int {
	id, ok := firstArg("open", args, out)
	if !ok {
		return 2
	}
	path, err := resolveOpenPath(id)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	if err := opener.Open(path); err != nil {
		fmt.Fprintln(out, path) // headless / no opener: print the path
	}
	return 0
}

// underDataDir reports whether path is inside dir (allowing dir itself),
// rejecting traversal escapes — the same discipline as engine.RemoveUnderDir.
func underDataDir(dir, path string) bool {
	rel, err := filepath.Rel(filepath.Clean(dir), filepath.Clean(path))
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

// resolveOpenPath finds the unique folder to open for idPrefix, across live
// torrents (preferred) and history entries, deduped by infohash. Every candidate
// must resolve under the configured data dir — a history entry with a
// traversal name must never point `open` outside it. 0 matches → an error;
// 2+ distinct torrents → ambiguous.
func resolveOpenPath(idPrefix string) (string, error) {
	prefix := strings.ToLower(idPrefix)
	dataDir := config.Load().DataDir
	paths := map[string]string{} // infohash → best path
	if c, err := daemon.Dial(daemon.SocketPath()); err == nil {
		defer c.Close()
		_ = c.SetDeadline(time.Now().Add(5 * time.Second))
		for _, s := range c.Statuses() {
			if strings.HasPrefix(strings.ToLower(s.InfoHash), prefix) && s.Path != "" && underDataDir(dataDir, s.Path) {
				paths[s.InfoHash] = s.Path
			}
		}
	}
	for _, e := range history.Load().Entries {
		if !strings.HasPrefix(strings.ToLower(e.InfoHash), prefix) {
			continue
		}
		if _, ok := paths[e.InfoHash]; ok {
			continue // already have a (live) path for this torrent
		}
		if e.Path != "" && underDataDir(dataDir, e.Path) && pathExists(e.Path) {
			paths[e.InfoHash] = e.Path
		} else if e.Name != "" {
			if guess := filepath.Join(dataDir, e.Name); underDataDir(dataDir, guess) && pathExists(guess) {
				paths[e.InfoHash] = guess
			}
		}
	}
	switch len(paths) {
	case 0:
		return "", fmt.Errorf("no such download: %s", idPrefix)
	case 1:
		for _, p := range paths {
			return p, nil
		}
	}
	return "", fmt.Errorf("ambiguous id %q matches %d downloads", idPrefix, len(paths))
}

func pathExists(p string) bool {
	_, err := os.Stat(p)
	return err == nil
}
