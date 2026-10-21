package terminal

import (
	"testing"
	"time"
)

func TestTmuxNameSanitizes(t *testing.T) {
	backend := NewTmuxBackend("", "agentty")
	got := backend.Name("a/b:c.d e")
	want := "agentty_a_b_c_d_e"
	if got != want {
		t.Fatalf("Name = %q, want %q", got, want)
	}
}

func TestTmuxMetaRoundTrip(t *testing.T) {
	backend := NewTmuxBackend(t.TempDir(), "agentty")
	meta := SessionMeta{
		ID:        "sess_1",
		Tmux:      "agentty_sess_1",
		Agent:     "claude",
		Command:   "claude",
		Cwd:       "/tmp",
		CreatedAt: time.Now().Truncate(time.Second),
	}
	if err := backend.SaveMeta(meta); err != nil {
		t.Fatalf("save: %v", err)
	}

	metas, err := backend.LoadMetas()
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if len(metas) != 1 || metas[0].ID != meta.ID || metas[0].Tmux != meta.Tmux {
		t.Fatalf("metas = %+v", metas)
	}

	backend.DeleteMeta(meta.Tmux)
	metas, err = backend.LoadMetas()
	if err != nil {
		t.Fatalf("load after delete: %v", err)
	}
	if len(metas) != 0 {
		t.Fatalf("expected no metas after delete, got %+v", metas)
	}
}

// TestTmuxSessionSurvivesRestart proves the persistence path: kill the attach
// process (simulating a daemon crash), then restore from metadata and see the
// agent's output again. It skips when tmux is not installed.
func TestTmuxSessionSurvivesRestart(t *testing.T) {
	backend := NewTmuxBackend(t.TempDir(), "agenttytest")
	if !backend.Available() {
		t.Skip("tmux is not installed")
	}

	id := "sess_" + time.Now().Format("150405.000000")
	manager := NewManager(0)
	if err := manager.Restore(backend, 4096); err != nil {
		t.Fatalf("restore: %v", err)
	}
	t.Cleanup(func() { manager.CloseAll() })

	session, _, err := manager.Acquire(id, Options{
		Command: Command{Command: "/bin/sh", Args: []string{"-c", "printf persisted; sleep 60"}},
	})
	if err != nil {
		t.Fatalf("acquire: %v", err)
	}

	sink := newFakeSink(256)
	if !session.Attach(sink, 0) {
		t.Fatal("attach failed")
	}
	if !waitForOutput(t, sink, "persisted", 3*time.Second) {
		t.Fatal("did not observe initial output")
	}

	// Simulate a daemon crash: kill the tmux attach process without calling
	// Close, so tmux keeps the session.
	if session.cmd != nil && session.cmd.Process != nil {
		_ = session.cmd.Process.Kill()
	}
	time.Sleep(300 * time.Millisecond)

	restoredManager := NewManager(0)
	if err := restoredManager.Restore(backend, 4096); err != nil {
		t.Fatalf("second restore: %v", err)
	}
	t.Cleanup(func() { restoredManager.CloseAll() })

	restored := restoredManager.Get(id)
	if restored == nil {
		t.Fatal("session was not restored from metadata")
	}

	sink2 := newFakeSink(256)
	if !restored.Attach(sink2, 0) {
		t.Fatal("attach to restored session failed")
	}
	if !waitForOutput(t, sink2, "persisted", 3*time.Second) {
		t.Fatal("restored session did not redraw its screen")
	}
}
