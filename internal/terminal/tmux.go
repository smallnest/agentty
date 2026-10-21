package terminal

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

var tmuxUnsafe = regexp.MustCompile(`[^A-Za-z0-9_-]`)

// SessionMeta records enough to reattach a tmux-backed session after a daemon
// restart.
type SessionMeta struct {
	ID        string    `json:"id"`
	Tmux      string    `json:"tmux"`
	Name      string    `json:"name"`
	Agent     string    `json:"agent"`
	Command   string    `json:"command"`
	Cwd       string    `json:"cwd"`
	CreatedAt time.Time `json:"createdAt"`
}

// TmuxBackend runs agents inside tmux so they survive a daemon restart. It is
// optional: when tmux is absent the daemon falls back to a direct PTY.
type TmuxBackend struct {
	home   string
	prefix string
}

// NewTmuxBackend creates a backend rooted at home. Prefix namespaces the tmux
// sessions this daemon creates.
func NewTmuxBackend(home, prefix string) *TmuxBackend {
	if prefix == "" {
		prefix = "agentty"
	}
	return &TmuxBackend{home: home, prefix: prefix}
}

// Available reports whether tmux is installed.
func (t *TmuxBackend) Available() bool {
	if t == nil {
		return false
	}
	_, err := exec.LookPath("tmux")
	return err == nil
}

// Name maps a session id to a safe tmux session name.
func (t *TmuxBackend) Name(sessionID string) string {
	prefix := "agentty"
	if t != nil && t.prefix != "" {
		prefix = t.prefix
	}
	return prefix + "_" + tmuxUnsafe.ReplaceAllString(sessionID, "_")
}

// Create starts a detached tmux session running command.
func (t *TmuxBackend) Create(sessionID string, command Command, cwd string, _, _ uint16) (string, error) {
	name := t.Name(sessionID)
	args := []string{"new-session", "-d", "-s", name}
	if cwd != "" {
		args = append(args, "-c", cwd)
	}
	args = append(args, "--", command.Command)
	args = append(args, command.Args...)
	if out, err := exec.Command("tmux", args...).CombinedOutput(); err != nil {
		return "", fmt.Errorf("tmux new-session: %s: %w", strings.TrimSpace(string(out)), err)
	}
	// Keep the session alive when the last client detaches; otherwise a daemon
	// restart would destroy the agent.
	_ = exec.Command("tmux", "set-option", "-t", name, "destroy-unattached", "off").Run()
	return name, nil
}

// Attach returns the command that attaches this process to the session.
func (t *TmuxBackend) Attach(name string) *exec.Cmd {
	return exec.Command("tmux", "attach-session", "-t", name)
}

// Kill destroys a tmux session.
func (t *TmuxBackend) Kill(name string) {
	if name == "" {
		return
	}
	_ = exec.Command("tmux", "kill-session", "-t", name).Run()
}

// Has reports whether a tmux session still exists.
func (t *TmuxBackend) Has(name string) bool {
	if name == "" {
		return false
	}
	return exec.Command("tmux", "has-session", "-t", name).Run() == nil
}

func (t *TmuxBackend) metaDir() string {
	return filepath.Join(t.home, "sessions")
}

// SaveMeta persists a session record for restart recovery.
func (t *TmuxBackend) SaveMeta(meta SessionMeta) error {
	if t.home == "" {
		return nil
	}
	if err := os.MkdirAll(t.metaDir(), 0o700); err != nil {
		return err
	}
	data, err := json.MarshalIndent(meta, "", "  ")
	if err != nil {
		return err
	}
	path := filepath.Join(t.metaDir(), meta.Tmux+".json")
	temp := path + ".tmp"
	if err := os.WriteFile(temp, data, 0o600); err != nil {
		return err
	}
	return os.Rename(temp, path)
}

// LoadMetas reads persisted session records.
func (t *TmuxBackend) LoadMetas() ([]SessionMeta, error) {
	if t.home == "" {
		return nil, nil
	}
	entries, err := os.ReadDir(t.metaDir())
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	metas := make([]SessionMeta, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		data, err := os.ReadFile(filepath.Join(t.metaDir(), entry.Name()))
		if err != nil {
			continue
		}
		var meta SessionMeta
		if err := json.Unmarshal(data, &meta); err != nil {
			continue
		}
		if meta.ID == "" || meta.Tmux == "" {
			continue
		}
		metas = append(metas, meta)
	}
	return metas, nil
}

// DeleteMeta removes a session record.
func (t *TmuxBackend) DeleteMeta(name string) {
	if t.home == "" || name == "" {
		return
	}
	_ = os.Remove(filepath.Join(t.metaDir(), name+".json"))
}
