package source

import "testing"

func TestParseMagnetInfoHashEncodingForms(t *testing.T) {
	const ih = "9ee38ecc0105ed61b0ef93a875325afe784b6fb5"
	cases := map[string]string{
		"percent-encoded (url.Values.Encode)": "magnet:?dn=BBB&tr=udp%3A%2F%2Ftracker.opentrackr.org%3A1337%2Fannounce&xt=urn%3Abtih%3A" + ih,
		"plain colons":                        "magnet:?xt=urn:btih:" + ih + "&dn=BBB",
		"round-trip via buildMagnet":          buildMagnet(ih, "BBB"),
	}
	for name, magnet := range cases {
		if got := ParseMagnetInfoHash(magnet); got != ih {
			t.Errorf("%s: ParseMagnetInfoHash = %q, want %q", name, got, ih)
		}
	}
	if got := ParseMagnetInfoHash("magnet:?dn=nohash&tr=udp%3A%2F%2Fx"); got != "" {
		t.Errorf("no-xt magnet should yield \"\", got %q", got)
	}
}
