// cmd/shoal/cli_daemon.go
package main

import (
	"context"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"runtime"
	"syscall"
	"time"

	"github.com/StrangeNoob/shoal/internal/config"
	"github.com/StrangeNoob/shoal/internal/daemon"
	"github.com/StrangeNoob/shoal/internal/engine"
	"github.com/StrangeNoob/shoal/internal/history"
	"github.com/StrangeNoob/shoal/internal/queue"
)

// daemonRunning reports whether a daemon is already accepting connections at sock.
func daemonRunning(sock string) bool {
	c, err := daemon.Dial(sock)
	if err != nil {
		return false
	}
	c.Close()
	return true
}

// ensureDaemon returns a client connected to the shared daemon, auto-starting a
// detached `shoal daemon` if none is running.
func ensureDaemon() (*daemon.Client, error) {
	if runtime.GOOS == "windows" {
		return nil, fmt.Errorf("the shoal daemon is not yet supported on Windows")
	}
	sock := daemon.SocketPath()
	if c, err := daemon.Dial(sock); err == nil {
		return c, nil
	}
	exe, err := os.Executable()
	if err != nil {
		return nil, err
	}
	logDir := filepath.Join(configDir(), "logs")
	if err := os.MkdirAll(logDir, 0o700); err != nil {
		return nil, err
	}
	logf, err := os.OpenFile(filepath.Join(logDir, "daemon.log"), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return nil, err
	}
	defer logf.Close()
	cmd := exec.CommandContext(context.Background(), exe, "daemon")
	cmd.Stdout = logf
	cmd.Stderr = logf
	cmd.SysProcAttr = detachSysProcAttr()
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	for i := 0; i < 25; i++ { // poll the socket for ~5s
		time.Sleep(200 * time.Millisecond)
		if c, err := daemon.Dial(sock); err == nil {
			return c, nil
		}
	}
	return nil, fmt.Errorf("could not start shoal daemon (see %s)", filepath.Join(logDir, "daemon.log"))
}

// recordCompletions polls the engine and appends newly-completed torrents to
// history, so CLI downloads land in the shared history like the TUI's do.
func recordCompletions(eng engine.Engine, hist *history.Store, interval time.Duration, stop <-chan struct{}) {
	recorded := map[string]bool{}
	tick := time.NewTicker(interval)
	defer tick.Stop()
	for {
		select {
		case <-stop:
			return
		case <-tick.C:
			for _, s := range eng.Statuses() {
				if s.Done && !recorded[s.InfoHash] {
					recorded[s.InfoHash] = true
					hist.Append(history.Entry{
						InfoHash:    s.InfoHash,
						Name:        s.Name,
						Size:        s.TotalBytes,
						CompletedAt: time.Now(),
						Path:        s.Path,
					})
				}
			}
		}
	}
}

// listenDaemon binds the daemon socket, refusing to start if a live daemon
// already answers. A leftover socket file (from a crashed daemon) is reclaimed
// under a lock file so two cold-starting daemons can't both remove-and-rebind it.
func listenDaemon(sock string) (net.Listener, error) {
	if l, err := net.Listen("unix", sock); err == nil {
		return l, nil
	}
	if daemonRunning(sock) {
		return nil, fmt.Errorf("already running at %s", sock)
	}
	lock, err := flockExclusive(sock + ".lock")
	if err != nil {
		return nil, err
	}
	defer lock.Close()
	if daemonRunning(sock) { // re-check under the lock: another starter may have won
		return nil, fmt.Errorf("already running at %s", sock)
	}
	_ = os.Remove(sock) // stale socket file — reclaim it
	return net.Listen("unix", sock)
}

// runDaemonCmd dispatches `daemon` (run), `daemon stop`, and `daemon status`.
func runDaemonCmd(args []string, out io.Writer) int {
	if len(args) > 0 {
		switch args[0] {
		case "stop":
			return runDaemonStop(out)
		case "status":
			return runDaemonStatus(out)
		default:
			fmt.Fprintf(os.Stderr, "shoal daemon: unknown subcommand %q (use stop|status, or no argument to run the daemon)\n", args[0])
			return 2
		}
	}
	return runDaemon(args, out)
}

func runDaemonStop(out io.Writer) int {
	c, err := daemon.Dial(daemon.SocketPath())
	if err != nil {
		fmt.Fprintln(out, "daemon not running")
		return 0
	}
	defer c.Close()
	_ = c.SetDeadline(time.Now().Add(5 * time.Second))
	if err := c.Shutdown(); err != nil {
		fmt.Fprintln(os.Stderr, "shoal daemon stop:", err)
		return 1
	}
	for i := 0; i < 25; i++ { // poll ~5s for it to exit
		time.Sleep(200 * time.Millisecond)
		if !daemonRunning(daemon.SocketPath()) {
			fmt.Fprintln(out, "daemon stopped")
			return 0
		}
	}
	fmt.Fprintln(out, "daemon stop requested (still shutting down)")
	return 0
}

func runDaemonStatus(out io.Writer) int {
	c, err := daemon.Dial(daemon.SocketPath())
	if err != nil {
		fmt.Fprintln(out, "daemon not running")
		return 0
	}
	defer c.Close()
	_ = c.SetDeadline(time.Now().Add(5 * time.Second))
	st, err := c.Status()
	if err != nil {
		fmt.Fprintln(os.Stderr, "shoal daemon status:", err)
		return 1
	}
	fmt.Fprintf(out, "running  ·  up %s  ·  %d torrents (%d downloading, %d seeding)  ·  pid %d\n",
		st.Uptime.Round(time.Second), st.Torrents, st.Downloading, st.Seeding, st.Pid)
	return 0
}

// runDaemon runs the shared engine and serves it on the unix socket (foreground).
func runDaemon(args []string, out io.Writer) int {
	if runtime.GOOS == "windows" {
		fmt.Fprintln(os.Stderr, "the shoal daemon is not yet supported on Windows")
		return 1
	}
	cfg := config.Load()
	sock := daemon.SocketPath()
	if err := os.MkdirAll(filepath.Dir(sock), 0o700); err != nil {
		fmt.Fprintln(os.Stderr, "shoal daemon:", err)
		return 1
	}
	l, err := listenDaemon(sock) // bind first — a second daemon fails here
	if err != nil {
		fmt.Fprintln(os.Stderr, "shoal daemon:", err)
		return 1
	}

	eng, err := engine.NewAnacrolix(engine.Config{
		DataDir:    cfg.DataDir,
		ListenPort: cfg.ListenPort,
		MaxPeers:   cfg.MaxPeers,
		Seed:       cfg.Seed,
		SeedRatio:  cfg.SeedRatio,
		QueuePath:  queue.DefaultPath(),
	})
	if err != nil {
		l.Close()
		_ = os.Remove(sock)
		fmt.Fprintln(os.Stderr, "shoal daemon: engine:", err)
		return 1
	}
	defer eng.Close()

	srv := daemon.NewServer(eng, time.Now(), time.Duration(cfg.DaemonIdleMinutes)*time.Minute)
	fmt.Fprintln(out, "shoal daemon listening on", sock)

	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGINT, syscall.SIGTERM)
	go func() { <-sig; srv.Shutdown() }()

	hist := history.Load()
	stop := make(chan struct{})
	done := make(chan struct{})
	go func() {
		recordCompletions(eng, &hist, time.Second, stop)
		close(done)
	}()

	_ = srv.Serve(l) // returns when the listener closes (stop/idle/signal)
	close(stop)
	<-done // wait for recordCompletions to exit before eng.Close() runs (deferred)
	_ = os.Remove(sock)
	return 0
}
