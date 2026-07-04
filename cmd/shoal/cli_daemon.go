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

// runDaemon runs the shared engine and serves it on the unix socket (foreground).
func runDaemon(args []string, out io.Writer) int {
	if runtime.GOOS == "windows" {
		fmt.Fprintln(os.Stderr, "the shoal daemon is not yet supported on Windows")
		return 1
	}
	sock := daemon.SocketPath()
	if daemonRunning(sock) {
		fmt.Fprintln(os.Stderr, "shoal daemon already running at", sock)
		return 1
	}
	_ = os.Remove(sock) // clear a stale socket file
	if err := os.MkdirAll(filepath.Dir(sock), 0o700); err != nil {
		fmt.Fprintln(os.Stderr, "shoal daemon:", err)
		return 1
	}

	cfg := config.Load()
	eng, err := engine.NewAnacrolix(engine.Config{
		DataDir:    cfg.DataDir,
		ListenPort: cfg.ListenPort,
		MaxPeers:   cfg.MaxPeers,
		Seed:       cfg.Seed,
		SeedRatio:  cfg.SeedRatio,
		QueuePath:  queue.DefaultPath(),
	})
	if err != nil {
		fmt.Fprintln(os.Stderr, "shoal daemon: engine:", err)
		return 1
	}
	defer eng.Close()

	l, err := net.Listen("unix", sock)
	if err != nil {
		fmt.Fprintln(os.Stderr, "shoal daemon: listen:", err)
		return 1
	}
	fmt.Fprintln(out, "shoal daemon listening on", sock)

	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-sig
		l.Close()
	}()

	hist := history.Load()
	stop := make(chan struct{})
	done := make(chan struct{})
	go func() {
		recordCompletions(eng, &hist, time.Second, stop)
		close(done)
	}()

	_ = daemon.Serve(l, eng) // returns when the listener closes
	close(stop)
	<-done // wait for recordCompletions to exit before eng.Close() runs (deferred above)
	_ = os.Remove(sock)
	return 0
}
