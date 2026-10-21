package sessionhost

import (
	"os/exec"
	"strconv"
)

// StartConfig describes a detached host process.
type StartConfig struct {
	Binary      string
	ID          string
	Socket      string
	MetaPath    string
	Cwd         string
	Cols        uint16
	Rows        uint16
	ReplayBytes int
	Command     string
	Args        []string
	Env         []string
}

// Process is a started host process.
type Process struct {
	cmd *exec.Cmd
}

// Start spawns the host detached from the daemon so it survives a daemon exit.
func Start(cfg StartConfig) (*Process, error) {
	args := []string{
		"host",
		"--id", cfg.ID,
		"--socket", cfg.Socket,
		"--meta", cfg.MetaPath,
		"--cols", strconv.Itoa(int(cfg.Cols)),
		"--rows", strconv.Itoa(int(cfg.Rows)),
		"--replay", strconv.Itoa(cfg.ReplayBytes),
	}
	if cfg.Cwd != "" {
		args = append(args, "--cwd", cfg.Cwd)
	}
	args = append(args, "--", cfg.Command)
	args = append(args, cfg.Args...)

	cmd := exec.Command(cfg.Binary, args...)
	cmd.Env = append(cfg.Env, "TERM=xterm-256color")
	cmd.SysProcAttr = detachAttributes()
	cmd.Stdin = nil
	cmd.Stdout = nil
	cmd.Stderr = nil
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	// Reap the host while the daemon is alive. After the daemon exits the host
	// is reparented and reaped by init.
	go func() { _ = cmd.Wait() }()
	return &Process{cmd: cmd}, nil
}

// PID returns the host process id.
func (p *Process) PID() int {
	if p == nil || p.cmd == nil || p.cmd.Process == nil {
		return 0
	}
	return p.cmd.Process.Pid
}

// Kill forcibly terminates the host. Prefer Client.Kill for a graceful stop.
func (p *Process) Kill() {
	if p == nil || p.cmd == nil || p.cmd.Process == nil {
		return
	}
	_ = p.cmd.Process.Kill()
}
