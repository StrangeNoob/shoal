package metainfo

import (
	"bytes"
	"crypto/sha1"
	"testing"

	"github.com/StrangeNoob/shoal/bencode"
)

// buildTorrent encodes a synthetic single-file .torrent so the tests are fully
// self-contained (no real .torrent file or network needed).
func buildTorrent(t *testing.T, info map[string]any) (data, infoBytes []byte) {
	t.Helper()
	var err error
	infoBytes, err = bencode.Encode(info)
	if err != nil {
		t.Fatalf("encode info: %v", err)
	}
	data, err = bencode.Encode(map[string]any{
		"announce": "http://tracker.example/announce",
		"info":     info,
	})
	if err != nil {
		t.Fatalf("encode top: %v", err)
	}
	return data, infoBytes
}

func TestParseSingleFile(t *testing.T) {
	info := map[string]any{
		"name":         "hello.bin",
		"piece length": int64(32768),
		"length":       int64(40000),
		"pieces":       string(bytes.Repeat([]byte{0x00}, 40)), // 2 pieces
	}
	data, infoBytes := buildTorrent(t, info)

	m, err := Parse(data)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}

	if m.Info.Name != "hello.bin" {
		t.Errorf("Name = %q, want hello.bin", m.Info.Name)
	}
	if want := sha1.Sum(infoBytes); m.InfoHash != want {
		t.Errorf("InfoHash = %x, want %x", m.InfoHash, want)
	}
	if m.NumPieces() != 2 {
		t.Errorf("NumPieces = %d, want 2", m.NumPieces())
	}
	if m.TotalLength() != 40000 {
		t.Errorf("TotalLength = %d, want 40000", m.TotalLength())
	}
	if m.PieceSize(0) != 32768 {
		t.Errorf("PieceSize(0) = %d, want 32768", m.PieceSize(0))
	}
	if m.PieceSize(1) != 40000-32768 {
		t.Errorf("PieceSize(1) = %d, want %d", m.PieceSize(1), 40000-32768)
	}
}

func TestParseMultiFile(t *testing.T) {
	info := map[string]any{
		"name":         "mydir",
		"piece length": int64(16384),
		"pieces":       string(bytes.Repeat([]byte{0x00}, 20)),
		"files": []any{
			map[string]any{"length": int64(100), "path": []any{"a.txt"}},
			map[string]any{"length": int64(200), "path": []any{"sub", "b.txt"}},
		},
	}
	data, _ := buildTorrent(t, info)

	m, err := Parse(data)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if m.TotalLength() != 300 {
		t.Errorf("TotalLength = %d, want 300", m.TotalLength())
	}
	if len(m.Info.Files) != 2 {
		t.Fatalf("Files = %d, want 2", len(m.Info.Files))
	}
	if m.Info.Files[1].Path[0] != "sub" || m.Info.Files[1].Path[1] != "b.txt" {
		t.Errorf("Files[1].Path = %v, want [sub b.txt]", m.Info.Files[1].Path)
	}
}

func TestParseRejectsBadPieces(t *testing.T) {
	info := map[string]any{
		"name":         "x",
		"piece length": int64(16384),
		"length":       int64(10),
		"pieces":       "not-a-multiple-of-twenty", // 24 bytes
	}
	data, _ := buildTorrent(t, info)
	if _, err := Parse(data); err == nil {
		t.Fatal("expected error for pieces not multiple of 20, got nil")
	}
}
