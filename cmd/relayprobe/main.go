// Command relayprobe is a minimal relay client for smoke tests. It connects to
// a relay as role=client, starts an agent session, and streams its output.
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"net/url"
	"os"
	"time"

	"github.com/gorilla/websocket"

	"github.com/smallnest/agentty/internal/terminal"
)

func main() {
	relay := flag.String("relay", "127.0.0.1:4000", "relay host:port")
	server := flag.String("server", "", "serverId")
	agent := flag.String("agent", "shell", "agent name")
	cmd := flag.String("cmd", "", "raw command (overrides agent)")
	input := flag.String("input", "", "input to send after attach")
	tls := flag.Bool("tls", false, "use wss")
	timeout := flag.Duration("timeout", 10*time.Second, "maximum run time")
	flag.Parse()

	if *server == "" {
		fmt.Fprintln(os.Stderr, "missing -server")
		os.Exit(2)
	}

	scheme := "ws"
	if *tls {
		scheme = "wss"
	}
	query := url.Values{}
	query.Set("serverId", *server)
	query.Set("role", "client")
	query.Set("v", "2")
	target := fmt.Sprintf("%s://%s/ws?%s", scheme, *relay, query.Encode())

	conn, _, err := websocket.DefaultDialer.Dial(target, nil)
	if err != nil {
		fmt.Fprintf(os.Stderr, "dial: %v\n", err)
		os.Exit(1)
	}
	defer conn.Close()

	request := terminal.CreateRequest{Slot: 0, Agent: *agent, Cmd: *cmd, Cols: 120, Rows: 40}
	payload, _ := json.Marshal(request)
	if err := conn.WriteMessage(websocket.BinaryMessage, terminal.Encode(terminal.Frame{
		Op:   terminal.OpCreate,
		Slot: 0,
		Data: payload,
	})); err != nil {
		fmt.Fprintf(os.Stderr, "create: %v\n", err)
		os.Exit(1)
	}

	if *input != "" {
		go func() {
			time.Sleep(500 * time.Millisecond)
			_ = conn.WriteMessage(websocket.BinaryMessage, terminal.Encode(terminal.Frame{
				Op:   terminal.OpInput,
				Slot: 0,
				Data: []byte(*input),
			}))
		}()
	}

	_ = conn.SetReadDeadline(time.Now().Add(*timeout))
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
		switch frame.Op {
		case terminal.OpOutput, terminal.OpSnapshot:
			_, _ = os.Stdout.Write(frame.Data)
		case terminal.OpAttached:
			fmt.Fprintf(os.Stderr, "\n[attached %s]\n", frame.Data)
		case terminal.OpExit:
			if code, ok := terminal.DecodeExit(frame.Data); ok {
				fmt.Fprintf(os.Stderr, "\n[exit %d]\n", code)
			}
			return
		case terminal.OpError:
			fmt.Fprintf(os.Stderr, "\n[error %s]\n", frame.Data)
			return
		}
	}
}
