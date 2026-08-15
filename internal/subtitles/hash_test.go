package subtitles

import (
	"encoding/binary"
	"os"
	"path/filepath"
	"testing"
)

// writeFixture writes size bytes where each 8-byte word is its little-endian
// word index, so expected hashes are computable in the test itself.
func writeFixture(t *testing.T, size int64) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "v.mkv")
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	buf := make([]byte, 8)
	for i := int64(0); i < size/8; i++ {
		binary.LittleEndian.PutUint64(buf, uint64(i))
		if _, err := f.Write(buf); err != nil {
			t.Fatal(err)
		}
	}
	return path
}

// expectedHash mirrors the spec: size + sum of LE words of first and last 64 KiB.
func expectedHash(size int64) uint64 {
	h := uint64(size)
	for i := int64(0); i < hashChunkSize/8; i++ { // first 64 KiB: words 0..8191
		h += uint64(i)
	}
	tailStart := (size - hashChunkSize) / 8
	for i := tailStart; i < tailStart+hashChunkSize/8; i++ { // last 64 KiB
		h += uint64(i)
	}
	return h
}

func TestHashKnownContent(t *testing.T) {
	size := int64(256 * 1024)
	path := writeFixture(t, size)
	got, err := Hash(path)
	if err != nil {
		t.Fatal(err)
	}
	want := expectedHash(size)
	if got != fmtHash(want) {
		t.Fatalf("Hash = %s, want %016x", got, want)
	}
	if len(got) != 16 {
		t.Fatalf("hash must be 16 hex chars, got %d", len(got))
	}
}

func TestHashOverlappingHeadTail(t *testing.T) {
	// 128 KiB exactly: head and tail are the same bytes, both still summed.
	size := int64(128 * 1024)
	path := writeFixture(t, size)
	got, err := Hash(path)
	if err != nil {
		t.Fatal(err)
	}
	if got != fmtHash(expectedHash(size)) {
		t.Fatalf("Hash = %s, want %016x", got, expectedHash(size))
	}
}

func TestHashTooSmall(t *testing.T) {
	path := writeFixture(t, 64*1024)
	if _, err := Hash(path); err != ErrTooSmall {
		t.Fatalf("err = %v, want ErrTooSmall", err)
	}
}

func TestHashMissingFile(t *testing.T) {
	if _, err := Hash(filepath.Join(t.TempDir(), "nope.mkv")); err == nil {
		t.Fatal("missing file must error")
	}
}
