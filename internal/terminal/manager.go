package terminal

import (
	"sort"
	"sync"
	"time"
)

// Manager owns named sessions so a client can reconnect to a running PTY.
type Manager struct {
	mu          sync.Mutex
	sessions    map[string]*Session
	idleTimeout time.Duration
	tmux        *TmuxBackend
	home        string
	backend     string
}

// NewManager creates an empty session manager.
func NewManager(idleTimeout time.Duration) *Manager {
	return &Manager{
		sessions:    map[string]*Session{},
		idleTimeout: idleTimeout,
	}
}

// Acquire returns the live session for id, or creates one from opts. The bool
// reports whether a new process was spawned.
func (m *Manager) Acquire(id string, opts Options) (*Session, bool, error) {
	m.mu.Lock()
	tmux := m.tmux
	home := m.home
	backend := m.backend
	if existing := m.sessions[id]; existing != nil {
		if !existing.IsClosed() {
			m.mu.Unlock()
			return existing, false, nil
		}
		delete(m.sessions, id)
	}
	m.mu.Unlock()

	if opts.Tmux == nil {
		opts.Tmux = tmux
	}
	if opts.Home == "" {
		opts.Home = home
	}
	if opts.Backend == "" {
		opts.Backend = backend
	}

	session, err := newSession(id, opts, func() { m.forget(id) })
	if err != nil {
		return nil, false, err
	}

	// Another connection may have created the same id while we were spawning.
	m.mu.Lock()
	if existing := m.sessions[id]; existing != nil && !existing.IsClosed() {
		m.mu.Unlock()
		session.Close()
		return existing, false, nil
	}
	m.sessions[id] = session
	m.mu.Unlock()

	go session.Run()
	return session, true, nil
}

// Get returns a live session by id.
func (m *Manager) Get(id string) *Session {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.sessions[id]
}

// Remove terminates and forgets a session.
func (m *Manager) Remove(id string) {
	m.mu.Lock()
	session := m.sessions[id]
	delete(m.sessions, id)
	m.mu.Unlock()
	if session != nil {
		session.Close()
	}
}

// SetHome configures the directory used for persisted host sessions.
func (m *Manager) SetHome(home string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.home = home
}

// SetBackend selects "auto", "host", "tmux", or "none".
func (m *Manager) SetBackend(backend string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.backend = backend
}

func (m *Manager) add(session *Session) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, exists := m.sessions[session.ID()]; !exists {
		m.sessions[session.ID()] = session
	}
}

// Restore reattaches tmux-backed and detached-host sessions recorded before a
// daemon restart. It also installs tmux as the backend for future Acquire calls.
func (m *Manager) Restore(tmux *TmuxBackend, replayBytes int) error {
	if tmux != nil && tmux.Available() {
		m.mu.Lock()
		m.tmux = tmux
		m.mu.Unlock()

		metas, err := tmux.LoadMetas()
		if err != nil {
			return err
		}
		for _, meta := range metas {
			if !tmux.Has(meta.Tmux) {
				tmux.DeleteMeta(meta.Tmux)
				continue
			}
			session, err := startAttached(meta.ID, Options{
				Tmux:        tmux,
				ReplayBytes: replayBytes,
				AgentName:   meta.Agent,
				Name:        meta.Name,
			}, meta.Tmux, meta, func() { m.forget(meta.ID) })
			if err != nil {
				continue
			}
			session.markIdle()
			m.add(session)
			go session.Run()
		}
	}

	m.mu.Lock()
	home := m.home
	m.mu.Unlock()
	if home != "" {
		restoreHostSessions(m, home, replayBytes)
	}
	return nil
}

// SessionView is the redacted session shape returned by the admin API.
type SessionView struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	Agent   string `json:"agent"`
	Exited  bool   `json:"exited"`
	Clients int    `json:"clients"`
}

// List returns a snapshot of tracked sessions, sorted by id.
func (m *Manager) List() []SessionView {
	m.mu.Lock()
	sessions := make([]*Session, 0, len(m.sessions))
	for _, session := range m.sessions {
		sessions = append(sessions, session)
	}
	m.mu.Unlock()

	views := make([]SessionView, 0, len(sessions))
	for _, session := range sessions {
		// Exited sessions stay attachable for a short grace so a late client can
		// read the final screen, but they are not listed.
		if session.IsExited() {
			continue
		}
		views = append(views, SessionView{
			ID:      session.ID(),
			Name:    session.Name(),
			Agent:   session.Agent(),
			Exited:  session.IsExited(),
			Clients: session.AttachmentCount(),
		})
	}
	sort.Slice(views, func(i, j int) bool { return views[i].ID < views[j].ID })
	return views
}

// Rename changes a session's display name. The id stays immutable.
func (m *Manager) Rename(id, name string) bool {
	session := m.Get(id)
	if session == nil {
		return false
	}
	return session.SetName(name)
}

// Len reports the number of tracked sessions.
func (m *Manager) Len() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.sessions)
}

// CloseAll terminates every tracked session.
func (m *Manager) CloseAll() {
	m.mu.Lock()
	sessions := m.sessions
	m.sessions = map[string]*Session{}
	m.mu.Unlock()
	for _, session := range sessions {
		session.Close()
	}
}

// Janitor periodically closes sessions that no client has attached to for the
// configured idle timeout.
func (m *Manager) Janitor(interval time.Duration) {
	if interval <= 0 || m.idleTimeout <= 0 {
		return
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for now := range ticker.C {
		for _, session := range m.snapshot() {
			if session.IdleFor(now) >= m.idleTimeout {
				m.Remove(session.ID())
			}
		}
	}
}

func (m *Manager) forget(id string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if session := m.sessions[id]; session != nil && session.IsClosed() {
		delete(m.sessions, id)
	}
}

func (m *Manager) snapshot() []*Session {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]*Session, 0, len(m.sessions))
	for _, session := range m.sessions {
		out = append(out, session)
	}
	return out
}
