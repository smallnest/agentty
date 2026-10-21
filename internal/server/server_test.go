package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"

	"github.com/smallnest/agentty/internal/config"
	"github.com/smallnest/agentty/internal/terminal"
)

func newTestServer(t *testing.T) *httptest.Server {
	t.Helper()
	cfg := config.Config{
		DefaultAgent: "shell",
		AllowCmd:     true,
		WorkingDir:   t.TempDir(),
		ReplayBytes:  64 * 1024,
		SendQueue:    64,
	}
	manager := terminal.NewManager(0)
	ts := httptest.NewServer(New(cfg, manager, nil).Handler())
	t.Cleanup(func() {
		ts.Close()
		manager.CloseAll()
	})
	return ts
}

func dial(t *testing.T, ts *httptest.Server) *websocket.Conn {
	t.Helper()
	url := "ws" + strings.TrimPrefix(ts.URL, "http") + "/ws"
	conn, _, err := websocket.DefaultDialer.Dial(url, nil)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	return conn
}

func sendCreate(t *testing.T, ws *websocket.Conn, request terminal.CreateRequest) {
	t.Helper()
	data, err := json.Marshal(request)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	frame := terminal.Frame{Op: terminal.OpCreate, Slot: request.Slot, Data: data}
	if err := ws.WriteMessage(websocket.BinaryMessage, terminal.Encode(frame)); err != nil {
		t.Fatalf("write create: %v", err)
	}
}

func sendFrame(t *testing.T, ws *websocket.Conn, frame terminal.Frame) {
	t.Helper()
	if err := ws.WriteMessage(websocket.BinaryMessage, terminal.Encode(frame)); err != nil {
		t.Fatalf("write frame: %v", err)
	}
}

func nextFrame(t *testing.T, ws *websocket.Conn) terminal.Frame {
	t.Helper()
	_ = ws.SetReadDeadline(time.Now().Add(3 * time.Second))
	for {
		messageType, data, err := ws.ReadMessage()
		if err != nil {
			t.Fatalf("read: %v", err)
		}
		if messageType != websocket.BinaryMessage {
			continue
		}
		frame, err := terminal.Decode(data)
		if err != nil {
			t.Fatalf("decode: %v", err)
		}
		return frame
	}
}

func waitForOutput(t *testing.T, ws *websocket.Conn, want string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		frame := nextFrame(t, ws)
		if frame.Op != terminal.OpOutput && frame.Op != terminal.OpSnapshot {
			continue
		}
		if strings.Contains(string(frame.Data), want) {
			return
		}
	}
	t.Fatalf("did not observe %q", want)
}

func TestHealth(t *testing.T) {
	ts := newTestServer(t)
	response, err := http.Get(ts.URL + "/health")
	if err != nil {
		t.Fatalf("health: %v", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", response.StatusCode)
	}
}

func TestAttachedFrame(t *testing.T) {
	ts := newTestServer(t)
	ws := dial(t, ts)

	sendCreate(t, ws, terminal.CreateRequest{Slot: 5, Session: "abc", Cmd: "sleep 30"})

	frame := nextFrame(t, ws)
	if frame.Op != terminal.OpAttached {
		t.Fatalf("first op = %#x, want attached", frame.Op)
	}
	if frame.Slot != 5 {
		t.Fatalf("slot = %d, want 5", frame.Slot)
	}
	if string(frame.Data) != "abc" {
		t.Fatalf("session = %q, want abc", frame.Data)
	}
}

func TestOutput(t *testing.T) {
	ts := newTestServer(t)
	ws := dial(t, ts)

	sendCreate(t, ws, terminal.CreateRequest{Slot: 0, Session: "out", Cmd: "printf hello"})
	waitForOutput(t, ws, "hello")
}

func TestInitialSize(t *testing.T) {
	ts := newTestServer(t)
	ws := dial(t, ts)

	sendCreate(t, ws, terminal.CreateRequest{Slot: 0, Session: "size", Cmd: "stty size", Cols: 100, Rows: 30})
	waitForOutput(t, ws, "30 100")
}

func TestInput(t *testing.T) {
	ts := newTestServer(t)
	ws := dial(t, ts)

	sendCreate(t, ws, terminal.CreateRequest{Slot: 0, Session: "input", Cmd: "cat"})
	sendFrame(t, ws, terminal.Frame{Op: terminal.OpInput, Slot: 0, Data: []byte("ping\n")})
	waitForOutput(t, ws, "ping")
}

func TestResize(t *testing.T) {
	ts := newTestServer(t)
	ws := dial(t, ts)

	sendCreate(t, ws, terminal.CreateRequest{Slot: 0, Session: "resize", Cmd: "sh"})
	// Consume the attached frame so the next read sees command output.
	_ = nextFrame(t, ws)

	sendFrame(t, ws, terminal.Frame{Op: terminal.OpResize, Slot: 0, Data: terminal.ResizePayload(40, 120)})
	sendFrame(t, ws, terminal.Frame{Op: terminal.OpInput, Slot: 0, Data: []byte("stty size\n")})
	waitForOutput(t, ws, "40 120")
}

func TestInvalidCreateReportsError(t *testing.T) {
	ts := newTestServer(t)
	ws := dial(t, ts)

	sendFrame(t, ws, terminal.Frame{Op: terminal.OpCreate, Slot: 0, Data: []byte("not json")})

	frame := nextFrame(t, ws)
	if frame.Op != terminal.OpError {
		t.Fatalf("op = %#x, want error", frame.Op)
	}
	if !strings.Contains(string(frame.Data), "invalid create") {
		t.Fatalf("error = %q", frame.Data)
	}
}

func TestAgentsEndpointIncludesBuiltins(t *testing.T) {
	ts := newTestServer(t)
	response, err := http.Get(ts.URL + "/api/agents")
	if err != nil {
		t.Fatalf("agents: %v", err)
	}
	defer response.Body.Close()
	var agents []agentInfo
	if err := json.NewDecoder(response.Body).Decode(&agents); err != nil {
		t.Fatalf("decode: %v", err)
	}
	names := make(map[string]bool, len(agents))
	for _, item := range agents {
		names[item.Name] = true
	}
	for _, want := range []string{"shell", "claude", "codex", "opencode", "pi", "gemini", "aider", "goose"} {
		if !names[want] {
			t.Fatalf("missing agent %q in %v", want, names)
		}
	}
}

func TestExitedSessionIsHiddenFromList(t *testing.T) {
	ts := newTestServer(t)
	ws := dial(t, ts)

	sendCreate(t, ws, terminal.CreateRequest{Slot: 0, Session: "exited", Cmd: "printf done"})

	sawExit := false
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		frame := nextFrame(t, ws)
		if frame.Op == terminal.OpExit {
			sawExit = true
			break
		}
	}
	if !sawExit {
		t.Fatal("did not observe the exit frame")
	}

	response, err := http.Get(ts.URL + "/api/sessions")
	if err != nil {
		t.Fatalf("sessions: %v", err)
	}
	defer response.Body.Close()
	var sessions []terminal.SessionView
	if err := json.NewDecoder(response.Body).Decode(&sessions); err != nil {
		t.Fatalf("decode: %v", err)
	}
	for _, session := range sessions {
		if session.ID == "exited" {
			t.Fatal("exited session should not be listed")
		}
	}
}

func TestSessionRenameOverWS(t *testing.T) {
	ts := newTestServer(t)
	ws := dial(t, ts)

	sendCreate(t, ws, terminal.CreateRequest{Slot: 0, Session: "rename-ws", Cmd: "cat"})

	attached := nextFrame(t, ws)
	if attached.Op != terminal.OpAttached {
		t.Fatalf("op = %#x, want attached", attached.Op)
	}
	name := nextFrame(t, ws)
	if name.Op != terminal.OpName {
		t.Fatalf("op = %#x, want name", name.Op)
	}

	sendFrame(t, ws, terminal.Frame{Op: terminal.OpRename, Slot: 0, Data: []byte("api-refactor")})

	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		frame := nextFrame(t, ws)
		if frame.Op != terminal.OpName {
			continue
		}
		if string(frame.Data) == "api-refactor" {
			return
		}
	}
	t.Fatal("did not observe the renamed frame")
}
