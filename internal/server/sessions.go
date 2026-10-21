package server

import (
	"encoding/json"
	"net/http"
)

func (s *Server) handleSessions(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdminOrDevice(w, r) {
		return
	}
	writeJSON(w, http.StatusOK, s.manager.List())
}

func (s *Server) handleSessionRename(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdminOrDevice(w, r) {
		return
	}
	var request struct {
		ID   string `json:"id"`
		Name string `json:"name"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&request); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "invalid request"})
		return
	}
	if request.ID == "" || request.Name == "" {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "id and name are required"})
		return
	}
	if !s.manager.Rename(request.ID, request.Name) {
		writeJSON(w, http.StatusNotFound, map[string]any{"error": "session not found or invalid name"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (s *Server) handleSessionClose(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdminOrDevice(w, r) {
		return
	}
	var request struct {
		ID string `json:"id"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&request); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "invalid request"})
		return
	}
	if request.ID == "" {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "missing session id"})
		return
	}
	if s.manager.Get(request.ID) == nil {
		writeJSON(w, http.StatusNotFound, map[string]any{"error": "session not found"})
		return
	}
	s.manager.Remove(request.ID)
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}
