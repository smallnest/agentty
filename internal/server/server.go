// Package server exposes agentty's HTTP surface: the /ws terminal multiplexer,
// /health, /api/agents, and the embedded web terminal.
package server

import (
	"encoding/json"
	"io/fs"
	"net/http"

	"github.com/gorilla/websocket"

	"github.com/smallnest/agentty/internal/agent"
	"github.com/smallnest/agentty/internal/auth"
	"github.com/smallnest/agentty/internal/config"
	"github.com/smallnest/agentty/internal/terminal"
	"github.com/smallnest/agentty/internal/web"
)

// Server is the agentty HTTP server.
type Server struct {
	config   config.Config
	manager  *terminal.Manager
	auth     *auth.Store
	upgrader websocket.Upgrader
}

// New builds a Server. A nil store disables authentication.
func New(cfg config.Config, manager *terminal.Manager, store *auth.Store) *Server {
	return &Server{
		config:  cfg,
		manager: manager,
		auth:    store,
		upgrader: websocket.Upgrader{
			// Local-first daemon: any origin may connect. Authentication is the
			// boundary, not Origin.
			CheckOrigin: func(*http.Request) bool { return true },
		},
	}
}

// Handler returns the root HTTP handler.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/health", s.handleHealth)
	mux.HandleFunc("/api/auth/status", s.handleAuthStatus)
	mux.HandleFunc("/api/auth/login", s.handleAuthLogin)
	mux.HandleFunc("/api/auth/logout", s.handleAuthLogout)
	mux.HandleFunc("/api/pairing/code", s.handlePairingCode)
	mux.HandleFunc("/api/devices", s.handleDevices)
	mux.HandleFunc("/api/devices/revoke", s.handleDeviceRevoke)
	mux.HandleFunc("/api/sessions", s.handleSessions)
	mux.HandleFunc("/api/sessions/rename", s.handleSessionRename)
	mux.HandleFunc("/api/sessions/close", s.handleSessionClose)
	mux.HandleFunc("/api/agents", s.handleAgents)
	mux.HandleFunc("/ws", s.handleWS)

	static, err := fs.Sub(web.Static, "static")
	if err != nil {
		panic(err)
	}
	mux.Handle("/", http.FileServer(http.FS(static)))
	return mux
}

func (s *Server) handleHealth(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write([]byte(`{"status":"ok"}`))
}

type agentInfo struct {
	Name      string `json:"name"`
	Command   string `json:"command"`
	Available bool   `json:"available"`
}

func (s *Server) handleAgents(w http.ResponseWriter, r *http.Request) {
	if !s.requireAuth(w, r) {
		return
	}
	specs := s.config.Agents
	if len(specs) == 0 {
		specs = agent.Builtins()
	}
	agents := []agentInfo{
		{Name: "shell", Command: shellPath(), Available: true},
	}
	for _, spec := range specs {
		agents = append(agents, agentInfo{
			Name:      spec.Name,
			Command:   spec.Command,
			Available: agent.Available(spec),
		})
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(agents)
}

func (s *Server) handleWS(w http.ResponseWriter, r *http.Request) {
	if !s.requireAuth(w, r) {
		return
	}
	ws, err := s.upgrader.Upgrade(w, r, nil)
	if err != nil {
		return
	}
	client := NewClient(s.manager, s.config, NewWSTransport(ws))
	client.Run()
}
