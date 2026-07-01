package download

import (
	"bytes"
	"crypto/sha1"
	"encoding/binary"
	"net"
	"strconv"
	"testing"
	"time"

	"github.com/StrangeNoob/shoal/metainfo"
	"github.com/StrangeNoob/shoal/peer"
	"github.com/StrangeNoob/shoal/tracker"
)

// startSeeder runs a loopback peer that completes the handshake, advertises
// bitfield, and serves any block it's asked for out of content. It models a
// healthy seeding peer well enough to exercise the whole worker pool.
func startSeeder(t *testing.T, infoHash [20]byte, content []byte, pieceLength int64, bitfield []byte) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	t.Cleanup(func() { ln.Close() })
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			go serveSeed(conn, infoHash, content, pieceLength, bitfield)
		}
	}()
	return ln.Addr().String()
}

func serveSeed(conn net.Conn, infoHash [20]byte, content []byte, pieceLength int64, bitfield []byte) {
	defer conn.Close()
	if _, err := peer.ReadHandshake(conn); err != nil {
		return
	}
	var pid [20]byte
	conn.Write((&peer.Handshake{InfoHash: infoHash, PeerID: pid}).Serialize())
	conn.Write((&peer.Message{ID: peer.MsgBitfield, Payload: bitfield}).Serialize())
	for {
		m, err := peer.ReadMessage(conn)
		if err != nil {
			return
		}
		if m == nil {
			continue
		}
		switch m.ID {
		case peer.MsgInterested:
			conn.Write((&peer.Message{ID: peer.MsgUnchoke}).Serialize())
		case peer.MsgRequest:
			idx := int(binary.BigEndian.Uint32(m.Payload[0:4]))
			begin := int(binary.BigEndian.Uint32(m.Payload[4:8]))
			length := int(binary.BigEndian.Uint32(m.Payload[8:12]))
			start := int(int64(idx)*pieceLength) + begin
			data := content[start : start+length]
			payload := make([]byte, 8+len(data))
			binary.BigEndian.PutUint32(payload[0:4], uint32(idx))
			binary.BigEndian.PutUint32(payload[4:8], uint32(begin))
			copy(payload[8:], data)
			conn.Write((&peer.Message{ID: peer.MsgPiece, Payload: payload}).Serialize())
		}
	}
}

func addrToPeer(t *testing.T, addr string) tracker.Peer {
	t.Helper()
	host, portStr, err := net.SplitHostPort(addr)
	if err != nil {
		t.Fatalf("split %q: %v", addr, err)
	}
	port, err := strconv.Atoi(portStr)
	if err != nil {
		t.Fatalf("port %q: %v", portStr, err)
	}
	return tracker.Peer{IP: net.ParseIP(host), Port: uint16(port)}
}

// testTorrent builds a Torrent whose piece hashes match content, split into
// ceil(len/pieceLength) pieces.
func testTorrent(infoHash [20]byte, content []byte, pieceLength int64, peers []tracker.Peer) *Torrent {
	m := &metainfo.MetaInfo{InfoHash: infoHash}
	m.Info.Name = "blob.bin"
	m.Info.PieceLength = pieceLength
	m.Info.Length = int64(len(content))
	var pieces []byte
	for off := 0; off < len(content); off += int(pieceLength) {
		end := off + int(pieceLength)
		if end > len(content) {
			end = len(content)
		}
		h := sha1.Sum(content[off:end])
		pieces = append(pieces, h[:]...)
	}
	m.Info.Pieces = pieces
	var pid [20]byte
	return &Torrent{Meta: m, PeerID: pid, InfoHash: infoHash, Peers: peers}
}

func makeContent(n int) []byte {
	b := make([]byte, n)
	for i := range b {
		b[i] = byte(i % 251)
	}
	return b
}

// runDownload runs Download with a hard timeout so a hang fails the test
// instead of stalling the suite (the worker pool has no completion timeout).
func runDownload(t *testing.T, tr *Torrent) []byte {
	t.Helper()
	type result struct {
		buf []byte
		err error
	}
	ch := make(chan result, 1)
	go func() {
		buf, err := tr.Download()
		ch <- result{buf, err}
	}()
	select {
	case r := <-ch:
		if r.err != nil {
			t.Fatalf("Download: %v", r.err)
		}
		return r.buf
	case <-time.After(15 * time.Second):
		t.Fatal("Download timed out")
		return nil
	}
}

func newInfoHash() (h [20]byte) {
	for i := range h {
		h[i] = byte(i + 1)
	}
	return
}

// Two pieces: 16400 (forces two 16384+16 blocks) and 50 (the short last piece).
const (
	testPieceLength = int64(16400)
	testTotal       = 16450
	bothPieces      = byte(0xC0) // bits for piece 0 and 1
	noPieces        = byte(0x00)
)

func TestDownloadEndToEnd(t *testing.T) {
	content := makeContent(testTotal)
	infoHash := newInfoHash()
	addr := startSeeder(t, infoHash, content, testPieceLength, []byte{bothPieces})
	tr := testTorrent(infoHash, content, testPieceLength, []tracker.Peer{addrToPeer(t, addr)})

	got := runDownload(t, tr)
	if !bytes.Equal(got, content) {
		t.Fatalf("downloaded %d bytes, does not match the %d-byte source", len(got), len(content))
	}
}

func TestDownloadSkipsUnreachablePeer(t *testing.T) {
	content := makeContent(testTotal)
	infoHash := newInfoHash()
	addr := startSeeder(t, infoHash, content, testPieceLength, []byte{bothPieces})
	peers := []tracker.Peer{
		{IP: net.IPv4(127, 0, 0, 1), Port: 1}, // refused: worker should give up and the good peer carries it
		addrToPeer(t, addr),
	}
	tr := testTorrent(infoHash, content, testPieceLength, peers)

	got := runDownload(t, tr)
	if !bytes.Equal(got, content) {
		t.Fatal("download failed with an unreachable peer in the list")
	}
}

func TestDownloadRequeuesWhenPeerLacksPiece(t *testing.T) {
	content := makeContent(testTotal)
	infoHash := newInfoHash()
	empty := startSeeder(t, infoHash, content, testPieceLength, []byte{noPieces})  // advertises nothing
	full := startSeeder(t, infoHash, content, testPieceLength, []byte{bothPieces}) // has everything
	tr := testTorrent(infoHash, content, testPieceLength, []tracker.Peer{addrToPeer(t, empty), addrToPeer(t, full)})

	got := runDownload(t, tr)
	if !bytes.Equal(got, content) {
		t.Fatal("download failed when one peer lacked the pieces")
	}
}
