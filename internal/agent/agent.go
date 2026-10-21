// Package agent describes the coding-agent CLIs agentty can launch.
package agent

import (
	"os/exec"
	"strings"
)

// Spec is one agent entry. Command is the executable; Args are passed before
// the interactive TUI starts.
type Spec struct {
	Name    string   `json:"name"`
	Command string   `json:"command"`
	Args    []string `json:"args,omitempty"`
}

// Builtins is the default catalog. Binary names follow each project's CLI
// package; entries whose binary is missing are shown as unavailable in the UI.
func Builtins() []Spec {
	return []Spec{
		{Name: "claude", Command: "claude"},
		{Name: "codex", Command: "codex"},
		{Name: "opencode", Command: "opencode"},
		{Name: "pi", Command: "pi"},
		{Name: "gemini", Command: "gemini"},
		{Name: "copilot", Command: "copilot"},
		{Name: "cursor", Command: "cursor-agent"},
		{Name: "aider", Command: "aider"},
		{Name: "amp", Command: "amp"},
		{Name: "goose", Command: "goose", Args: []string{"session"}},
		{Name: "crush", Command: "crush"},
		{Name: "qwen", Command: "qwen"},
		{Name: "droid", Command: "droid"},
		{Name: "openhands", Command: "openhands"},
		{Name: "interpreter", Command: "interpreter"},
		{Name: "plandex", Command: "plandex"},
		{Name: "kimi", Command: "kimi"},
		{Name: "mimo", Command: "mimo"},
		{Name: "zcode", Command: "zcode"},
		{Name: "deveco", Command: "deveco"},
		{Name: "mothx", Command: "mothx"},
		{Name: "omo", Command: "omo"},
		{Name: "omp", Command: "omp"},
		{Name: "cline", Command: "cline"},
		{Name: "codebuddy", Command: "codebuddy"},
		{Name: "atomcode", Command: "atomcode"},
		{Name: "snow", Command: "snow"},
		{Name: "antigravity", Command: "agy"},
	}
}

// Merge puts custom entries first so they can override a built-in by name.
func Merge(builtins, custom []Spec) []Spec {
	merged := make([]Spec, 0, len(builtins)+len(custom))
	seen := make(map[string]struct{}, len(builtins)+len(custom))

	for _, spec := range custom {
		name := strings.ToLower(strings.TrimSpace(spec.Name))
		if name == "" || strings.TrimSpace(spec.Command) == "" {
			continue
		}
		if _, exists := seen[name]; exists {
			continue
		}
		seen[name] = struct{}{}
		merged = append(merged, spec)
	}
	for _, spec := range builtins {
		name := strings.ToLower(strings.TrimSpace(spec.Name))
		if _, exists := seen[name]; exists {
			continue
		}
		seen[name] = struct{}{}
		merged = append(merged, spec)
	}
	return merged
}

// Find looks up an agent by name, case-insensitively.
func Find(specs []Spec, name string) (Spec, bool) {
	name = strings.ToLower(strings.TrimSpace(name))
	for _, spec := range specs {
		if strings.ToLower(strings.TrimSpace(spec.Name)) == name {
			return spec, true
		}
	}
	return Spec{}, false
}

// Available reports whether the agent binary is on PATH.
func Available(spec Spec) bool {
	if strings.TrimSpace(spec.Command) == "" {
		return false
	}
	_, err := exec.LookPath(spec.Command)
	return err == nil
}
