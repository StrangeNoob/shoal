package metainfo

import (
	"bytes"
	"testing"

	"github.com/StrangeNoob/shoal/bencode"
)

func TestParseAnnounceListAndPieceHash(t *testing.T) {
	h0 := bytes.Repeat([]byte{0x01}, 20)
	h1 := bytes.Repeat([]byte{0x02}, 20)
	info := map[string]any{
		"name":         "x",
		"piece length": int64(16384),
		"length":       int64(100),
		"pieces":       string(append(append([]byte{}, h0...), h1...)),
	}
	data, err := bencode.Encode(map[string]any{
		"announce": "http://primary/announce",
		"announce-list": []any{
			[]any{"http://a/announce"},
			[]any{"http://b/announce", "http://c/announce"},
		},
		"info": info,
	})
	if err != nil {
		t.Fatalf("encode: %v", err)
	}

	m, err := Parse(data)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if len(m.AnnounceList) != 2 {
		t.Fatalf("AnnounceList tiers = %d, want 2", len(m.AnnounceList))
	}
	if m.AnnounceList[1][1] != "http://c/announce" {
		t.Errorf("AnnounceList[1][1] = %q, want http://c/announce", m.AnnounceList[1][1])
	}
	if got := m.PieceHash(0); !bytes.Equal(got[:], h0) {
		t.Errorf("PieceHash(0) = %x, want %x", got, h0)
	}
	if got := m.PieceHash(1); !bytes.Equal(got[:], h1) {
		t.Errorf("PieceHash(1) = %x, want %x", got, h1)
	}
}

func TestParseErrors(t *testing.T) {
	enc := func(v any) []byte {
		b, err := bencode.Encode(v)
		if err != nil {
			t.Fatalf("encode: %v", err)
		}
		return b
	}
	valid := string(bytes.Repeat([]byte{0}, 20))

	cases := map[string][]byte{
		"missing info":          enc(map[string]any{"announce": "http://t"}),
		"info not a dictionary": enc(map[string]any{"info": "x"}),
		"missing piece length": enc(map[string]any{"info": map[string]any{
			"name": "x", "pieces": valid, "length": int64(1)}}),
		"zero piece length": enc(map[string]any{"info": map[string]any{
			"name": "x", "piece length": int64(0), "pieces": valid, "length": int64(1)}}),
		"missing pieces": enc(map[string]any{"info": map[string]any{
			"name": "x", "piece length": int64(16384), "length": int64(1)}}),
		"ragged pieces": enc(map[string]any{"info": map[string]any{
			"name": "x", "piece length": int64(16384), "pieces": "abc", "length": int64(1)}}),
		"neither length nor files": enc(map[string]any{"info": map[string]any{
			"name": "x", "piece length": int64(16384), "pieces": valid}}),
		"invalid length type": enc(map[string]any{"info": map[string]any{
			"name": "x", "piece length": int64(16384), "pieces": valid, "length": "big"}}),
		"invalid files type": enc(map[string]any{"info": map[string]any{
			"name": "x", "piece length": int64(16384), "pieces": valid, "files": "nope"}}),
	}
	for name, data := range cases {
		if _, err := Parse(data); err == nil {
			t.Errorf("%s: Parse expected error, got nil", name)
		}
	}
}
