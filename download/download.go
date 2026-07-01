// Package download drives the actual transfer. It spreads the torrent's pieces
// across all known peers using a worker pool: each peer connection pulls piece
// "work" off a shared channel, downloads and hash-verifies it, and sends the
// finished piece back. A piece that fails (bad peer, hash mismatch, timeout) is
// put back on the queue for another worker to retry.
//
// This is the well-known design from Jesse Li's "Building a BitTorrent client
// from the ground up in Go", kept deliberately simple so the flow is readable.
package download

import (
	"crypto/sha1"
	"encoding/binary"
	"fmt"
	"runtime"
	"time"

	"github.com/StrangeNoob/shoal/metainfo"
	"github.com/StrangeNoob/shoal/peer"
	"github.com/StrangeNoob/shoal/tracker"
)

const (
	maxBlockSize = 16384 // peers reject requests larger than 16 KiB
	maxBacklog   = 5     // how many block requests to keep in flight at once
)

// Torrent is everything a download needs: the parsed metadata, our identity,
// and the list of peers to try.
type Torrent struct {
	Meta     *metainfo.MetaInfo
	PeerID   [20]byte
	InfoHash [20]byte
	Peers    []tracker.Peer
}

type pieceWork struct {
	index  int
	hash   [20]byte
	length int
}

type pieceResult struct {
	index int
	buf   []byte
}

// pieceProgress tracks an in-flight piece on one connection.
type pieceProgress struct {
	index      int
	conn       *peer.Conn
	buf        []byte
	downloaded int
	requested  int
	backlog    int
}

func (s *pieceProgress) readMessage() error {
	msg, err := s.conn.Read()
	if err != nil {
		return err
	}
	if msg == nil { // keep-alive
		return nil
	}
	switch msg.ID {
	case peer.MsgUnchoke:
		s.conn.Choked = false
	case peer.MsgChoke:
		s.conn.Choked = true
	case peer.MsgHave:
		if len(msg.Payload) == 4 {
			s.conn.Bitfield.SetPiece(int(binary.BigEndian.Uint32(msg.Payload)))
		}
	case peer.MsgPiece:
		n, err := parsePiece(s.index, s.buf, msg)
		if err != nil {
			return err
		}
		s.downloaded += n
		s.backlog--
	}
	return nil
}

// parsePiece copies a received block into buf and returns its size.
func parsePiece(index int, buf []byte, msg *peer.Message) (int, error) {
	if msg.ID != peer.MsgPiece {
		return 0, fmt.Errorf("expected PIECE (id %d), got id %d", peer.MsgPiece, msg.ID)
	}
	if len(msg.Payload) < 8 {
		return 0, fmt.Errorf("PIECE payload too short: %d bytes", len(msg.Payload))
	}
	parsedIndex := int(binary.BigEndian.Uint32(msg.Payload[0:4]))
	if parsedIndex != index {
		return 0, fmt.Errorf("expected piece %d, got %d", index, parsedIndex)
	}
	begin := int(binary.BigEndian.Uint32(msg.Payload[4:8]))
	data := msg.Payload[8:]
	if begin+len(data) > len(buf) {
		return 0, fmt.Errorf("data offset %d+%d runs past piece end %d", begin, len(data), len(buf))
	}
	copy(buf[begin:], data)
	return len(data), nil
}

func attemptDownloadPiece(c *peer.Conn, pw *pieceWork) ([]byte, error) {
	state := pieceProgress{index: pw.index, conn: c, buf: make([]byte, pw.length)}

	// A generous per-piece deadline keeps a slow or stalled peer from blocking
	// a worker forever; the piece is simply requeued for someone else.
	c.SetDeadline(time.Now().Add(30 * time.Second))
	defer c.SetDeadline(time.Time{})

	for state.downloaded < pw.length {
		// While unchoked, fill the request pipeline. Pipelining several block
		// requests at once is what makes BitTorrent fast — waiting for each
		// block before asking for the next would waste the round-trip time.
		if !c.Choked {
			for state.backlog < maxBacklog && state.requested < pw.length {
				blockSize := maxBlockSize
				if remaining := pw.length - state.requested; remaining < blockSize {
					blockSize = remaining
				}
				if err := c.SendRequest(pw.index, state.requested, blockSize); err != nil {
					return nil, err
				}
				state.backlog++
				state.requested += blockSize
			}
		}
		if err := state.readMessage(); err != nil {
			return nil, err
		}
	}
	return state.buf, nil
}

func checkIntegrity(pw *pieceWork, buf []byte) error {
	if sha1.Sum(buf) != pw.hash {
		return fmt.Errorf("piece %d failed integrity check", pw.index)
	}
	return nil
}

func (t *Torrent) startWorker(p tracker.Peer, work chan *pieceWork, results chan *pieceResult) {
	c, err := peer.Connect(p.String(), t.InfoHash, t.PeerID)
	if err != nil {
		return // couldn't reach this peer; nothing to clean up
	}
	defer c.Close()

	// Politeness: tell the peer we're not choking it and that we want data.
	c.SendUnchoke()
	c.SendInterested()

	for pw := range work {
		if !c.Bitfield.HasPiece(pw.index) {
			work <- pw // this peer doesn't have the piece; let someone else take it
			continue
		}
		buf, err := attemptDownloadPiece(c, pw)
		if err != nil {
			work <- pw // requeue and disconnect: this connection is unhealthy
			return
		}
		if err := checkIntegrity(pw, buf); err != nil {
			work <- pw // corrupt data; try a different peer
			continue
		}
		c.SendHave(pw.index)
		results <- &pieceResult{index: pw.index, buf: buf}
	}
}

// Download fetches the whole torrent and returns the assembled bytes (the full
// content laid out end to end, ready to be split into files).
func (t *Torrent) Download() ([]byte, error) {
	numPieces := t.Meta.NumPieces()
	if numPieces == 0 {
		return nil, fmt.Errorf("download: torrent has no pieces")
	}

	// The buffer is sized to the piece count so requeuing a piece never blocks:
	// there are never more than numPieces items in flight.
	work := make(chan *pieceWork, numPieces)
	results := make(chan *pieceResult)
	for i := 0; i < numPieces; i++ {
		work <- &pieceWork{index: i, hash: t.Meta.PieceHash(i), length: int(t.Meta.PieceSize(i))}
	}

	for _, p := range t.Peers {
		go t.startWorker(p, work, results)
	}

	buf := make([]byte, t.Meta.TotalLength())
	done := 0
	for done < numPieces {
		res := <-results
		begin := int64(res.index) * t.Meta.Info.PieceLength
		copy(buf[begin:], res.buf)
		done++

		percent := float64(done) / float64(numPieces) * 100
		activeWorkers := runtime.NumGoroutine() - 1
		fmt.Printf("\r  %5.1f%%  (%d/%d pieces, %d workers) ", percent, done, numPieces, activeWorkers)
	}
	close(work)
	fmt.Println()
	return buf, nil
}
