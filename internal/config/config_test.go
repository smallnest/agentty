package config

import (
	"testing"

	"github.com/smallnest/agentty/internal/agent"
)

func TestParseAgents(t *testing.T) {
	specs, errMsg := parseAgents(`[{"name":"local","command":"local","args":["--x"]}]`)
	if errMsg != "" || len(specs) != 1 || specs[0].Command != "local" {
		t.Fatalf("specs = %+v, err = %q", specs, errMsg)
	}

	if _, errMsg := parseAgents(`{not json`); errMsg == "" {
		t.Fatal("expected an error for invalid JSON")
	}

	specs, errMsg = parseAgents(`[
		{"name":"","command":"x"},
		{"name":"ok","command":""},
		{"name":"ok","command":"ok"}
	]`)
	if errMsg != "" || len(specs) != 1 || specs[0].Name != "ok" {
		t.Fatalf("filtered = %+v, err = %q", specs, errMsg)
	}
}

func TestFromEnvMergesCustomAgents(t *testing.T) {
	t.Setenv("AGENTTY_AGENTS", `[{"name":"claude","command":"my-claude","args":["--yolo"]}]`)

	cfg := FromEnv()
	if cfg.AgentConfigError != "" {
		t.Fatalf("unexpected error: %s", cfg.AgentConfigError)
	}

	spec, ok := agent.Find(cfg.Agents, "claude")
	if !ok || spec.Command != "my-claude" || len(spec.Args) != 1 || spec.Args[0] != "--yolo" {
		t.Fatalf("claude override = %+v, %v", spec, ok)
	}
	if _, ok := agent.Find(cfg.Agents, "gemini"); !ok {
		t.Fatal("built-in gemini should remain")
	}
}

func TestFromEnvReportsInvalidAgents(t *testing.T) {
	t.Setenv("AGENTTY_AGENTS", `{broken`)
	cfg := FromEnv()
	if cfg.AgentConfigError == "" {
		t.Fatal("expected an agent config error")
	}
	if _, ok := agent.Find(cfg.Agents, "claude"); !ok {
		t.Fatal("built-ins should still be present")
	}
}
