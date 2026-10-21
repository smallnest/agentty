package sessionhost

import (
	"errors"
	"net"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"sync"
	"syscall"
	"time"

	"github.com/creack/pty"
)

// ServerConfig describes the PTY host.
type ServerConfig struct {
	ID          string
	Socket      string
	MetaPath    string
	Command     string
	Args        []string
	Cwd         string
	Cols        uint16
	Rows        uint16
	ReplayBytes int
}

// Serve runs a host until the agent exits, the daemon kills it, or a signal
// arrives. It blocks.
func Serve(cfg ServerConfig) error {
	server, err := newServer(cfg)
	if err != nil {
		return err
	}
	return server.Run()
}

type Server struct {
	cfg      ServerConfig
	listener net.Listener
	ptmx     *os.File
	cmd      *exec.Cmd
	replay   *ring
	done     chan struct{}

	mu     sync.Mutex
	client *hostClient
	closed bool
	waited bool
}

func newServer(cfg ServerConfig) (*Server, error) {
	if cfg.Socket == "" {
		return nil, errors.New("sessionhost: missing socket")
	}
	if cfg.Command == "" {
		return nil, errors.New("sessionhost: missing command")
	}
	if err := os.MkdirAll(filepath.Dir(cfg.Socket), 0o700); err != nil {
		return nil, err
	}
	_ = os.Remove(cfg.Socket)

	listener, err := net.Listen("unix", cfg.Socket)
	if err != nil {
		return nil, err
	}
	_ = os.Chmod(cfg.Socket, 0o600)

	cols, rows := cfg.Cols, cfg.Rows
	if cols == 0 {
		cols = 80
	}
	if rows == 0 {
		rows = 24
	}

	cmd := exec.Command(cfg.Command, cfg.Args...)
	cmd.Env = append(os.Environ(), "TERM=xterm-256color")
	if cfg.Cwd != "" {
		cmd.Dir = cfg.Cwd
	}
	ptmx, err := pty.StartWithSize(cmd, &pty.Winsize{Cols: cols, Rows: rows})
	if err != nil {
		_ = listener.Close()
		_ = os.Remove(cfg.Socket)
		return nil, err
	}

	return &Server{
		cfg:      cfg,
		listener: listener,
		ptmx:     ptmx,
		cmd:      cmd,
		replay:   newRing(cfg.ReplayBytes),
		done:     make(chan struct{}),
	}, nil
}

// Run serves connections until the host shuts down.
func (s *Server) Run() error {
	signals := make(chan os.Signal, 1)
	signal.Notify(signals, syscall.SIGTERM, syscall.SIGINT, syscall.SIGHUP)
	defer signal.Stop(signals)

	go func() {
		select {
		case <-signals:
			s.shutdown(0, false)
		case <-s.done:
		}
	}()

	go s.readPTY()
	go s.acceptLoop()

	<-s.done
	s.cleanup()
	return nil
}

func (s *Server) readPTY() {
	buf := make([]byte, 32*1024)
	for {
		n, err := s.ptmx.Read(buf)
		if n > 0 {
			chunk := make([]byte, n)
			copy(chunk, buf[:n])
			s.replay.Write(chunk)
			s.broadcast(Frame{Type: TypeOutput, Data: chunk})
		}
		if err != nil {
			break
		}
	}

	code := 0
	if s.cmd != nil {
		if err := s.cmd.Wait(); err != nil {
			var exitErr *exec.ExitError
			if errors.As(err, &exitErr) {
				if c := exitErr.ExitCode(); c >= 0 {
					code = c
				}
			}
		}
	}

	s.mu.Lock()
	s.waited = true
	s.mu.Unlock()
	s.shutdown(code, true)
}

func (s *Server) acceptLoop() {
	for {
		conn, err := s.listener.Accept()
		if err != nil {
			return
		}
		client := newHostClient(conn)
		s.setClient(client)
		go client.writeLoop()
		client.enqueue(Frame{Type: TypeReady})
		if snapshot := s.replay.Snapshot(); len(snapshot) > 0 {
			client.enqueue(Frame{Type: TypeOutput, Data: snapshot})
		}
		go s.serveClient(client)
	}
}

func (s *Server) serveClient(client *hostClient) {
	for {
		frame, err := readFrame(client.conn)
		if err != nil {
			break
		}
		switch frame.Type {
		case TypeInput:
			_, _ = s.ptmx.Write(frame.Data)
		case TypeResize:
			if rows, cols, ok := DecodeResize(frame.Data); ok {
				_ = pty.Setsize(s.ptmx, &pty.Winsize{Rows: rows, Cols: cols})
			}
		case TypeKill:
			s.shutdown(0, false)
			return
		}
	}
	s.clearClient(client)
}

func (s *Server) shutdown(code int, notify bool) {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return
	}
	s.closed = true
	waited := s.waited
	client := s.client
	s.client = nil
	s.mu.Unlock()

	if notify && client != nil {
		client.enqueue(Frame{Type: TypeExit, Data: ExitPayload(code)})
		// Give the writer a moment to flush the final screen and exit code.
		time.Sleep(100 * time.Millisecond)
	}
	if client != nil {
		client.close()
	}

	_ = s.listener.Close()
	if s.ptmx != nil {
		_ = s.ptmx.Close()
	}
	if !waited && s.cmd != nil && s.cmd.Process != nil {
		killGroup(s.cmd.Process.Pid)
	}
	close(s.done)
}

func (s *Server) cleanup() {
	_ = s.listener.Close()
	_ = os.Remove(s.cfg.Socket)
	if s.cfg.MetaPath != "" {
		_ = os.Remove(s.cfg.MetaPath)
	}
}

func (s *Server) broadcast(frame Frame) {
	s.mu.Lock()
	client := s.client
	s.mu.Unlock()
	if client == nil {
		return
	}
	if !client.enqueue(frame) {
		client.close()
		s.clearClient(client)
	}
}

func (s *Server) setClient(client *hostClient) {
	s.mu.Lock()
	old := s.client
	s.client = client
	s.mu.Unlock()
	if old != nil {
		old.close()
	}
}

func (s *Server) clearClient(client *hostClient) {
	s.mu.Lock()
	if s.client == client {
		s.client = nil
	}
	s.mu.Unlock()
}

type hostClient struct {
	conn net.Conn
	send chan Frame
	done chan struct{}
	once sync.Once
}

func newHostClient(conn net.Conn) *hostClient {
	return &hostClient{
		conn: conn,
		send: make(chan Frame, 64),
		done: make(chan struct{}),
	}
}

func (c *hostClient) enqueue(frame Frame) bool {
	select {
	case <-c.done:
		return false
	default:
	}
	select {
	case c.send <- frame:
		return true
	default:
		return false
	}
}

func (c *hostClient) writeLoop() {
	for {
		select {
		case <-c.done:
			return
		case frame := <-c.send:
			_ = c.conn.SetWriteDeadline(time.Now().Add(10 * time.Second))
			if err := writeFrame(c.conn, frame); err != nil {
				c.close()
				return
			}
		}
	}
}

func (c *hostClient) close() {
	c.once.Do(func() {
		close(c.done)
		_ = c.conn.Close()
	})
}
