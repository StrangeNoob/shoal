// Package metainfo reads a .torrent file into a structured form and computes
// its infohash. A .torrent is just a bencoded dictionary; the interesting part
// is the nested "info" dictionary, which describes the actual content (name,
// how it's chopped into fixed-size pieces, and the SHA-1 of every piece).
package metainfo

import (
	"crypto/sha1"
	"errors"

	"github.com/StrangeNoob/shoal/bencode"
)

// File is one entry of a multi-file torrent.
type File struct {
	Length int64
	Path   []string // path components, relative to the torrent's name directory
}

// Info is the content description (the "info" dictionary).
type Info struct {
	Name        string
	PieceLength int64
	Pieces      []byte // concatenated 20-byte SHA-1 hashes, one per piece
	Length      int64  // total length when single-file; 0 when multi-file
	Files       []File // populated only for multi-file torrents
}

// MetaInfo is the whole .torrent file.
type MetaInfo struct {
	Announce     string
	AnnounceList [][]string
	Info         Info
	InfoHash     [20]byte
}

// Parse decodes a .torrent file's bytes.
func Parse(data []byte) (*MetaInfo, error) {
	top, raw, err := bencode.DecodeTopDict(data)
	if err != nil {
		return nil, err
	}

	m := &MetaInfo{}
	if a, ok := top["announce"].(string); ok {
		m.Announce = a
	}
	if al, ok := top["announce-list"].([]any); ok {
		for _, tier := range al {
			ts, ok := tier.([]any)
			if !ok {
				continue
			}
			var urls []string
			for _, u := range ts {
				if s, ok := u.(string); ok {
					urls = append(urls, s)
				}
			}
			if len(urls) > 0 {
				m.AnnounceList = append(m.AnnounceList, urls)
			}
		}
	}

	infoRaw, ok := raw["info"]
	if !ok {
		return nil, errors.New("metainfo: missing info dictionary")
	}
	// The infohash is the SHA-1 of the info dict's *original* bytes.
	m.InfoHash = sha1.Sum(infoRaw)

	infoMap, ok := top["info"].(map[string]any)
	if !ok {
		return nil, errors.New("metainfo: info is not a dictionary")
	}
	info, err := parseInfo(infoMap)
	if err != nil {
		return nil, err
	}
	m.Info = info
	return m, nil
}

func parseInfo(d map[string]any) (Info, error) {
	var info Info

	name, _ := d["name"].(string)
	info.Name = name

	pl, ok := d["piece length"].(int64)
	if !ok || pl <= 0 {
		return info, errors.New("metainfo: missing or invalid piece length")
	}
	info.PieceLength = pl

	pieces, ok := d["pieces"].(string)
	if !ok {
		return info, errors.New("metainfo: missing pieces")
	}
	if len(pieces)%20 != 0 {
		return info, errors.New("metainfo: pieces field is not a multiple of 20 bytes")
	}
	info.Pieces = []byte(pieces)

	switch {
	case d["length"] != nil:
		length, ok := d["length"].(int64)
		if !ok {
			return info, errors.New("metainfo: invalid length")
		}
		info.Length = length // single-file torrent
	case d["files"] != nil:
		files, ok := d["files"].([]any)
		if !ok {
			return info, errors.New("metainfo: invalid files list")
		}
		for _, f := range files {
			fm, ok := f.(map[string]any)
			if !ok {
				continue
			}
			length, _ := fm["length"].(int64)
			var path []string
			if segs, ok := fm["path"].([]any); ok {
				for _, seg := range segs {
					if s, ok := seg.(string); ok {
						path = append(path, s)
					}
				}
			}
			info.Files = append(info.Files, File{Length: length, Path: path})
		}
	default:
		return info, errors.New("metainfo: info has neither length nor files")
	}
	return info, nil
}

// NumPieces is the number of pieces the content is split into.
func (m *MetaInfo) NumPieces() int { return len(m.Info.Pieces) / 20 }

// PieceHash returns the expected SHA-1 of piece i.
func (m *MetaInfo) PieceHash(i int) (h [20]byte) {
	copy(h[:], m.Info.Pieces[i*20:i*20+20])
	return h
}

// TotalLength is the size of the whole download in bytes.
func (m *MetaInfo) TotalLength() int64 {
	if len(m.Info.Files) == 0 {
		return m.Info.Length
	}
	var n int64
	for _, f := range m.Info.Files {
		n += f.Length
	}
	return n
}

// PieceSize returns the length of piece i in bytes. Every piece is
// Info.PieceLength except the last, which is whatever remains.
func (m *MetaInfo) PieceSize(i int) int64 {
	total := m.TotalLength()
	begin := int64(i) * m.Info.PieceLength
	end := begin + m.Info.PieceLength
	if end > total {
		end = total
	}
	return end - begin
}
