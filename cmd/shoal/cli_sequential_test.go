package main

import (
	"bytes"
	"testing"

	"github.com/StrangeNoob/shoal/internal/engine"
)

func TestSequentialOnOffResolvesPrefix(t *testing.T) {
	fake := &fakeEngine{statuses: []engine.Status{{InfoHash: "abcdef0123", Name: "M"}}}
	serveFakeDaemon(t, fake)

	var buf bytes.Buffer
	if code := runSequential([]string{"abcdef", "on"}, &buf); code != 0 {
		t.Fatalf("on exit = %d: %s", code, buf.String())
	}
	if code := runSequential([]string{"abcdef", "off"}, &buf); code != 0 {
		t.Fatalf("off exit = %d: %s", code, buf.String())
	}
	seq := fake.gotSequential()
	if len(seq) != 2 || seq[0].infoHash != "abcdef0123" || !seq[0].on || seq[1].infoHash != "abcdef0123" || seq[1].on {
		t.Fatalf("sequential calls = %+v, want on then off for abcdef0123", seq)
	}
}

func TestSequentialUnknownID(t *testing.T) {
	fake := &fakeEngine{statuses: []engine.Status{{InfoHash: "abcdef0123", Name: "M"}}}
	serveFakeDaemon(t, fake)

	var buf bytes.Buffer
	if code := runSequential([]string{"zz", "on"}, &buf); code == 0 {
		t.Fatal("unknown id should exit non-zero")
	}
}

func TestSequentialBadThirdArg(t *testing.T) {
	fake := &fakeEngine{statuses: []engine.Status{{InfoHash: "abcdef0123", Name: "M"}}}
	serveFakeDaemon(t, fake)

	var buf bytes.Buffer
	if code := runSequential([]string{"abcdef", "maybe"}, &buf); code != 2 {
		t.Fatalf("bad third arg exit = %d, want 2", code)
	}
	if seq := fake.gotSequential(); seq != nil {
		t.Fatalf("bad third arg must not call SetSequential, got %+v", seq)
	}
}

func TestSequentialNoArgs(t *testing.T) {
	var buf bytes.Buffer
	if code := runSequential(nil, &buf); code != 2 {
		t.Fatalf("exit = %d, want 2", code)
	}
}
