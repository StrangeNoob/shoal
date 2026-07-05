package main

import (
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

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
	path := resolveOpenPath(id)
	if path == "" {
		fmt.Fprintln(os.Stderr, "no such download:", id)
		return 1
	}
	if err := opener.Open(path); err != nil {
		fmt.Fprintln(out, path) // headless / no opener: print the path
	}
	return 0
}

// resolveOpenPath finds a folder to open for idPrefix: a live torrent's path,
// else a history entry's path, else <dataDir>/<name> if it exists.
func resolveOpenPath(idPrefix string) string {
	prefix := strings.ToLower(idPrefix)
	if c, err := daemon.Dial(daemon.SocketPath()); err == nil {
		defer c.Close()
		for _, s := range c.Statuses() {
			if strings.HasPrefix(strings.ToLower(s.InfoHash), prefix) && s.Path != "" {
				return s.Path
			}
		}
	}
	dataDir := config.Load().DataDir
	for _, e := range history.Load().Entries {
		if !strings.HasPrefix(strings.ToLower(e.InfoHash), prefix) {
			continue
		}
		if e.Path != "" && pathExists(e.Path) {
			return e.Path
		}
		if e.Name != "" {
			if guess := filepath.Join(dataDir, e.Name); pathExists(guess) {
				return guess
			}
		}
	}
	return ""
}

func pathExists(p string) bool {
	_, err := os.Stat(p)
	return err == nil
}
