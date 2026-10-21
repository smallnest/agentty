package terminal

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/smallnest/agentty/internal/sessionhost"
)

// hostMeta is persisted for built-in (non-tmux) sessions.
type hostMeta struct {
	ID        string    `json:"id"`
	Name      string    `json:"name"`
	Agent     string    `json:"agent"`
	Command   string    `json:"command"`
	Args      []string  `json:"args"`
	Cwd       string    `json:"cwd"`
	Socket    string    `json:"socket"`
	HostPID   int       `json:"hostPid"`
	CreatedAt time.Time `json:"createdAt"`
}

func hostMetaPath(home, id string) string {
	safe := tmuxUnsafe.ReplaceAllString(id, "_")
	return filepath.Join(home, "sessions", "host-"+safe+".json")
}

// hostSocketPath keeps the Unix socket under the platform's sun_path limit. On
// long homes it falls back to a hashed path in the system temp directory.
func hostSocketPath(home, id string) string {
	safe := tmuxUnsafe.ReplaceAllString(id, "_")
	candidate := filepath.Join(home, "sessions", "host-"+safe+".sock")
	if len(candidate) < 100 {
		return candidate
	}
	sum := sha256.Sum256([]byte(home + "\x00" + id))
	return filepath.Join(os.TempDir(), "agentty-"+hex.EncodeToString(sum[:8])+".sock")
}

func saveHostMeta(home string, meta hostMeta) error {
	if home == "" {
		return nil
	}
	dir := filepath.Join(home, "sessions")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	data, err := json.MarshalIndent(meta, "", "  ")
	if err != nil {
		return err
	}
	path := hostMetaPath(home, meta.ID)
	temp := path + ".tmp"
	if err := os.WriteFile(temp, data, 0o600); err != nil {
		return err
	}
	return os.Rename(temp, path)
}

func loadHostMetas(home string) ([]hostMeta, error) {
	dir := filepath.Join(home, "sessions")
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	metas := make([]hostMeta, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasPrefix(entry.Name(), "host-") || !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		data, err := os.ReadFile(filepath.Join(dir, entry.Name()))
		if err != nil {
			continue
		}
		var meta hostMeta
		if err := json.Unmarshal(data, &meta); err != nil {
			continue
		}
		if meta.ID == "" || meta.Socket == "" {
			continue
		}
		metas = append(metas, meta)
	}
	return metas, nil
}

func deleteHostMeta(home, id string) {
	if home == "" {
		return
	}
	_ = os.Remove(hostMetaPath(home, id))
}

// startHost spawns a detached host process and connects to it.
func startHost(id string, opts Options, onExit func()) (*Session, error) {
	cols, rows := normalizeCols(opts.Cols), normalizeRows(opts.Rows)
	socket := hostSocketPath(opts.Home, id)
	metaPath := hostMetaPath(opts.Home, id)
	meta := hostMeta{
		ID:        id,
		Name:      opts.Name,
		Agent:     opts.AgentName,
		Command:   opts.Command.Command,
		Args:      opts.Command.Args,
		Cwd:       opts.Cwd,
		Socket:    socket,
		CreatedAt: time.Now(),
	}
	if err := saveHostMeta(opts.Home, meta); err != nil {
		return nil, err
	}

	binary, err := os.Executable()
	if err != nil {
		deleteHostMeta(opts.Home, id)
		return nil, err
	}

	process, err := sessionhost.Start(sessionhost.StartConfig{
		Binary:      binary,
		ID:          id,
		Socket:      socket,
		MetaPath:    metaPath,
		Cwd:         opts.Cwd,
		Cols:        cols,
		Rows:        rows,
		ReplayBytes: opts.ReplayBytes,
		Command:     opts.Command.Command,
		Args:        opts.Command.Args,
		Env:         os.Environ(),
	})
	if err != nil {
		deleteHostMeta(opts.Home, id)
		return nil, err
	}

	client, err := sessionhost.Dial(socket, 10*time.Second)
	if err != nil {
		process.Kill()
		deleteHostMeta(opts.Home, id)
		return nil, err
	}

	meta.HostPID = process.PID()
	_ = saveHostMeta(opts.Home, meta)

	session := newSessionObject(id, opts, nil, nil, onExit, "", SessionMeta{})
	session.host = client
	session.hostMeta = meta
	session.home = opts.Home
	return session, nil
}

// restoreHostSessions reconnects to hosts recorded before a daemon restart.
func restoreHostSessions(manager *Manager, home string, replayBytes int) {
	metas, err := loadHostMetas(home)
	if err != nil {
		return
	}
	for _, meta := range metas {
		client, err := sessionhost.Dial(meta.Socket, 500*time.Millisecond)
		if err != nil {
			deleteHostMeta(home, meta.ID)
			continue
		}

		opts := Options{
			Home:        home,
			ReplayBytes: replayBytes,
			AgentName:   meta.Agent,
			Name:        meta.Name,
		}
		session := newSessionObject(meta.ID, opts, nil, nil, func() { manager.forget(meta.ID) }, "", SessionMeta{})
		session.host = client
		session.hostMeta = meta
		session.home = home
		session.markIdle()
		manager.add(session)
		go session.Run()
	}
}
