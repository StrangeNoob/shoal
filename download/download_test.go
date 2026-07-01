package download

import (
	"crypto/sha1"
	"testing"

	"github.com/StrangeNoob/shoal/peer"
)

func TestParsePieceCopiesData(t *testing.T) {
	buf := make([]byte, 16)
	// index=0, begin=4, data=DEAD
	payload := []byte{0, 0, 0, 0, 0, 0, 0, 4, 0xDE, 0xAD}
	msg := &peer.Message{ID: peer.MsgPiece, Payload: payload}

	n, err := parsePiece(0, buf, msg)
	if err != nil {
		t.Fatalf("parsePiece: %v", err)
	}
	if n != 2 {
		t.Errorf("n = %d, want 2", n)
	}
	if buf[4] != 0xDE || buf[5] != 0xAD {
		t.Errorf("buf[4:6] = %v, want [222 173]", buf[4:6])
	}
}

func TestParsePieceWrongIndex(t *testing.T) {
	buf := make([]byte, 16)
	payload := []byte{0, 0, 0, 9, 0, 0, 0, 0, 0xFF}
	msg := &peer.Message{ID: peer.MsgPiece, Payload: payload}
	if _, err := parsePiece(0, buf, msg); err == nil {
		t.Fatal("expected error for mismatched index")
	}
}

func TestParsePieceOverflow(t *testing.T) {
	buf := make([]byte, 4)
	// begin=2, data of 4 bytes -> 2+4 > 4
	payload := []byte{0, 0, 0, 0, 0, 0, 0, 2, 1, 2, 3, 4}
	msg := &peer.Message{ID: peer.MsgPiece, Payload: payload}
	if _, err := parsePiece(0, buf, msg); err == nil {
		t.Fatal("expected error for data running past piece end")
	}
}

func TestCheckIntegrity(t *testing.T) {
	data := []byte("the quick brown fox")
	pw := &pieceWork{index: 0, hash: sha1.Sum(data), length: len(data)}
	if err := checkIntegrity(pw, data); err != nil {
		t.Errorf("checkIntegrity on good data: %v", err)
	}
	if err := checkIntegrity(pw, []byte("tampered tampered..")); err == nil {
		t.Error("expected integrity error on tampered data")
	}
}
