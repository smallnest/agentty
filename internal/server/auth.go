package server

import (
	"bytes"
	"crypto/subtle"
	"encoding/json"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/mdp/qrterminal/v3"

	"github.com/smallnest/agentty/internal/auth"
)

const deviceCookie = "agentty_device"

func (s *Server) handleAuthStatus(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{
		"authenticated": s.authenticated(r),
		"mode":          s.auth.Mode(),
	})
}

// handleAuthLogin exchanges the bootstrap secret or a one-time pairing code for
// a device credential.
func (s *Server) handleAuthLogin(w http.ResponseWriter, r *http.Request) {
	if s.auth == nil {
		writeJSON(w, http.StatusOK, map[string]any{"ok": true})
		return
	}

	ip := clientIP(r)
	if !s.auth.Allow(ip) {
		writeJSON(w, http.StatusTooManyRequests, map[string]any{"error": "too many attempts"})
		return
	}

	var request struct {
		Secret string `json:"secret"`
		Name   string `json:"name"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&request); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "invalid request"})
		return
	}

	if !s.auth.TryVerifySlot() {
		writeJSON(w, http.StatusTooManyRequests, map[string]any{"error": "too many concurrent attempts"})
		return
	}
	deviceID, deviceSecret, ok := s.auth.Exchange(request.Secret, request.Name)
	s.auth.ReleaseVerifySlot()

	if !ok {
		s.auth.RecordFailure(ip)
		time.Sleep(auth.FailureDelay)
		writeJSON(w, http.StatusUnauthorized, map[string]any{"error": "invalid credentials"})
		return
	}

	s.setDeviceCookie(w, r, deviceID, deviceSecret)
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "device": deviceID})
}

// handleAuthLogout revokes the calling device and clears its cookie.
func (s *Server) handleAuthLogout(w http.ResponseWriter, r *http.Request) {
	if s.auth != nil {
		if id, _, ok := deviceFromRequest(r); ok {
			s.auth.RevokeDevice(id)
		}
	}
	s.clearDeviceCookie(w, r)
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (s *Server) handleDevices(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdminOrDevice(w, r) {
		return
	}
	writeJSON(w, http.StatusOK, s.auth.ListDevices())
}

func (s *Server) handleDeviceRevoke(w http.ResponseWriter, r *http.Request) {
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
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "missing device id"})
		return
	}

	s.auth.RevokeDevice(request.ID)
	if id, _, ok := deviceFromRequest(r); ok && id == request.ID {
		s.clearDeviceCookie(w, r)
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// handlePairingCode mints a one-time code and returns its URL and terminal QR.
// Browser sessions and the local admin credential can both mint codes.
func (s *Server) handlePairingCode(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdminOrDevice(w, r) {
		return
	}
	code, expiry := s.auth.NewPairingCode()
	link := s.baseURL(r) + "/#pair=" + code

	var qr bytes.Buffer
	qrterminal.GenerateHalfBlock(link, qrterminal.L, &qr)

	writeJSON(w, http.StatusOK, map[string]any{
		"code":      code,
		"expiresAt": expiry,
		"url":       link,
		"qr":        qr.String(),
	})
}

// authenticated accepts a device cookie (browsers) or a bearer id.secret (CLI).
func (s *Server) authenticated(r *http.Request) bool {
	if s.auth == nil {
		return true
	}
	id, secret, ok := deviceFromRequest(r)
	return ok && s.auth.AuthenticateDevice(id, secret)
}

func deviceFromRequest(r *http.Request) (string, string, bool) {
	if cookie, err := r.Cookie(deviceCookie); err == nil {
		if id, secret, ok := splitDevice(cookie.Value); ok {
			return id, secret, true
		}
	}
	if header := r.Header.Get("Authorization"); strings.HasPrefix(header, "Bearer ") {
		if id, secret, ok := splitDevice(strings.TrimSpace(strings.TrimPrefix(header, "Bearer "))); ok {
			return id, secret, true
		}
	}
	return "", "", false
}

func splitDevice(value string) (string, string, bool) {
	id, secret, ok := strings.Cut(value, ".")
	if !ok || id == "" || secret == "" {
		return "", "", false
	}
	return id, secret, true
}

func (s *Server) setDeviceCookie(w http.ResponseWriter, r *http.Request, id, secret string) {
	http.SetCookie(w, &http.Cookie{
		Name:     deviceCookie,
		Value:    id + "." + secret,
		Path:     "/",
		Expires:  time.Now().Add(s.config.SessionTTL),
		HttpOnly: true,
		SameSite: http.SameSiteStrictMode,
		Secure:   s.cookieSecure(r),
	})
}

func (s *Server) clearDeviceCookie(w http.ResponseWriter, r *http.Request) {
	http.SetCookie(w, &http.Cookie{
		Name:     deviceCookie,
		Value:    "",
		Path:     "/",
		MaxAge:   -1,
		HttpOnly: true,
		SameSite: http.SameSiteStrictMode,
		Secure:   s.cookieSecure(r),
	})
}

func (s *Server) requireAuth(w http.ResponseWriter, r *http.Request) bool {
	if s.authenticated(r) {
		return true
	}
	writeJSON(w, http.StatusUnauthorized, map[string]any{"error": "unauthorized"})
	return false
}

// isAdmin accepts the local credential written to $AGENTTY_HOME for CLI commands.
func (s *Server) isAdmin(r *http.Request) bool {
	if s.config.AdminToken == "" {
		return false
	}
	header := r.Header.Get("Authorization")
	if !strings.HasPrefix(header, "Bearer ") {
		return false
	}
	token := strings.TrimSpace(strings.TrimPrefix(header, "Bearer "))
	return subtle.ConstantTimeCompare([]byte(token), []byte(s.config.AdminToken)) == 1
}

func (s *Server) requireAdminOrDevice(w http.ResponseWriter, r *http.Request) bool {
	if s.authenticated(r) || s.isAdmin(r) {
		return true
	}
	writeJSON(w, http.StatusUnauthorized, map[string]any{"error": "unauthorized"})
	return false
}

func (s *Server) cookieSecure(r *http.Request) bool {
	return s.config.CookieSecure || r.TLS != nil
}

// baseURL is the address a QR should point at. PublicURL wins when the daemon
// sits behind a tunnel or reverse proxy.
func (s *Server) baseURL(r *http.Request) string {
	if s.config.PublicURL != "" {
		return strings.TrimRight(s.config.PublicURL, "/")
	}
	scheme := "http"
	if r.TLS != nil || s.config.CookieSecure {
		scheme = "https"
	}
	return scheme + "://" + r.Host
}

func clientIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

func writeJSON(w http.ResponseWriter, status int, payload any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(payload)
}
