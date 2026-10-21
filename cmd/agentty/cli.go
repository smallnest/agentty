package main

import (
	"bytes"
	"crypto/rand"
	"crypto/tls"
	"encoding/base64"
	"encoding/json"
	"flag"
	"fmt"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/smallnest/agentty/internal/sessionhost"
)

type runtimeInfo struct {
	Listen string `json:"listen"`
	Scheme string `json:"scheme"`
	PID    int    `json:"pid"`
}

type adminConn struct {
	base   string
	token  string
	client *http.Client
}

func main() {
	if len(os.Args) < 2 {
		runDaemon()
		return
	}
	switch os.Args[1] {
	case "pair":
		if err := runPair(agenttyHome()); err != nil {
			fmt.Fprintf(os.Stderr, "agentty pair: %v\n", err)
			os.Exit(1)
		}
	case "devices":
		if err := runDevices(agenttyHome(), os.Args[2:]); err != nil {
			fmt.Fprintf(os.Stderr, "agentty devices: %v\n", err)
			os.Exit(1)
		}
	case "sessions":
		if err := runSessions(agenttyHome(), os.Args[2:]); err != nil {
			fmt.Fprintf(os.Stderr, "agentty sessions: %v\n", err)
			os.Exit(1)
		}
	case "host":
		if err := runHost(os.Args[2:]); err != nil {
			fmt.Fprintf(os.Stderr, "agentty host: %v\n", err)
			os.Exit(1)
		}
	case "version", "-v", "--version":
		printVersion()
	case "help", "-h", "--help":
		usage()
	default:
		fmt.Fprintf(os.Stderr, "unknown command %q\n\n", os.Args[1])
		usage()
		os.Exit(2)
	}
}

func usage() {
	fmt.Print(`agentty — run coding agents in PTYs and serve them in a browser

Usage:
  agentty                        run the daemon (configure with AGENTTY_* env)
  agentty pair                   mint a one-time pairing code from the running daemon
  agentty devices                list enrolled devices
  agentty devices revoke <id>    revoke a device immediately
  agentty sessions               list sessions
  agentty sessions rename <id> <name>
  agentty sessions kill <id>
  agentty version                print version information
  agentty help                   show this help

Both commands use AGENTTY_HOME (default ~/.agentty) to find the daemon.
`)
}

func agenttyHome() string {
	if value := os.Getenv("AGENTTY_HOME"); value != "" {
		return value
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ".agentty"
	}
	return filepath.Join(home, ".agentty")
}

// loadOrCreateAdminToken persists the credential local CLI commands present to
// the daemon. It lives beside the pairing token with mode 0600.
func loadOrCreateAdminToken(home string) (string, error) {
	path := filepath.Join(home, "local-credential")
	if data, err := os.ReadFile(path); err == nil {
		if token := strings.TrimSpace(string(data)); token != "" {
			return token, nil
		}
	}

	var raw [32]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "", err
	}
	token := base64.RawURLEncoding.EncodeToString(raw[:])
	if err := os.MkdirAll(home, 0o700); err != nil {
		return "", err
	}
	if err := os.WriteFile(path, []byte(token+"\n"), 0o600); err != nil {
		return "", err
	}
	return token, nil
}

func writeRuntime(home, listen string, secure bool) {
	scheme := "http"
	if secure {
		scheme = "https"
	}
	data, err := json.MarshalIndent(runtimeInfo{
		Listen: listen,
		Scheme: scheme,
		PID:    os.Getpid(),
	}, "", "  ")
	if err != nil {
		return
	}
	if err := os.MkdirAll(home, 0o700); err != nil {
		return
	}
	path := filepath.Join(home, "runtime.json")
	temp := path + ".tmp"
	if err := os.WriteFile(temp, data, 0o600); err != nil {
		return
	}
	_ = os.Rename(temp, path)
}

func readRuntime(home string) (runtimeInfo, error) {
	data, err := os.ReadFile(filepath.Join(home, "runtime.json"))
	if err != nil {
		return runtimeInfo{}, err
	}
	var info runtimeInfo
	if err := json.Unmarshal(data, &info); err != nil {
		return runtimeInfo{}, err
	}
	if info.Listen == "" {
		return runtimeInfo{}, fmt.Errorf("runtime.json has no listen address")
	}
	return info, nil
}

func connectAdmin(home string) (*adminConn, error) {
	info, err := readRuntime(home)
	if err != nil {
		return nil, fmt.Errorf("agentty does not look like it is running (start it first): %w", err)
	}
	credential, err := os.ReadFile(filepath.Join(home, "local-credential"))
	if err != nil {
		return nil, fmt.Errorf("read local credential: %w", err)
	}

	host, port, err := net.SplitHostPort(info.Listen)
	if err != nil {
		return nil, fmt.Errorf("runtime listen address: %w", err)
	}
	if host == "" || host == "0.0.0.0" || host == "::" {
		host = "127.0.0.1"
	}

	scheme := info.Scheme
	if scheme == "" {
		scheme = "http"
	}
	client := &http.Client{Timeout: 10 * time.Second}
	if scheme == "https" {
		// The CLI only talks to the local daemon, whose certificate is usually
		// self-signed on loopback.
		client.Transport = &http.Transport{
			TLSClientConfig: &tls.Config{InsecureSkipVerify: true},
		}
	}

	return &adminConn{
		base:   fmt.Sprintf("%s://%s", scheme, net.JoinHostPort(host, port)),
		token:  strings.TrimSpace(string(credential)),
		client: client,
	}, nil
}

func runPair(home string) error {
	admin, err := connectAdmin(home)
	if err != nil {
		return err
	}

	request, err := http.NewRequest(http.MethodPost, admin.base+"/api/pairing/code", nil)
	if err != nil {
		return err
	}
	request.Header.Set("Authorization", "Bearer "+admin.token)

	response, err := admin.client.Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return fmt.Errorf("daemon returned %s", response.Status)
	}

	var pairing struct {
		Code      string    `json:"code"`
		URL       string    `json:"url"`
		QR        string    `json:"qr"`
		ExpiresAt time.Time `json:"expiresAt"`
	}
	if err := json.NewDecoder(response.Body).Decode(&pairing); err != nil {
		return err
	}

	fmt.Printf("pairing code: %s\n", pairing.Code)
	fmt.Printf("valid until:  %s\n", pairing.ExpiresAt.Format(time.RFC3339))
	fmt.Printf("url:          %s\n", pairing.URL)
	fmt.Println(pairing.QR)
	return nil
}

func runDevices(home string, args []string) error {
	admin, err := connectAdmin(home)
	if err != nil {
		return err
	}
	if len(args) >= 1 && args[0] == "revoke" {
		if len(args) < 2 || strings.TrimSpace(args[1]) == "" {
			return fmt.Errorf("usage: agentty devices revoke <id>")
		}
		return revokeDevice(admin, args[1])
	}
	return listDevices(admin)
}

func listDevices(admin *adminConn) error {
	request, err := http.NewRequest(http.MethodGet, admin.base+"/api/devices", nil)
	if err != nil {
		return err
	}
	request.Header.Set("Authorization", "Bearer "+admin.token)

	response, err := admin.client.Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return fmt.Errorf("daemon returned %s", response.Status)
	}

	var devices []struct {
		ID        string    `json:"id"`
		Name      string    `json:"name"`
		LastSeen  time.Time `json:"lastSeen"`
		ExpiresAt time.Time `json:"expiresAt"`
	}
	if err := json.NewDecoder(response.Body).Decode(&devices); err != nil {
		return err
	}
	if len(devices) == 0 {
		fmt.Println("no devices enrolled")
		return nil
	}
	for _, device := range devices {
		fmt.Printf("%s  %-20s  last seen %s\n", device.ID, device.Name, device.LastSeen.Format(time.RFC3339))
	}
	return nil
}

func revokeDevice(admin *adminConn, id string) error {
	body, err := json.Marshal(map[string]string{"id": id})
	if err != nil {
		return err
	}
	request, err := http.NewRequest(http.MethodPost, admin.base+"/api/devices/revoke", bytes.NewReader(body))
	if err != nil {
		return err
	}
	request.Header.Set("Authorization", "Bearer "+admin.token)
	request.Header.Set("Content-Type", "application/json")

	response, err := admin.client.Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return fmt.Errorf("daemon returned %s", response.Status)
	}
	fmt.Printf("revoked %s\n", id)
	return nil
}

func runSessions(home string, args []string) error {
	admin, err := connectAdmin(home)
	if err != nil {
		return err
	}
	if len(args) >= 1 && args[0] == "rename" {
		if len(args) < 3 {
			return fmt.Errorf("usage: agentty sessions rename <id> <name>")
		}
		return renameSession(admin, args[1], strings.Join(args[2:], " "))
	}
	if len(args) >= 1 && args[0] == "kill" {
		if len(args) < 2 {
			return fmt.Errorf("usage: agentty sessions kill <id>")
		}
		return killSession(admin, args[1])
	}
	return listSessions(admin)
}

func listSessions(admin *adminConn) error {
	request, err := http.NewRequest(http.MethodGet, admin.base+"/api/sessions", nil)
	if err != nil {
		return err
	}
	request.Header.Set("Authorization", "Bearer "+admin.token)

	response, err := admin.client.Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return fmt.Errorf("daemon returned %s", response.Status)
	}

	var sessions []struct {
		ID      string `json:"id"`
		Name    string `json:"name"`
		Agent   string `json:"agent"`
		Exited  bool   `json:"exited"`
		Clients int    `json:"clients"`
	}
	if err := json.NewDecoder(response.Body).Decode(&sessions); err != nil {
		return err
	}
	if len(sessions) == 0 {
		fmt.Println("no sessions")
		return nil
	}
	for _, session := range sessions {
		state := "running"
		if session.Exited {
			state = "exited"
		}
		fmt.Printf("%s  %-24s  %-12s  %s  clients=%d\n",
			session.ID, session.Name, session.Agent, state, session.Clients)
	}
	return nil
}

func renameSession(admin *adminConn, id, name string) error {
	body, err := json.Marshal(map[string]string{"id": id, "name": name})
	if err != nil {
		return err
	}
	request, err := http.NewRequest(http.MethodPost, admin.base+"/api/sessions/rename", bytes.NewReader(body))
	if err != nil {
		return err
	}
	request.Header.Set("Authorization", "Bearer "+admin.token)
	request.Header.Set("Content-Type", "application/json")

	response, err := admin.client.Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return fmt.Errorf("daemon returned %s", response.Status)
	}
	fmt.Printf("renamed %s to %q\n", id, name)
	return nil
}

func killSession(admin *adminConn, id string) error {
	body, err := json.Marshal(map[string]string{"id": id})
	if err != nil {
		return err
	}
	request, err := http.NewRequest(http.MethodPost, admin.base+"/api/sessions/close", bytes.NewReader(body))
	if err != nil {
		return err
	}
	request.Header.Set("Authorization", "Bearer "+admin.token)
	request.Header.Set("Content-Type", "application/json")

	response, err := admin.client.Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return fmt.Errorf("daemon returned %s", response.Status)
	}
	fmt.Printf("closed %s\n", id)
	return nil
}

// runHost is the detached per-session PTY host. It is spawned by the daemon.
func runHost(args []string) error {
	flags := flag.NewFlagSet("host", flag.ContinueOnError)
	id := flags.String("id", "", "")
	socket := flags.String("socket", "", "")
	meta := flags.String("meta", "", "")
	cwd := flags.String("cwd", "", "")
	replay := flags.Int("replay", 256*1024, "")
	cols := flags.Int("cols", 80, "")
	rows := flags.Int("rows", 24, "")
	if err := flags.Parse(args); err != nil {
		return err
	}
	command := flags.Args()
	if len(command) == 0 {
		return fmt.Errorf("missing command")
	}
	return sessionhost.Serve(sessionhost.ServerConfig{
		ID:          *id,
		Socket:      *socket,
		MetaPath:    *meta,
		Command:     command[0],
		Args:        command[1:],
		Cwd:         *cwd,
		Cols:        uint16(*cols),
		Rows:        uint16(*rows),
		ReplayBytes: *replay,
	})
}
