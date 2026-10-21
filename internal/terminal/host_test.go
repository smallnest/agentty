package terminal

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/smallnest/agentty/internal/sessionhost"
)

func TestRestoreHostSession(t *testing.T) {
	home := t.TempDir()
	id := "sess_host_test"
	dir := filepath.Join(home, "sessions")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	socket := hostSocketPath(home, id)
	metaPath := hostMetaPath(home, id)

	done := make(chan struct{})
	go func() {
		_ = sessionhost.Serve(sessionhost.ServerConfig{
			ID:          id,
			Socket:      socket,
			MetaPath:    metaPath,
			Command:     "/bin/sh",
			Args:        []string{"-c", "printf persisted; sleep 10"},
			ReplayBytes: 4096,
		})
		close(done)
	}()
	t.Cleanup(func() {
		select {
		case <-done:
		case <-time.After(3 * time.Second):
			t.Errorf("host did not stop")
		}
	})

	deadline := time.Now().Add(2 * time.Second)
	for {
		if _, err := os.Stat(socket); err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("host socket was not created")
		}
		time.Sleep(20 * time.Millisecond)
	}

	if err := saveHostMeta(home, hostMeta{
		ID:        id,
		Name:      "persisted",
		Agent:     "shell",
		Command:   "/bin/sh",
		Args:      []string{"-c", "printf persisted; sleep 10"},
		Socket:    socket,
		CreatedAt: time.Now(),
	}); err != nil {
		t.Fatalf("save meta: %v", err)
	}

	manager := NewManager(0)
	manager.SetHome(home)
	defer manager.CloseAll()
	if err := manager.Restore(nil, 4096); err != nil {
		t.Fatalf("restore: %v", err)
	}

	session := manager.Get(id)
	if session == nil {
		t.Fatal("session was not restored")
	}
	if session.Name() != "persisted" {
		t.Fatalf("name = %q, want persisted", session.Name())
	}
	if session.IdleFor(time.Now()) <= 0 {
		t.Fatal("restored session should start idle so the janitor can reclaim it")
	}

	sink := newFakeSink(128)
	if !session.Attach(sink, 0) {
		t.Fatal("attach failed")
	}
	if !waitForOutput(t, sink, "persisted", 3*time.Second) {
		t.Fatal("did not observe the host replay")
	}
}
