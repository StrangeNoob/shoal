// Package subtitles finds and downloads subtitles for downloaded videos via
// the OpenSubtitles.com REST API, matching by moviehash first (the file is on
// disk) with a filename query as fallback.
package subtitles

import (
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"os"
)

// hashChunkSize is the head/tail window the OpenSubtitles moviehash sums.
const hashChunkSize = 65536

// ErrTooSmall is returned for files under 128 KiB, which the protocol cannot hash.
var ErrTooSmall = errors.New("subtitles: file smaller than 128 KiB has no moviehash")

func fmtHash(h uint64) string { return fmt.Sprintf("%016x", h) }

// Hash computes the OpenSubtitles moviehash: file size plus the little-endian
// uint64 words of the first and last 64 KiB, summed with uint64 wraparound.
func Hash(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	fi, err := f.Stat()
	if err != nil {
		return "", err
	}
	size := fi.Size()
	if size < 2*hashChunkSize {
		return "", ErrTooSmall
	}
	h := uint64(size)
	sum := func(off int64) error {
		buf := make([]byte, hashChunkSize)
		if _, err := f.Seek(off, io.SeekStart); err != nil {
			return err
		}
		if _, err := io.ReadFull(f, buf); err != nil {
			return err
		}
		for i := 0; i < hashChunkSize; i += 8 {
			h += binary.LittleEndian.Uint64(buf[i:])
		}
		return nil
	}
	if err := sum(0); err != nil {
		return "", err
	}
	if err := sum(size - hashChunkSize); err != nil {
		return "", err
	}
	return fmtHash(h), nil
}
