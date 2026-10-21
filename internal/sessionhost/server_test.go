package sessionhost

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func runHost(t *testing.T, cfg ServerConfig) (<-chan struct{}, string) {
	t.Helper()
	if cfg.Socket == "" {
		cfg.Socket = filepath.Join(t.TempDir(), "host.sock")
	}
	if cfg.ReplayBytes == 0 {
		cfg.ReplayBytes = 4096
	}
	done := make(chan struct{})
	go func() {
		_ = Serve(cfg)
		close(done)
	}()
	t.Cleanup(func() {
		select {
		case <-done:
		case <-time.After(3 * time.Second):
			t.Errorf("host did not stop")
		}
	})
	return done, cfg.Socket
}

func dial(t *testing.T, socket string) *Client {
	t.Helper()
	client, err := Dial(socket, 3*time.Second)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	t.Cleanup(func() { _ = client.Close() })
	return client
}

func waitForFrame(t *testing.T, client *Client, want byte, timeout time.Duration) Frame {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		_ = client.conn.SetReadDeadline(time.Now().Add(timeout))
		frame, err := client.ReadFrame()
		if err != nil {
			t.Fatalf("read frame: %v", err)
		}
		if frame.Type == want {
			return frame
		}
	}
	t.Fatalf("did not see frame %#x", want)
	return Frame{}
}

func waitForOutput(t *testing.T, client *Client, want string, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		_ = client.conn.SetReadDeadline(time.Now().Add(timeout))
		frame, err := client.ReadFrame()
		if err != nil {
			t.Fatalf("read output: %v", err)
		}
		if frame.Type == TypeOutput && strings.Contains(string(frame.Data), want) {
			return
		}
	}
	t.Fatalf("did not see output %q", want)
}

func TestHostOutput(t *testing.T) {
	done, socket := runHost(t, ServerConfig{
		Command: "/bin/sh",
		Args:    []string{"-c", "printf hello; sleep 5"},
	})
	client := dial(t, socket)
	waitForFrame(t, client, TypeReady, 2*time.Second)
	waitForOutput(t, client, "hello", 3*time.Second)

	_ = client.Kill()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("host did not stop after kill")
	}
}

func TestHostInput(t *testing.T) {
	_, socket := runHost(t, ServerConfig{
		Command: "/bin/sh",
		Args:    []string{"-c", "cat"},
	})
	client := dial(t, socket)
	waitForFrame(t, client, TypeReady, 2*time.Second)

	if err := client.SendInput([]byte("ping\n")); err != nil {
		t.Fatalf("input: %v", err)
	}
	waitForOutput(t, client, "ping", 3*time.Second)
	_ = client.Kill()
}

func TestHostReplayOnReconnect(t *testing.T) {
	_, socket := runHost(t, ServerConfig{
		Command: "/bin/sh",
		Args:    []string{"-c", "printf persisted; sleep 5"},
	})

	first := dial(t, socket)
	waitForFrame(t, first, TypeReady, 2*time.Second)
	waitForOutput(t, first, "persisted", 3*time.Second)
	_ = first.Close()

	second := dial(t, socket)
	waitForFrame(t, second, TypeReady, 2*time.Second)
	waitForOutput(t, second, "persisted", 3*time.Second)
	_ = second.Kill()
}

func TestHostResize(t *testing.T) {
	_, socket := runHost(t, ServerConfig{
		Command: "/bin/sh",
		Args:    []string{"-c", "sh"},
	})
	client := dial(t, socket)
	waitForFrame(t, client, TypeReady, 2*time.Second)

	if err := client.Resize(30, 100); err != nil {
		t.Fatalf("resize: %v", err)
	}
	if err := client.SendInput([]byte("stty size\n")); err != nil {
		t.Fatalf("input: %v", err)
	}
	waitForOutput(t, client, "30 100", 3*time.Second)
	_ = client.Kill()
}

func TestHostKillRemovesSocket(t *testing.T) {
	done, socket := runHost(t, ServerConfig{
		Command: "/bin/sh",
		Args:    []string{"-c", "sleep 30"},
	})
	client := dial(t, socket)
	waitForFrame(t, client, TypeReady, 2*time.Second)

	if err := client.Kill(); err != nil {
		t.Fatalf("kill: %v", err)
	}
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("host did not stop")
	}
	if _, err := os.Stat(socket); !os.IsNotExist(err) {
		t.Fatalf("socket still exists: %v", err)
	}
}
