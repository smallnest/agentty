package server

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"

	"github.com/smallnest/agentty/internal/auth"
	"github.com/smallnest/agentty/internal/config"
	"github.com/smallnest/agentty/internal/terminal"
)

func newAuthServer(t *testing.T, token string) *httptest.Server {
	t.Helper()
	cfg := config.Config{
		DefaultAgent: "shell",
		AllowCmd:     true,
		WorkingDir:   t.TempDir(),
		ReplayBytes:  64 * 1024,
		SendQueue:    64,
		AuthEnabled:  true,
		SessionTTL:   time.Hour,
		AdminToken:   "admin-token",
	}
	manager := terminal.NewManager(0)
	store := auth.NewTokenStore(token, time.Hour)
	ts := httptest.NewServer(New(cfg, manager, store).Handler())
	t.Cleanup(func() {
		ts.Close()
		manager.CloseAll()
	})
	return ts
}

func postLogin(t *testing.T, client *http.Client, endpoint, secret string) *http.Response {
	t.Helper()
	body, err := json.Marshal(map[string]string{"secret": secret, "name": "test"})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	response, err := client.Post(endpoint, "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatalf("post: %v", err)
	}
	return response
}

func newJarClient(t *testing.T) *http.Client {
	t.Helper()
	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatalf("cookiejar: %v", err)
	}
	return &http.Client{Jar: jar}
}

func adminRequest(t *testing.T, method, endpoint string, payload any) *http.Response {
	t.Helper()
	var reader io.Reader
	if payload != nil {
		data, err := json.Marshal(payload)
		if err != nil {
			t.Fatalf("marshal: %v", err)
		}
		reader = bytes.NewReader(data)
	}
	request, err := http.NewRequest(method, endpoint, reader)
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	request.Header.Set("Authorization", "Bearer admin-token")
	request.Header.Set("Content-Type", "application/json")
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatalf("admin request: %v", err)
	}
	return response
}

func TestAuthStatusUnauthenticated(t *testing.T) {
	ts := newAuthServer(t, "tok")
	response, err := http.Get(ts.URL + "/api/auth/status")
	if err != nil {
		t.Fatalf("status: %v", err)
	}
	defer response.Body.Close()
	var body map[string]any
	if err := json.NewDecoder(response.Body).Decode(&body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if body["authenticated"] == true {
		t.Fatal("expected unauthenticated")
	}
	if body["mode"] != "token" {
		t.Fatalf("mode = %v", body["mode"])
	}
}

func TestLoginFlow(t *testing.T) {
	ts := newAuthServer(t, "tok")
	client := newJarClient(t)

	wrong := postLogin(t, client, ts.URL+"/api/auth/login", "nope")
	wrong.Body.Close()
	if wrong.StatusCode != http.StatusUnauthorized {
		t.Fatalf("wrong secret status = %d, want 401", wrong.StatusCode)
	}

	right := postLogin(t, client, ts.URL+"/api/auth/login", "tok")
	right.Body.Close()
	if right.StatusCode != http.StatusOK {
		t.Fatalf("right secret status = %d, want 200", right.StatusCode)
	}

	status, err := client.Get(ts.URL + "/api/auth/status")
	if err != nil {
		t.Fatalf("status: %v", err)
	}
	defer status.Body.Close()
	var body map[string]any
	_ = json.NewDecoder(status.Body).Decode(&body)
	if body["authenticated"] != true {
		t.Fatal("expected authenticated after login")
	}

	agents, err := client.Get(ts.URL + "/api/agents")
	if err != nil {
		t.Fatalf("agents: %v", err)
	}
	defer agents.Body.Close()
	if agents.StatusCode != http.StatusOK {
		t.Fatalf("agents status = %d, want 200", agents.StatusCode)
	}
}

func TestAgentsRequireAuth(t *testing.T) {
	ts := newAuthServer(t, "tok")
	response, err := http.Get(ts.URL + "/api/agents")
	if err != nil {
		t.Fatalf("agents: %v", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", response.StatusCode)
	}
}

func TestLogoutRevokesDevice(t *testing.T) {
	ts := newAuthServer(t, "tok")
	client := newJarClient(t)

	login := postLogin(t, client, ts.URL+"/api/auth/login", "tok")
	cookies := login.Cookies()
	login.Body.Close()
	if len(cookies) == 0 {
		t.Fatal("login did not set a device cookie")
	}
	deviceCookieValue := cookies[0].Value

	logout, err := client.Post(ts.URL+"/api/auth/logout", "application/json", nil)
	if err != nil {
		t.Fatalf("logout: %v", err)
	}
	logout.Body.Close()

	// The old cookie must stop working even if it is replayed.
	request, _ := http.NewRequest(http.MethodGet, ts.URL+"/api/agents", nil)
	request.Header.Set("Cookie", deviceCookie+"="+deviceCookieValue)
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatalf("agents: %v", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusUnauthorized {
		t.Fatalf("replayed cookie status = %d, want 401", response.StatusCode)
	}
}

func TestWSRequiresAuth(t *testing.T) {
	ts := newAuthServer(t, "tok")
	wsURL := "ws" + strings.TrimPrefix(ts.URL, "http") + "/ws"

	conn, response, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err == nil {
		_ = conn.Close()
		t.Fatal("expected the unauthenticated upgrade to fail")
	}
	if response == nil || response.StatusCode != http.StatusUnauthorized {
		t.Fatalf("status = %v, err = %v, want 401", response, err)
	}
}

func TestWSAcceptsDeviceCookie(t *testing.T) {
	ts := newAuthServer(t, "tok")
	login := postLogin(t, &http.Client{}, ts.URL+"/api/auth/login", "tok")
	cookies := login.Cookies()
	login.Body.Close()
	if len(cookies) == 0 {
		t.Fatal("login did not set a device cookie")
	}

	header := http.Header{}
	header.Set("Cookie", cookies[0].Name+"="+cookies[0].Value)
	wsURL := "ws" + strings.TrimPrefix(ts.URL, "http") + "/ws"
	conn, _, err := websocket.DefaultDialer.Dial(wsURL, header)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	conn.Close()
}

func TestPairingCodeFlow(t *testing.T) {
	ts := newAuthServer(t, "tok")

	response := adminRequest(t, http.MethodPost, ts.URL+"/api/pairing/code", nil)
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("pairing code status = %d", response.StatusCode)
	}
	var pairing struct {
		Code string `json:"code"`
		URL  string `json:"url"`
		QR   string `json:"qr"`
	}
	if err := json.NewDecoder(response.Body).Decode(&pairing); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if pairing.Code == "" || pairing.URL == "" || pairing.QR == "" {
		t.Fatalf("incomplete pairing response: %+v", pairing)
	}

	phone := newJarClient(t)
	pair := postLogin(t, phone, ts.URL+"/api/auth/login", pairing.Code)
	pair.Body.Close()
	if pair.StatusCode != http.StatusOK {
		t.Fatalf("pair status = %d, want 200", pair.StatusCode)
	}

	agents, err := phone.Get(ts.URL + "/api/agents")
	if err != nil {
		t.Fatalf("agents: %v", err)
	}
	defer agents.Body.Close()
	if agents.StatusCode != http.StatusOK {
		t.Fatalf("paired device status = %d, want 200", agents.StatusCode)
	}
}

func TestAdminDevicesListAndRevoke(t *testing.T) {
	ts := newAuthServer(t, "tok")
	deviceClient := newJarClient(t)

	login := postLogin(t, deviceClient, ts.URL+"/api/auth/login", "tok")
	login.Body.Close()

	response := adminRequest(t, http.MethodGet, ts.URL+"/api/devices", nil)
	var devices []auth.DeviceView
	if err := json.NewDecoder(response.Body).Decode(&devices); err != nil {
		t.Fatalf("decode: %v", err)
	}
	response.Body.Close()
	if len(devices) != 1 {
		t.Fatalf("devices = %d, want 1", len(devices))
	}

	revoke := adminRequest(t, http.MethodPost, ts.URL+"/api/devices/revoke", map[string]string{"id": devices[0].ID})
	revoke.Body.Close()
	if revoke.StatusCode != http.StatusOK {
		t.Fatalf("revoke status = %d", revoke.StatusCode)
	}

	status, err := deviceClient.Get(ts.URL + "/api/auth/status")
	if err != nil {
		t.Fatalf("status: %v", err)
	}
	defer status.Body.Close()
	var body map[string]any
	_ = json.NewDecoder(status.Body).Decode(&body)
	if body["authenticated"] == true {
		t.Fatal("expected the revoked device to be logged out")
	}
}

func TestDeviceCannotManageDevices(t *testing.T) {
	ts := newAuthServer(t, "tok")
	client := newJarClient(t)

	login := postLogin(t, client, ts.URL+"/api/auth/login", "tok")
	login.Body.Close()

	devices, err := client.Get(ts.URL + "/api/devices")
	if err != nil {
		t.Fatalf("devices: %v", err)
	}
	devices.Body.Close()
	if devices.StatusCode != http.StatusForbidden {
		t.Fatalf("devices status = %d, want 403", devices.StatusCode)
	}

	pairing, err := client.Post(ts.URL+"/api/pairing/code", "application/json", nil)
	if err != nil {
		t.Fatalf("pairing code: %v", err)
	}
	pairing.Body.Close()
	if pairing.StatusCode != http.StatusForbidden {
		t.Fatalf("pairing status = %d, want 403", pairing.StatusCode)
	}
}

func TestPairingCodeRequiresAuth(t *testing.T) {
	ts := newAuthServer(t, "tok")
	response, err := http.Post(ts.URL+"/api/pairing/code", "application/json", nil)
	if err != nil {
		t.Fatalf("post: %v", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", response.StatusCode)
	}
}

func TestPairingCodeAcceptsAdminToken(t *testing.T) {
	ts := newAuthServer(t, "tok")

	request, err := http.NewRequest(http.MethodPost, ts.URL+"/api/pairing/code", nil)
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	request.Header.Set("Authorization", "Bearer admin-token")
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatalf("post: %v", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", response.StatusCode)
	}

	var pairing struct {
		Code string `json:"code"`
		URL  string `json:"url"`
		QR   string `json:"qr"`
	}
	if err := json.NewDecoder(response.Body).Decode(&pairing); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if pairing.Code == "" || pairing.QR == "" {
		t.Fatalf("incomplete pairing response: %+v", pairing)
	}
}

func TestPairingCodeRejectsWrongAdminToken(t *testing.T) {
	ts := newAuthServer(t, "tok")

	request, err := http.NewRequest(http.MethodPost, ts.URL+"/api/pairing/code", nil)
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	request.Header.Set("Authorization", "Bearer wrong")
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatalf("post: %v", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", response.StatusCode)
	}
}

func TestAdminSessionRename(t *testing.T) {
	ts := newAuthServer(t, "tok")

	login := postLogin(t, &http.Client{}, ts.URL+"/api/auth/login", "tok")
	cookies := login.Cookies()
	login.Body.Close()
	if len(cookies) == 0 {
		t.Fatal("login did not set a device cookie")
	}
	header := http.Header{}
	header.Set("Cookie", cookies[0].Name+"="+cookies[0].Value)

	wsURL := "ws" + strings.TrimPrefix(ts.URL, "http") + "/ws"
	conn, _, err := websocket.DefaultDialer.Dial(wsURL, header)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.Close()

	request := terminal.CreateRequest{Slot: 0, Session: "rename-admin", Cmd: "cat"}
	payload, _ := json.Marshal(request)
	if err := conn.WriteMessage(websocket.BinaryMessage, terminal.Encode(terminal.Frame{
		Op:   terminal.OpCreate,
		Slot: 0,
		Data: payload,
	})); err != nil {
		t.Fatalf("create: %v", err)
	}

	var sessionID string
	deadline := time.Now().Add(3 * time.Second)
	for sessionID == "" && time.Now().Before(deadline) {
		_ = conn.SetReadDeadline(time.Now().Add(2 * time.Second))
		messageType, data, err := conn.ReadMessage()
		if err != nil {
			t.Fatalf("read: %v", err)
		}
		if messageType != websocket.BinaryMessage {
			continue
		}
		frame, err := terminal.Decode(data)
		if err != nil {
			continue
		}
		if frame.Op == terminal.OpAttached {
			sessionID = string(frame.Data)
		}
	}
	if sessionID == "" {
		t.Fatal("did not receive the attached frame")
	}

	listRequest, err := http.NewRequest(http.MethodGet, ts.URL+"/api/sessions", nil)
	if err != nil {
		t.Fatalf("list request: %v", err)
	}
	listRequest.Header.Set("Authorization", "Bearer admin-token")
	listResponse, err := http.DefaultClient.Do(listRequest)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	var sessions []terminal.SessionView
	if err := json.NewDecoder(listResponse.Body).Decode(&sessions); err != nil {
		t.Fatalf("decode: %v", err)
	}
	listResponse.Body.Close()
	found := false
	for _, session := range sessions {
		if session.ID == sessionID {
			found = true
		}
	}
	if !found {
		t.Fatalf("session %s not listed", sessionID)
	}

	body, _ := json.Marshal(map[string]string{"id": sessionID, "name": "renamed-via-admin"})
	renameRequest, err := http.NewRequest(http.MethodPost, ts.URL+"/api/sessions/rename", bytes.NewReader(body))
	if err != nil {
		t.Fatalf("rename request: %v", err)
	}
	renameRequest.Header.Set("Authorization", "Bearer admin-token")
	renameRequest.Header.Set("Content-Type", "application/json")
	renameResponse, err := http.DefaultClient.Do(renameRequest)
	if err != nil {
		t.Fatalf("rename: %v", err)
	}
	renameResponse.Body.Close()
	if renameResponse.StatusCode != http.StatusOK {
		t.Fatalf("rename status = %d", renameResponse.StatusCode)
	}

	deadline = time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		_ = conn.SetReadDeadline(time.Now().Add(2 * time.Second))
		messageType, data, err := conn.ReadMessage()
		if err != nil {
			t.Fatalf("read after rename: %v", err)
		}
		if messageType != websocket.BinaryMessage {
			continue
		}
		frame, err := terminal.Decode(data)
		if err != nil {
			continue
		}
		if frame.Op == terminal.OpName && string(frame.Data) == "renamed-via-admin" {
			return
		}
	}
	t.Fatal("did not observe the renamed frame")
}
