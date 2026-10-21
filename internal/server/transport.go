package server

import (
	"sync"
	"time"

	"github.com/gorilla/websocket"

	"github.com/smallnest/agentty/internal/terminal"
)

// Transport carries terminal frames for one client. The local HTTP server and
// the relay data socket both provide one, so session handling is identical.
type Transport interface {
	// ReadFrame returns the next binary terminal frame. ok is false for
	// non-binary messages that should be ignored.
	ReadFrame() (frame terminal.Frame, ok bool, err error)
	WriteFrame(frame terminal.Frame) error
	CloseWith(code int, reason string)
}

// wsTransport wraps a gorilla WebSocket connection.
type wsTransport struct {
	conn *websocket.Conn
	once sync.Once
}

// NewWSTransport adapts a WebSocket connection to a Transport.
func NewWSTransport(conn *websocket.Conn) Transport {
	conn.SetReadLimit(terminal.MaxFrameBytes)
	return &wsTransport{conn: conn}
}

func (t *wsTransport) ReadFrame() (terminal.Frame, bool, error) {
	for {
		messageType, data, err := t.conn.ReadMessage()
		if err != nil {
			return terminal.Frame{}, false, err
		}
		if messageType != websocket.BinaryMessage {
			continue
		}
		frame, err := terminal.Decode(data)
		if err != nil {
			continue
		}
		return frame, true, nil
	}
}

func (t *wsTransport) WriteFrame(frame terminal.Frame) error {
	_ = t.conn.SetWriteDeadline(time.Now().Add(10 * time.Second))
	return t.conn.WriteMessage(websocket.BinaryMessage, terminal.Encode(frame))
}

func (t *wsTransport) CloseWith(code int, reason string) {
	t.once.Do(func() {
		_ = t.conn.WriteControl(
			websocket.CloseMessage,
			websocket.FormatCloseMessage(code, reason),
			time.Now().Add(time.Second),
		)
		_ = t.conn.Close()
	})
}
