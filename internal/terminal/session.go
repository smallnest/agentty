package terminal

import (
	"errors"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/creack/pty"

	"github.com/smallnest/agentty/internal/sessionhost"
)

// ErrClosed is returned when writing to a closed session.
var ErrClosed = errors.New("terminal: session closed")

// Sink receives frames for one WebSocket client.
//
// Enqueue may apply short-term backpressure; TryEnqueue must never block. The
// attach path uses TryEnqueue so registering a client and installing its
// snapshot is atomic with respect to PTY output.
type Sink interface {
	Enqueue(Frame) bool
	TryEnqueue(Frame) bool
	Close(code int, reason string)
}

// Command describes a program to run in a PTY.
type Command struct {
	Command string
	Args    []string
}

// Options configures a new session.
type Options struct {
	Command Command
	Cwd     string
	Cols    uint16
	Rows    uint16

	// ReplayBytes bounds the rebuild buffer.
	ReplayBytes int

	// Tmux, when non-nil and available, runs the command inside a tmux session
	// so it survives a daemon restart.
	Tmux *TmuxBackend

	// AgentName is recorded in tmux metadata for display after a restart.
	AgentName string

	// Name is the mutable display name. It defaults to AgentName or the id.
	Name string

	// Home is the agentty home used for persisted session metadata.
	Home string

	// Backend selects "auto", "host", "tmux", or "none".
	Backend string

	// Persist enables the built-in detached host.
	Persist bool
}

type attachment struct {
	sink Sink
	slot uint16
}

// exitGrace keeps an exited session (and its replay buffer) around briefly so
// a client that attaches late can still read the final output and exit code.
const exitGrace = 60 * time.Second

// Session owns one PTY process and fans its output out to attached clients.
type Session struct {
	id       string
	opts     Options
	ptmx     *os.File
	cmd      *exec.Cmd
	tmux     *TmuxBackend
	tmuxName string
	meta     SessionMeta
	host     *sessionhost.Client
	hostMeta hostMeta
	home     string

	mu          sync.Mutex
	attachments map[Sink]uint16
	closed      bool
	exited      bool
	waited      bool
	exitCode    int
	name        string
	idleSince   time.Time

	replay    *Ring
	onExit    func()
	closeOnce sync.Once
}

func newSession(id string, opts Options, onExit func()) (*Session, error) {
	cols, rows := normalizeCols(opts.Cols), normalizeRows(opts.Rows)
	opts.Name = defaultName(id, opts.AgentName, opts.Name)

	tmuxAvailable := opts.Tmux != nil && opts.Tmux.Available()
	useTmux := tmuxAvailable &&
		(opts.Backend == "" || opts.Backend == "auto" || opts.Backend == "tmux")
	if useTmux {
		tmuxName, err := opts.Tmux.Create(id, opts.Command, opts.Cwd, cols, rows)
		if err == nil {
			meta := SessionMeta{
				ID:        id,
				Tmux:      tmuxName,
				Agent:     opts.AgentName,
				Command:   opts.Command.Command,
				Cwd:       opts.Cwd,
				CreatedAt: time.Now(),
				Name:      opts.Name,
			}
			_ = opts.Tmux.SaveMeta(meta)
			return startAttached(id, opts, tmuxName, meta, onExit)
		}
	}
	if opts.Backend == "tmux" {
		return nil, errors.New("terminal: tmux backend requested but tmux is not available")
	}

	useHost := opts.Persist && opts.Home != "" &&
		(opts.Backend == "" || opts.Backend == "auto" || opts.Backend == "host")
	if useHost {
		return startHost(id, opts, onExit)
	}
	if opts.Backend == "host" {
		return nil, errors.New("terminal: host backend requested but persistence is disabled")
	}

	return startDirect(id, opts, SessionMeta{
		ID:      id,
		Agent:   opts.AgentName,
		Command: opts.Command.Command,
		Cwd:     opts.Cwd,
		Name:    opts.Name,
	}, onExit)
}

func startDirect(id string, opts Options, meta SessionMeta, onExit func()) (*Session, error) {
	cmd := exec.Command(opts.Command.Command, opts.Command.Args...)
	cmd.Env = append(os.Environ(), "TERM=xterm-256color")
	if opts.Cwd != "" {
		cmd.Dir = opts.Cwd
	}

	ptmx, err := pty.StartWithSize(cmd, &pty.Winsize{
		Cols: normalizeCols(opts.Cols),
		Rows: normalizeRows(opts.Rows),
	})
	if err != nil {
		return nil, err
	}
	return newSessionObject(id, opts, ptmx, cmd, onExit, "", meta), nil
}

func startAttached(id string, opts Options, tmuxName string, meta SessionMeta, onExit func()) (*Session, error) {
	cmd := opts.Tmux.Attach(tmuxName)
	cmd.Env = append(os.Environ(), "TERM=xterm-256color")

	ptmx, err := pty.StartWithSize(cmd, &pty.Winsize{
		Cols: normalizeCols(opts.Cols),
		Rows: normalizeRows(opts.Rows),
	})
	if err != nil {
		opts.Tmux.Kill(tmuxName)
		opts.Tmux.DeleteMeta(tmuxName)
		return nil, err
	}
	return newSessionObject(id, opts, ptmx, cmd, onExit, tmuxName, meta), nil
}

func newSessionObject(
	id string,
	opts Options,
	ptmx *os.File,
	cmd *exec.Cmd,
	onExit func(),
	tmuxName string,
	meta SessionMeta,
) *Session {
	return &Session{
		id:          id,
		opts:        opts,
		ptmx:        ptmx,
		cmd:         cmd,
		tmux:        opts.Tmux,
		tmuxName:    tmuxName,
		meta:        meta,
		name:        opts.Name,
		attachments: map[Sink]uint16{},
		replay:      NewRing(opts.ReplayBytes),
		onExit:      onExit,
	}
}

func defaultName(id, agent, name string) string {
	if name = strings.TrimSpace(name); name != "" {
		return name
	}
	if agent = strings.TrimSpace(agent); agent != "" {
		return agent
	}
	return id
}

func normalizeCols(cols uint16) uint16 {
	if cols == 0 {
		return 80
	}
	return cols
}

func normalizeRows(rows uint16) uint16 {
	if rows == 0 {
		return 24
	}
	return rows
}

// ID returns the session identifier.
func (s *Session) ID() string { return s.id }

// Name returns the mutable display name.
func (s *Session) Name() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.name
}

// Agent returns the agent name recorded at creation.
func (s *Session) Agent() string { return s.opts.AgentName }

// AttachmentCount reports how many clients are attached.
func (s *Session) AttachmentCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.attachments)
}

// markIdle starts the idle clock for a restored session that has no client yet,
// so the janitor can eventually reclaim it.
func (s *Session) markIdle() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.attachments) == 0 && s.idleSince.IsZero() {
		s.idleSince = time.Now()
	}
}

// SetName changes the display name, persists it in tmux metadata, and
// broadcasts it to attached clients. The session id never changes.
func (s *Session) SetName(name string) bool {
	name = strings.TrimSpace(name)
	if name == "" || utf8.RuneCountInString(name) > 64 {
		return false
	}

	s.mu.Lock()
	s.name = name
	s.meta.Name = name
	targets := s.snapshotLocked()
	tmux := s.tmux
	meta := s.meta
	s.mu.Unlock()

	if tmux != nil && meta.Tmux != "" {
		_ = tmux.SaveMeta(meta)
	}

	payload := []byte(name)
	for _, target := range targets {
		target.sink.TryEnqueue(Frame{Op: OpName, Slot: target.slot, Data: payload})
	}
	return true
}

// Run reads PTY output until the process ends, then reports its exit code.
func (s *Session) Run() {
	if s.host != nil {
		s.runHost()
		return
	}

	buf := make([]byte, 32*1024)
	for {
		n, err := s.ptmx.Read(buf)
		if n > 0 {
			chunk := make([]byte, n)
			copy(chunk, buf[:n])
			s.emit(chunk)
		}
		if err != nil {
			break
		}
	}

	code := 0
	if s.cmd != nil {
		if err := s.cmd.Wait(); err != nil {
			code = exitCode(err)
		}
	}
	s.finish(code)
}

// finish marks the process exited, broadcasts the exit code, and schedules
// reclamation after a short grace period.
func (s *Session) finish(code int) {
	s.mu.Lock()
	if s.closed || s.exited {
		s.mu.Unlock()
		return
	}
	s.exited = true
	s.waited = true
	s.exitCode = code
	targets := s.snapshotLocked()
	s.mu.Unlock()

	if s.ptmx != nil {
		_ = s.ptmx.Close()
	}
	payload := ExitPayload(code)
	for _, target := range targets {
		target.sink.TryEnqueue(Frame{Op: OpExit, Slot: target.slot, Data: payload})
	}

	time.AfterFunc(exitGrace, s.Close)
}

// Attach binds a client sink to a slot and replays the rebuild buffer. The
// hello/snapshot frames are queued under the session lock so PTY output cannot
// slip in front of them on the client's queue.
func (s *Session) Attach(sink Sink, slot uint16) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return false
	}
	s.attachments[sink] = slot
	s.idleSince = time.Time{}

	if !sink.TryEnqueue(Frame{Op: OpAttached, Slot: slot, Data: []byte(s.id)}) {
		delete(s.attachments, sink)
		return false
	}
	if !sink.TryEnqueue(Frame{Op: OpName, Slot: slot, Data: []byte(s.name)}) {
		delete(s.attachments, sink)
		return false
	}
	if snapshot := s.replay.Snapshot(); len(snapshot) > 0 {
		if !sink.TryEnqueue(Frame{Op: OpSnapshot, Slot: slot, Data: snapshot}) {
			delete(s.attachments, sink)
			return false
		}
	}
	if s.exited {
		sink.TryEnqueue(Frame{Op: OpExit, Slot: slot, Data: ExitPayload(s.exitCode)})
	}
	return true
}

// Detach removes a client without terminating the session.
func (s *Session) Detach(sink Sink) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.attachments, sink)
	if len(s.attachments) == 0 && s.idleSince.IsZero() {
		s.idleSince = time.Now()
	}
}

// Write sends client input to the PTY.
func (s *Session) Write(p []byte) (int, error) {
	s.mu.Lock()
	closed := s.closed || s.exited
	host := s.host
	s.mu.Unlock()
	if closed {
		return 0, ErrClosed
	}
	if host != nil {
		if err := host.SendInput(p); err != nil {
			return 0, err
		}
		return len(p), nil
	}
	return s.ptmx.Write(p)
}

// Resize changes the PTY window size and signals the child.
func (s *Session) Resize(rows, cols uint16) error {
	if rows == 0 || cols == 0 {
		return nil
	}
	s.mu.Lock()
	closed := s.closed || s.exited
	host := s.host
	s.mu.Unlock()
	if closed {
		return ErrClosed
	}
	if host != nil {
		return host.Resize(rows, cols)
	}
	return pty.Setsize(s.ptmx, &pty.Winsize{Rows: rows, Cols: cols})
}

// Close terminates the session, reporting exit code -1 to attached clients.
func (s *Session) Close() {
	s.CloseWithCode(-1)
}

// CloseWithCode terminates the process group, reports the exit code if the
// process had not already exited, and removes the session. Client WebSocket
// connections stay open so the final output remains readable.
func (s *Session) CloseWithCode(code int) {
	s.closeOnce.Do(func() {
		s.mu.Lock()
		alreadyExited := s.exited
		waited := s.waited
		s.closed = true
		s.exited = true
		targets := s.snapshotLocked()
		s.attachments = map[Sink]uint16{}
		host := s.host
		home := s.home
		hostMeta := s.hostMeta
		tmux := s.tmux
		tmuxName := s.tmuxName
		s.mu.Unlock()

		if !alreadyExited {
			payload := ExitPayload(code)
			for _, target := range targets {
				target.sink.TryEnqueue(Frame{Op: OpExit, Slot: target.slot, Data: payload})
			}
		}

		if host != nil {
			_ = host.Kill()
			_ = host.Close()
			deleteHostMeta(home, hostMeta.ID)
		}

		if s.ptmx != nil {
			_ = s.ptmx.Close()
		}
		// Only signal the group when Wait has not reaped the child yet; killing a
		// reaped pid could target a recycled process group.
		if !waited && s.cmd != nil && s.cmd.Process != nil {
			killProcessGroup(s.cmd.Process.Pid)
		}
		if tmux != nil && tmuxName != "" {
			tmux.Kill(tmuxName)
			tmux.DeleteMeta(tmuxName)
		}
		if s.onExit != nil {
			s.onExit()
		}
	})
}

// runHost reads frames from the detached host.
func (s *Session) runHost() {
	for {
		frame, err := s.host.ReadFrame()
		if err != nil {
			break
		}
		switch frame.Type {
		case sessionhost.TypeOutput:
			s.emit(frame.Data)
		case sessionhost.TypeExit:
			if code, ok := sessionhost.DecodeExit(frame.Data); ok {
				s.finishHost(code)
				return
			}
		}
	}
	s.finishHost(1)
}

func (s *Session) finishHost(code int) {
	s.mu.Lock()
	if s.closed || s.exited {
		s.mu.Unlock()
		return
	}
	s.exited = true
	s.waited = true
	s.exitCode = code
	targets := s.snapshotLocked()
	s.mu.Unlock()

	payload := ExitPayload(code)
	for _, target := range targets {
		target.sink.TryEnqueue(Frame{Op: OpExit, Slot: target.slot, Data: payload})
	}
	time.AfterFunc(exitGrace, s.Close)
}

// IsClosed reports whether the session has been torn down.
func (s *Session) IsClosed() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.closed
}

// IsExited reports whether the process has ended but the session is still
// replayable.
func (s *Session) IsExited() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.exited
}

// IdleFor reports how long the session has had no attached client.
func (s *Session) IdleFor(now time.Time) time.Duration {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.attachments) > 0 || s.idleSince.IsZero() {
		return 0
	}
	return now.Sub(s.idleSince)
}

// emit records output in the replay buffer and forwards it. Recording and
// target collection happen under the session lock, so a client either gets the
// bytes in its snapshot or after it, never in between.
func (s *Session) emit(data []byte) {
	targets := s.collect(func() { s.replay.Write(data) })
	deliver(targets, OpOutput, data)
}

func (s *Session) collect(record func()) []attachment {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return nil
	}
	if record != nil {
		record()
	}
	return s.snapshotLocked()
}

func (s *Session) snapshotLocked() []attachment {
	targets := make([]attachment, 0, len(s.attachments))
	for sink, slot := range s.attachments {
		targets = append(targets, attachment{sink: sink, slot: slot})
	}
	return targets
}

func deliver(targets []attachment, op byte, data []byte) {
	for _, target := range targets {
		if target.sink.Enqueue(Frame{Op: op, Slot: target.slot, Data: data}) {
			continue
		}
		// The client is gone or too slow; drop it so it can reconnect and get a
		// snapshot instead of stalling the PTY reader.
		target.sink.Close(1013, "slow consumer")
	}
}

func exitCode(err error) int {
	if err == nil {
		return 0
	}
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		if code := exitErr.ExitCode(); code >= 0 {
			return code
		}
		return 1
	}
	return 1
}
