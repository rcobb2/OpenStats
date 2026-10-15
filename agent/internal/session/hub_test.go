package session

import (
	"bytes"
	"strings"
	"testing"
	"time"
)

func TestParseLine(t *testing.T) {
	good := []struct {
		in   string
		want Sample
	}{
		{`{"pid":1234,"idleMs":5000}`, Sample{1234, 5 * time.Second}},
		{`{"pid":0,"idleMs":0}`, Sample{0, 0}},
	}
	for _, c := range good {
		got, ok := ParseLine([]byte(c.in))
		if !ok || got != c.want {
			t.Errorf("ParseLine(%q) = %+v,%v want %+v", c.in, got, ok, c.want)
		}
	}
	for _, bad := range []string{``, `garbage`, `{"pid":-1,"idleMs":0}`, `{"pid":1,"idleMs":-5}`, `{"pid":"x"}`, `[1,2]`} {
		if _, ok := ParseLine([]byte(bad)); ok {
			t.Errorf("ParseLine(%q) accepted malformed input", bad)
		}
	}
}

func TestEncodeLineRoundTrip(t *testing.T) {
	in := Sample{PID: 42, Idle: 1500 * time.Millisecond}
	got, ok := ParseLine(bytes.TrimSpace(EncodeLine(in)))
	if !ok || got != in {
		t.Fatalf("round trip = %+v,%v", got, ok)
	}
}

func TestReadSamplesSkipsMalformedAndInvalid(t *testing.T) {
	input := `{"pid":1,"idleMs":1}` + "\n" +
		"not json\n" +
		`{"pid":666,"idleMs":2}` + "\n" +
		`{"pid":0,"idleMs":3}` + "\n" +
		`{"pid":2,"idleMs":4}` + "\n"
	var got []Sample
	err := ReadSamples(strings.NewReader(input), func(pid uint32) bool { return pid != 666 }, func(s Sample) { got = append(got, s) })
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 3 || got[0].PID != 1 || got[1].PID != 0 || got[2].PID != 2 {
		t.Fatalf("got %+v", got)
	}
}

func TestReadSamplesOversizedLineStopsReading(t *testing.T) {
	input := `{"pid":1,"idleMs":1}` + "\n" + strings.Repeat("x", MaxLineBytes+10) + "\n" + `{"pid":2,"idleMs":2}` + "\n"
	var got []Sample
	err := ReadSamples(strings.NewReader(input), nil, func(s Sample) { got = append(got, s) })
	if err == nil {
		t.Fatal("want error for oversized line")
	}
	if len(got) != 1 || got[0].PID != 1 {
		t.Fatalf("got %+v, want only the line before the oversized one", got)
	}
}

func TestHubFreshnessAndLatestWins(t *testing.T) {
	h := NewHub()
	now := time.Unix(1000, 0)
	h.now = func() time.Time { return now }

	h.Update("a", Sample{PID: 1})
	h.Update("a", Sample{PID: 2}) // replaces
	h.Update("b", Sample{PID: 3})
	got := h.Fresh()
	if len(got) != 2 || got[0].PID != 2 || got[1].PID != 3 {
		t.Fatalf("Fresh = %+v, want a's latest then b", got)
	}

	now = now.Add(FreshFor - time.Second)
	h.Update("b", Sample{PID: 4})
	now = now.Add(2 * time.Second) // a is now stale, b fresh
	got = h.Fresh()
	if len(got) != 1 || got[0].PID != 4 || h.Connected() != 1 {
		t.Fatalf("Fresh = %+v connected=%d, want only b", got, h.Connected())
	}

	h.Remove("b")
	if h.Connected() != 0 {
		t.Fatal("removed helper still connected")
	}
}
