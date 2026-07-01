package bencode

import "testing"

func TestEncodeByteSliceIntAndList(t *testing.T) {
	got, err := Encode([]byte("ab"))
	if err != nil {
		t.Fatalf("Encode []byte: %v", err)
	}
	if string(got) != "2:ab" {
		t.Errorf("Encode([]byte) = %q, want 2:ab", got)
	}

	got, err = Encode(int(5))
	if err != nil {
		t.Fatalf("Encode int: %v", err)
	}
	if string(got) != "i5e" {
		t.Errorf("Encode(int 5) = %q, want i5e", got)
	}

	got, err = Encode([]any{"a", int64(1), []byte("b")})
	if err != nil {
		t.Fatalf("Encode list: %v", err)
	}
	if string(got) != "l1:ai1e1:be" {
		t.Errorf("Encode list = %q, want l1:ai1e1:be", got)
	}
}

func TestEncodeUnsupportedType(t *testing.T) {
	if _, err := Encode(3.14); err == nil {
		t.Error("Encode(float64) expected error, got nil")
	}
	if _, err := Encode([]any{3.14}); err == nil {
		t.Error("Encode(list with bad element) expected error, got nil")
	}
	if _, err := Encode(map[string]any{"k": 3.14}); err == nil {
		t.Error("Encode(dict with bad value) expected error, got nil")
	}
}

func TestDecodeTopDictErrors(t *testing.T) {
	cases := map[string]string{
		"not a dictionary":  "i1e",
		"unterminated dict": "d3:cow3:moo",
		"bad value":         "d3:cowi1", // value is an unterminated integer
		"value at end":      "d2:ab",    // key ok, no value
		"key not a string":  "di1ei2ee", // key must be a byte string
	}
	for name, in := range cases {
		if _, _, err := DecodeTopDict([]byte(in)); err == nil {
			t.Errorf("%s: DecodeTopDict(%q) expected error, got nil", name, in)
		}
	}
}

func TestDecodeMoreMalformed(t *testing.T) {
	bad := []string{
		"i1.5e", // invalid integer token
		"iabce", // invalid integer token
		"1a:bc", // invalid string length (non-digit before colon)
	}
	for _, in := range bad {
		if _, err := Decode([]byte(in)); err == nil {
			t.Errorf("Decode(%q) expected error, got nil", in)
		}
	}
}
