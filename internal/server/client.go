package server

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"time"

	"github.com/smallnest/agentty/internal/config"
	"github.com/smallnest/agentty/internal/terminal"
)

// Client is one remote terminal client. It is transport-agnostic: the local
// WebSocket server and the relay data socket both drive it, so both paths run
// exactly the same session logic.
type Client struct {
	manager   *terminal.Manager
	config    config.Config
	transport Transport

	send chan terminal.Frame
	done chan struct{}
	once sync.Once

	mu    sync.Mutex
	slots map[uint16]*terminal.Session
}

// NewClient builds a client over transport.
func NewClient(manager *terminal.Manager, cfg config.Config, transport Transport) *Client {
	return &Client{
		manager:   manager,
		config:    cfg,
		transport: transport,
		send:      make(chan terminal.Frame, cfg.SendQueue),
		done:      make(chan struct{}),
		slots:     map[uint16]*terminal.Session{},
	}
}

// Run reads frames until the transport closes, then detaches its sessions.
func (c *Client) Run() {
	go c.writeLoop()
	for {
		frame, ok, err := c.transport.ReadFrame()
		if err != nil {
			break
		}
		if ok {
			c.handle(frame)
		}
	}
	c.Close(1000, "client closed")
}

// Close tears down the transport and detaches every session. The sessions are
// daemon-owned, so they keep running and can be reattached.
func (c *Client) Close(code int, reason string) {
	c.once.Do(func() {
		close(c.done)
		c.transport.CloseWith(code, reason)
		for _, session := range c.detachAll() {
			session.Detach(c)
		}
	})
}

func (c *Client) handle(frame terminal.Frame) {
	switch frame.Op {
	case terminal.OpCreate:
		c.handleCreate(frame)

	case terminal.OpInput:
		if session := c.session(frame.Slot); session != nil {
			_, _ = session.Write(frame.Data)
		}

	case terminal.OpResize:
		if session := c.session(frame.Slot); session != nil {
			if rows, cols, ok := terminal.DecodeResize(frame.Data); ok {
				_ = session.Resize(rows, cols)
			}
		}

	case terminal.OpClose:
		if session := c.session(frame.Slot); session != nil {
			c.untrack(frame.Slot)
			session.Detach(c)
			c.manager.Remove(session.ID())
		}

	case terminal.OpRename:
		if session := c.session(frame.Slot); session != nil {
			if !session.SetName(string(frame.Data)) {
				c.fail(frame.Slot, "invalid session name")
			}
		}
	}
}

func agentName(cfg config.Config, requested string) string {
	if requested != "" {
		return requested
	}
	if cfg.DefaultAgent != "" {
		return cfg.DefaultAgent
	}
	return "shell"
}

func (c *Client) handleCreate(frame terminal.Frame) {
	var request terminal.CreateRequest
	if err := json.Unmarshal(frame.Data, &request); err != nil {
		c.fail(frame.Slot, "invalid create request")
		return
	}

	sessionID := request.Session
	if sessionID == "" {
		sessionID = generateSessionID()
	}

	opts := terminal.Options{
		Command:     resolveCommand(c.config, request.Agent, request.Cmd),
		Cwd:         resolveCwd(c.config, request.Cwd),
		Cols:        request.Cols,
		Rows:        request.Rows,
		ReplayBytes: c.config.ReplayBytes,
		AgentName:   agentName(c.config, request.Agent),
		Name:        request.Name,
		Persist:     c.config.Persist,
		Home:        c.config.AgenttyHome,
	}

	session, _, err := c.manager.Acquire(sessionID, opts)
	if err != nil {
		c.fail(frame.Slot, "failed to start agent: "+err.Error())
		return
	}
	if !session.Attach(c, frame.Slot) {
		c.manager.Remove(session.ID())
		c.fail(frame.Slot, "session closed")
		return
	}
	c.track(frame.Slot, session)
}

func (c *Client) TryEnqueue(frame terminal.Frame) bool {
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

func (c *Client) Enqueue(frame terminal.Frame) bool {
	if c.TryEnqueue(frame) {
		return true
	}
	// Queue full: give the writer a short grace period before shedding.
	timer := time.NewTimer(2 * time.Second)
	defer timer.Stop()
	select {
	case c.send <- frame:
		return true
	case <-c.done:
		return false
	case <-timer.C:
		return false
	}
}

func (c *Client) writeLoop() {
	for {
		select {
		case <-c.done:
			return
		case frame := <-c.send:
			if err := c.transport.WriteFrame(frame); err != nil {
				c.Close(1011, "write failed")
				return
			}
		}
	}
}

func (c *Client) track(slot uint16, session *terminal.Session) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.slots[slot] = session
}

func (c *Client) untrack(slot uint16) {
	c.mu.Lock()
	defer c.mu.Unlock()
	delete(c.slots, slot)
}

func (c *Client) session(slot uint16) *terminal.Session {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.slots[slot]
}

func (c *Client) detachAll() []*terminal.Session {
	c.mu.Lock()
	defer c.mu.Unlock()
	sessions := make([]*terminal.Session, 0, len(c.slots))
	for _, session := range c.slots {
		sessions = append(sessions, session)
	}
	c.slots = map[uint16]*terminal.Session{}
	return sessions
}

func (c *Client) fail(slot uint16, message string) {
	c.Enqueue(terminal.Frame{Op: terminal.OpError, Slot: slot, Data: []byte(message)})
}

func resolveCommand(cfg config.Config, agent, cmd string) terminal.Command {
	if cmd != "" && cfg.AllowCmd {
		return terminal.Command{Command: "sh", Args: []string{"-c", cmd}}
	}

	name := agent
	if name == "" {
		name = cfg.DefaultAgent
	}
	switch name {
	case "claude":
		return terminal.Command{Command: "claude"}
	case "codex":
		return terminal.Command{Command: "codex"}
	case "opencode":
		return terminal.Command{Command: "opencode"}
	case "pi":
		return terminal.Command{Command: "pi"}
	default:
		return terminal.Command{Command: shellPath()}
	}
}

func resolveCwd(cfg config.Config, cwd string) string {
	if cwd != "" {
		if info, err := os.Stat(cwd); err == nil && info.IsDir() {
			return cwd
		}
	}
	if cfg.WorkingDir != "" {
		if info, err := os.Stat(cfg.WorkingDir); err == nil && info.IsDir() {
			return cfg.WorkingDir
		}
	}
	return filepath.Clean(".")
}

func shellPath() string {
	if shell := os.Getenv("SHELL"); shell != "" {
		return shell
	}
	return "/bin/sh"
}

func generateSessionID() string {
	var b [8]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "sess_fallback"
	}
	return "sess_" + hex.EncodeToString(b[:])
}

// LookupAgent reports whether an agent binary is available, for /api/agents.
func LookupAgent(name string) bool {
	if name == "shell" {
		return true
	}
	_, err := exec.LookPath(name)
	return err == nil
}
