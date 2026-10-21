package terminal

import "testing"

func TestRingTrimsToBound(t *testing.T) {
	r := NewRing(8)
	r.Write([]byte("abcdef"))
	r.Write([]byte("ghij"))
	if got := string(r.Snapshot()); got != "cdefghij" {
		t.Fatalf("got %q, want %q", got, "cdefghij")
	}
}

func TestRingKeepsTailOfOversizedChunk(t *testing.T) {
	r := NewRing(4)
	r.Write([]byte("abcdefgh"))
	if got := string(r.Snapshot()); got != "efgh" {
		t.Fatalf("got %q, want %q", got, "efgh")
	}
}

func TestRingTrimsToEscapeBoundary(t *testing.T) {
	r := NewRing(5)
	r.Write([]byte("abc\x1bdef"))
	// Trimmed buffer is "c\x1bdef"; the leading fragment is dropped so replay
	// starts at the escape byte.
	if got := string(r.Snapshot()); got != "\x1bdef" {
		t.Fatalf("got %q, want %q", got, "\x1bdef")
	}
}

func TestRingEmptySnapshot(t *testing.T) {
	if got := NewRing(8).Snapshot(); got != nil {
		t.Fatalf("got %q, want nil", got)
	}
}
