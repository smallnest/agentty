package sessionhost

import (
	"net"
	"sync"
	"time"
)

// Client is the daemon side of a host connection.
type Client struct {
	conn net.Conn
	mu   sync.Mutex
}

// Dial connects to a host socket, retrying until timeout. The host may still be
// starting when the daemon spawns it.
func Dial(socket string, timeout time.Duration) (*Client, error) {
	deadline := time.Now().Add(timeout)
	var lastErr error
	for {
		conn, err := net.DialTimeout("unix", socket, time.Second)
		if err == nil {
			return &Client{conn: conn}, nil
		}
		lastErr = err
		if time.Now().After(deadline) {
			return nil, lastErr
		}
		time.Sleep(50 * time.Millisecond)
	}
}

// SendInput forwards keyboard bytes to the PTY.
func (c *Client) SendInput(data []byte) error {
	return c.write(Frame{Type: TypeInput, Data: data})
}

// Resize changes the PTY size.
func (c *Client) Resize(rows, cols uint16) error {
	return c.write(Frame{Type: TypeResize, Data: ResizePayload(rows, cols)})
}

// Kill asks the host to terminate the agent and exit.
func (c *Client) Kill() error {
	return c.write(Frame{Type: TypeKill})
}

// ReadFrame reads one frame from the host.
func (c *Client) ReadFrame() (Frame, error) {
	return readFrame(c.conn)
}

// Close closes the socket. The host keeps running.
func (c *Client) Close() error {
	return c.conn.Close()
}

func (c *Client) write(frame Frame) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	_ = c.conn.SetWriteDeadline(time.Now().Add(5 * time.Second))
	return writeFrame(c.conn, frame)
}
