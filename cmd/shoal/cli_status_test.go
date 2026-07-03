package main

import (
	"bytes"
	"encoding/json"
	"testing"
)

func TestStateOf(t *testing.T) {
	dead := func(int) bool { return false }
	alive := func(int) bool { return true }
	cases := []struct {
		a    Active
		f    func(int) bool
		want string
	}{
		{Active{Error: "boom"}, alive, "error"},
		{Active{Done: true}, alive, "done"},
		{Active{Done: true, Pid: 42}, dead, "done"}, // done wins over stalled even with a dead pid
		{Active{Pid: 42}, dead, "stalled"},
		{Active{Pid: 42}, alive, "downloading"},
	}
	for _, c := range cases {
		if got := stateOf(c.a, c.f); got != c.want {
			t.Errorf("stateOf(%+v) = %q, want %q", c.a, got, c.want)
		}
	}
}

func TestPrintStatusJSON(t *testing.T) {
	var buf bytes.Buffer
	items := []Active{{ID: "aaaa0000", Name: "M", Total: 200, Completed: 100, Pid: 1}}
	printStatus(&buf, items, true, func(int) bool { return true })
	var rows []map[string]any
	if err := json.Unmarshal(buf.Bytes(), &rows); err != nil {
		t.Fatalf("bad json: %v\n%s", err, buf.String())
	}
	if rows[0]["state"] != "downloading" || rows[0]["percent"].(float64) != 0.5 {
		t.Fatalf("row = %+v", rows[0])
	}
}
