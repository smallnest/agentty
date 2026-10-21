package relayclient

import (
	"context"
	"encoding/json"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"

	"github.com/smallnest/agentty/internal/config"
	"github.com/smallnest/agentty/internal/terminal"
)

func TestEndpoint(t *testing.T) {
	connector := New(
		config.Config{ServerID: "srv_x", RelayEndpoint: "relay.example.com:443", RelayUseTLS: true},
		terminal.NewManager(0),
		log.New(io.Discard, "", 0),
	)
	got := connector.endpoint("server", "cid1")
	want := "wss://relay.example.com:443/ws?connectionId=cid1&role=server&serverId=srv_x&v=2"
	if got != want {
		t.Fatalf("endpoint = %q, want %q", got, want)
	}
}

// TestConnectorForwardsToDataSocket exercises the full v2 handshake: the fake
// relay notifies the connector of a client, the connector opens the data
// socket, and terminal frames flow over it.
func TestConnectorForwardsToDataSocket(t *testing.T) {
	stop := make(chan struct{})
	result := make(chan struct{}, 1)

	upgrader := websocket.Upgrader{CheckOrigin: func(*http.Request) bool { return true }}
	mux := http.NewServeMux()
	mux.HandleFunc("/ws", func(w http.ResponseWriter, r *http.Request) {
		query := r.URL.Query()
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close()

		if query.Get("role") == "server" && query.Get("connectionId") == "" {
			// Control socket: announce one waiting client.
			_ = conn.WriteMessage(websocket.TextMessage, []byte(`{"type":"connected","connectionId":"cid1"}`))
			<-stop
			return
		}

		// Data socket: behave like a relay forwarding a browser's CREATE frame.
		request := terminal.CreateRequest{Slot: 0, Session: "s1", Cmd: "printf relay-ok"}
		payload, _ := json.Marshal(request)
		_ = conn.WriteMessage(websocket.BinaryMessage, terminal.Encode(terminal.Frame{
			Op:   terminal.OpCreate,
			Slot: 0,
			Data: payload,
		}))

		_ = conn.SetReadDeadline(time.Now().Add(5 * time.Second))
		for {
			messageType, data, err := conn.ReadMessage()
			if err != nil {
				return
			}
			if messageType != websocket.BinaryMessage {
				continue
			}
			frame, err := terminal.Decode(data)
			if err != nil {
				continue
			}
			if frame.Op != terminal.OpOutput && frame.Op != terminal.OpSnapshot {
				continue
			}
			if strings.Contains(string(frame.Data), "relay-ok") {
				select {
				case result <- struct{}{}:
				default:
				}
				return
			}
		}
	})

	ts := httptest.NewServer(mux)
	defer ts.Close()
	defer close(stop)

	cfg := config.Config{
		ServerID:      "srv_test",
		RelayEndpoint: strings.TrimPrefix(ts.URL, "http://"),
		AllowCmd:      true,
		DefaultAgent:  "shell",
		WorkingDir:    t.TempDir(),
		ReplayBytes:   4096,
		SendQueue:     16,
	}

	manager := terminal.NewManager(0)
	defer manager.CloseAll()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go New(cfg, manager, log.New(io.Discard, "", 0)).Run(ctx)

	select {
	case <-result:
	case <-time.After(8 * time.Second):
		t.Fatal("timed out waiting for relayed output")
	}
}
