// cmd/shoal/cli_daemon.go
package main

import (
	"fmt"
	"io"
	"net"
	"os"
	"os/signal"
	"path/filepath"
	"runtime"
	"syscall"

	"github.com/StrangeNoob/shoal/internal/config"
	"github.com/StrangeNoob/shoal/internal/daemon"
	"github.com/StrangeNoob/shoal/internal/engine"
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

	_ = daemon.Serve(l, eng) // returns when the listener closes
	_ = os.Remove(sock)
	return 0
}
