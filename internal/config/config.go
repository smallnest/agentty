// Package config reads agentty settings from the environment.
package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/smallnest/agentty/internal/agent"
)

// Config holds runtime settings for the agent daemon.
type Config struct {
	// Listen is the HTTP/WebSocket listen address.
	Listen string

	// DefaultAgent is used when a client does not name one.
	DefaultAgent string

	// AllowCmd permits the raw `cmd` create field. When false only the built-in
	// agent names are accepted.
	AllowCmd bool

	// WorkingDir is the default working directory for spawned agents.
	WorkingDir string

	// ReplayBytes bounds the per-session output ring buffer used to rebuild the
	// screen when a client attaches.
	ReplayBytes int

	// SendQueue bounds queued outbound frames per client before backpressure
	// turns into a slow-consumer shed.
	SendQueue int

	// IdleTimeout closes sessions with no attached client for this long. Zero
	// disables the janitor.
	IdleTimeout time.Duration

	// Optional TLS for direct HTTPS/WSS.
	TLSCertFile string
	TLSKeyFile  string

	// Auth settings. AuthEnabled gates the browser behind a pairing token or
	// password; CookieSecure forces the Secure flag when TLS terminates upstream.
	AuthEnabled  bool
	Password     string
	SessionTTL   time.Duration
	PublicURL    string
	CookieSecure bool
	// PairingToken overrides the persisted token (useful for tests/CI).
	PairingToken string

	// AdminToken authenticates local CLI commands (agentty pair/devices) against
	// the running daemon. It comes from $AGENTTY_HOME/local-credential.
	AdminToken string

	// Persist runs each session in a detached host so it survives a daemon
	// restart. Backend selects "auto", "host", "tmux", or "none".
	Persist bool
	Backend string

	// Agents is the built-in catalog merged with AGENTTY_AGENTS overrides.
	Agents []agent.Spec
	// AgentConfigError reports an invalid AGENTTY_AGENTS value.
	AgentConfigError string

	// AgenttyHome holds the persisted server id.
	AgenttyHome string

	// Interface forces the advertised LAN interface (e.g. en0, eth0). Empty
	// means auto-select the best physical interface.
	Interface string

	// ServerID is the stable identity used on the relay. Resolved at startup.
	ServerID string

	// Relay settings. When RelayEnabled is true agentty dials the relay outbound
	// and exposes its terminal to relay clients.
	RelayEnabled  bool
	RelayEndpoint string
	RelayUseTLS   bool
}

// FromEnv reads AGENTTY_* variables.
func FromEnv() Config {
	endpoint := envString("AGENTTY_RELAY_ENDPOINT", "")
	certFile := os.Getenv("AGENTTY_TLS_CERT_FILE")
	keyFile := os.Getenv("AGENTTY_TLS_KEY_FILE")
	customAgents, agentErr := parseAgents(os.Getenv("AGENTTY_AGENTS"))
	return Config{
		// Bind all interfaces by default so a phone on the same network can
		// reach the pairing QR. Set AGENTTY_LISTEN=127.0.0.1:7070 for local-only.
		Listen:           envString("AGENTTY_LISTEN", "0.0.0.0:7070"),
		DefaultAgent:     envString("AGENTTY_DEFAULT_AGENT", "shell"),
		AllowCmd:         envBool("AGENTTY_ALLOW_CMD", true),
		WorkingDir:       envString("AGENTTY_WORKING_DIR", defaultHome()),
		ReplayBytes:      envInt("AGENTTY_REPLAY_BYTES", 256*1024),
		SendQueue:        envInt("AGENTTY_SEND_QUEUE", 64),
		IdleTimeout:      time.Duration(envInt("AGENTTY_IDLE_TIMEOUT_SECONDS", 1800)) * time.Second,
		TLSCertFile:      certFile,
		TLSKeyFile:       keyFile,
		AgenttyHome:      envString("AGENTTY_HOME", defaultAgenttyHome()),
		Interface:        envString("AGENTTY_INTERFACE", ""),
		ServerID:         envString("AGENTTY_SERVER_ID", ""),
		RelayEnabled:     envBool("AGENTTY_RELAY_ENABLED", endpoint != ""),
		RelayEndpoint:    endpoint,
		RelayUseTLS:      envBool("AGENTTY_RELAY_USE_TLS", strings.HasSuffix(endpoint, ":443")),
		AuthEnabled:      envBool("AGENTTY_AUTH_ENABLED", true),
		Password:         os.Getenv("AGENTTY_PASSWORD"),
		SessionTTL:       time.Duration(envInt("AGENTTY_SESSION_TTL_SECONDS", 30*24*3600)) * time.Second,
		PublicURL:        os.Getenv("AGENTTY_PUBLIC_URL"),
		CookieSecure:     envBool("AGENTTY_COOKIE_SECURE", certFile != "" && keyFile != ""),
		PairingToken:     os.Getenv("AGENTTY_PAIRING_TOKEN"),
		Persist:          envBool("AGENTTY_PERSIST", true),
		Backend:          envString("AGENTTY_BACKEND", "auto"),
		Agents:           agent.Merge(agent.Builtins(), customAgents),
		AgentConfigError: agentErr,
	}
}

func defaultHome() string {
	if home, err := os.UserHomeDir(); err == nil {
		return home
	}
	return "."
}

func defaultAgenttyHome() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ".agentty"
	}
	return filepath.Join(home, ".agentty")
}

func parseAgents(raw string) ([]agent.Spec, string) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, ""
	}
	var specs []agent.Spec
	if err := json.Unmarshal([]byte(raw), &specs); err != nil {
		return nil, "AGENTTY_AGENTS is not valid JSON: " + err.Error()
	}
	out := make([]agent.Spec, 0, len(specs))
	for _, spec := range specs {
		spec.Name = strings.TrimSpace(spec.Name)
		spec.Command = strings.TrimSpace(spec.Command)
		if spec.Name == "" || spec.Command == "" {
			continue
		}
		out = append(out, spec)
	}
	return out, ""
}

func envString(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func envInt(key string, fallback int) int {
	v := os.Getenv(key)
	if v == "" {
		return fallback
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		return fallback
	}
	return n
}

func envBool(key string, fallback bool) bool {
	v := os.Getenv(key)
	if v == "" {
		return fallback
	}
	b, err := strconv.ParseBool(v)
	if err != nil {
		return fallback
	}
	return b
}
