// Command shoal downloads a .torrent file to disk using the hand-written
// BitTorrent core in this repo (no third-party libraries).
//
// Usage:
//
//	shoal <file.torrent> [output-dir]
//
// It parses the torrent, asks the tracker for peers, downloads and verifies
// every piece, then writes the result (a single file, or a directory for
// multi-file torrents) into output-dir (default: current directory).
package main

import (
	crand "crypto/rand"
	"fmt"
	"os"
	"path/filepath"

	"github.com/StrangeNoob/shoal/download"
	"github.com/StrangeNoob/shoal/metainfo"
	"github.com/StrangeNoob/shoal/tracker"
)

const listenPort = 6881 // advertised to the tracker; we don't actually accept incoming peers yet

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintln(os.Stderr, "usage: shoal <file.torrent> [output-dir]")
		os.Exit(2)
	}
	torrentPath := os.Args[1]
	outDir := "."
	if len(os.Args) >= 3 {
		outDir = os.Args[2]
	}

	raw, err := os.ReadFile(torrentPath)
	must(err)

	meta, err := metainfo.Parse(raw)
	must(err)

	fmt.Printf("Name:      %s\n", meta.Info.Name)
	fmt.Printf("InfoHash:  %x\n", meta.InfoHash)
	fmt.Printf("Size:      %s\n", humanBytes(meta.TotalLength()))
	fmt.Printf("Pieces:    %d × %s\n", meta.NumPieces(), humanBytes(meta.Info.PieceLength))

	peerID := newPeerID()

	fmt.Println("\nAnnouncing to tracker…")
	resp, err := tracker.Announce(meta, peerID, listenPort)
	must(err)
	fmt.Printf("Got %d peer(s).\n", len(resp.Peers))
	if len(resp.Peers) == 0 {
		fmt.Fprintln(os.Stderr, "no peers available — nothing to download from")
		os.Exit(1)
	}

	fmt.Println("\nDownloading…")
	t := &download.Torrent{
		Meta:     meta,
		PeerID:   peerID,
		InfoHash: meta.InfoHash,
		Peers:    resp.Peers,
	}
	data, err := t.Download()
	must(err)

	must(writeOutput(meta, outDir, data))
	fmt.Printf("Done → %s\n", filepath.Join(outDir, meta.Info.Name))
}

// newPeerID returns a random 20-byte peer id with the conventional
// "-<client><version>-" prefix (here: shoal 0.0.1).
func newPeerID() [20]byte {
	var id [20]byte
	_, err := crand.Read(id[:])
	must(err)
	copy(id[:8], []byte("-SH0001-"))
	return id
}

// writeOutput lays the assembled byte buffer onto disk: one file for a
// single-file torrent, or a directory tree for a multi-file one.
func writeOutput(m *metainfo.MetaInfo, outDir string, data []byte) error {
	if len(m.Info.Files) == 0 {
		path := filepath.Join(outDir, m.Info.Name)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			return err
		}
		return os.WriteFile(path, data, 0o644)
	}

	var offset int64
	for _, f := range m.Info.Files {
		parts := append([]string{outDir, m.Info.Name}, f.Path...)
		path := filepath.Join(parts...)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			return err
		}
		end := offset + f.Length
		if end > int64(len(data)) {
			return fmt.Errorf("writeOutput: file %q runs past downloaded data", path)
		}
		if err := os.WriteFile(path, data[offset:end], 0o644); err != nil {
			return err
		}
		offset = end
	}
	return nil
}

func humanBytes(n int64) string {
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}
	div, exp := int64(unit), 0
	for x := n / unit; x >= unit; x /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %ciB", float64(n)/float64(div), "KMGTPE"[exp])
}

func must(err error) {
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}
