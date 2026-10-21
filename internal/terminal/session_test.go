package terminal

import (
	"strings"
	"sync"
	"testing"
	"time"
)

type fakeSink struct {
	ch     chan Frame
	mu     sync.Mutex
	closed bool
}

func newFakeSink(size int) *fakeSink {
	return &fakeSink{ch: make(chan Frame, size)}
}

func (f *fakeSink) TryEnqueue(frame Frame) bool {
	select {
	case f.ch <- frame:
		return true
	default:
		return false
	}
}

func (f *fakeSink) Enqueue(frame Frame) bool { return f.TryEnqueue(frame) }

func (f *fakeSink) Close(int, string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.closed = true
}

func waitForOutput(t *testing.T, sink *fakeSink, want string, timeout time.Duration) bool {
	t.Helper()
	deadline := time.After(timeout)
	for {
		select {
		case frame := <-sink.ch:
			if frame.Op != OpOutput && frame.Op != OpSnapshot {
				continue
			}
			if strings.Contains(string(frame.Data), want) {
				return true
			}
		case <-deadline:
			return false
		}
	}
}

func waitForOp(t *testing.T, sink *fakeSink, op byte, timeout time.Duration) (Frame, bool) {
	t.Helper()
	deadline := time.After(timeout)
	for {
		select {
		case frame := <-sink.ch:
			if frame.Op == op {
				return frame, true
			}
		case <-deadline:
			return Frame{}, false
		}
	}
}

func TestSessionRename(t *testing.T) {
	manager := NewManager(0)
	defer manager.CloseAll()

	session, _, err := manager.Acquire("rename", Options{
		Command: Command{Command: "/bin/sh", Args: []string{"-c", "cat"}},
		Name:    "initial",
	})
	if err != nil {
		t.Fatalf("acquire: %v", err)
	}

	sink := newFakeSink(64)
	if !session.Attach(sink, 0) {
		t.Fatal("attach failed")
	}
	frame, ok := waitForOp(t, sink, OpName, time.Second)
	if !ok || string(frame.Data) != "initial" {
		t.Fatalf("initial name = %q, ok=%v", frame.Data, ok)
	}

	if !session.SetName("api-refactor") {
		t.Fatal("SetName failed")
	}
	if session.Name() != "api-refactor" {
		t.Fatalf("Name = %q", session.Name())
	}
	frame, ok = waitForOp(t, sink, OpName, time.Second)
	if !ok || string(frame.Data) != "api-refactor" {
		t.Fatalf("renamed value = %q, ok=%v", frame.Data, ok)
	}

	if session.SetName("") {
		t.Fatal("empty name should fail")
	}
	if session.SetName(strings.Repeat("x", 65)) {
		t.Fatal("overlong name should fail")
	}
	if session.Name() != "api-refactor" {
		t.Fatalf("failed renames changed the name to %q", session.Name())
	}
}

func TestSessionOutput(t *testing.T) {
	manager := NewManager(0)
	defer manager.CloseAll()

	session, created, err := manager.Acquire("out", Options{
		Command:     Command{Command: "/bin/sh", Args: []string{"-c", "printf hello"}},
		ReplayBytes: 4096,
	})
	if err != nil {
		t.Fatalf("acquire: %v", err)
	}
	if !created {
		t.Fatal("expected a new session")
	}

	sink := newFakeSink(256)
	if !session.Attach(sink, 3) {
		t.Fatal("attach failed")
	}
	if !waitForOutput(t, sink, "hello", 3*time.Second) {
		t.Fatal("did not observe output")
	}
}

func TestSessionInitialSize(t *testing.T) {
	manager := NewManager(0)
	defer manager.CloseAll()

	session, _, err := manager.Acquire("size", Options{
		Command: Command{Command: "/bin/sh", Args: []string{"-c", "stty size"}},
		Cols:    100,
		Rows:    30,
	})
	if err != nil {
		t.Fatalf("acquire: %v", err)
	}

	sink := newFakeSink(256)
	if !session.Attach(sink, 0) {
		t.Fatal("attach failed")
	}
	if !waitForOutput(t, sink, "30 100", 3*time.Second) {
		t.Fatal("initial size was not applied")
	}
}

func TestSessionInput(t *testing.T) {
	manager := NewManager(0)
	defer manager.CloseAll()

	session, _, err := manager.Acquire("input", Options{
		Command: Command{Command: "/bin/sh", Args: []string{"-c", "cat"}},
	})
	if err != nil {
		t.Fatalf("acquire: %v", err)
	}

	sink := newFakeSink(256)
	if !session.Attach(sink, 0) {
		t.Fatal("attach failed")
	}

	if _, err := session.Write([]byte("ping\n")); err != nil {
		t.Fatalf("write: %v", err)
	}
	if !waitForOutput(t, sink, "ping", 3*time.Second) {
		t.Fatal("input did not reach the PTY")
	}
}

func TestManagerReusesLiveSession(t *testing.T) {
	manager := NewManager(0)
	defer manager.CloseAll()

	opts := Options{Command: Command{Command: "/bin/sh", Args: []string{"-c", "cat"}}}
	first, created, err := manager.Acquire("reuse", opts)
	if err != nil {
		t.Fatalf("acquire: %v", err)
	}
	if !created {
		t.Fatal("first acquire should create")
	}

	second, created, err := manager.Acquire("reuse", opts)
	if err != nil {
		t.Fatalf("acquire: %v", err)
	}
	if created {
		t.Fatal("second acquire should reuse")
	}
	if first != second {
		t.Fatal("expected the same session pointer")
	}
}

func TestManagerReplacesRemovedSession(t *testing.T) {
	manager := NewManager(0)
	defer manager.CloseAll()

	opts := Options{Command: Command{Command: "/bin/sh", Args: []string{"-c", "cat"}}}
	first, _, err := manager.Acquire("replace", opts)
	if err != nil {
		t.Fatalf("acquire: %v", err)
	}

	manager.Remove("replace")
	if !first.IsClosed() {
		t.Fatal("removed session should be closed")
	}

	second, created, err := manager.Acquire("replace", opts)
	if err != nil {
		t.Fatalf("acquire: %v", err)
	}
	if !created {
		t.Fatal("expected a replacement session")
	}
	if second == first {
		t.Fatal("expected a new session pointer")
	}
}
