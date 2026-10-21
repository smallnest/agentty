// Command agentty runs coding-agent CLIs in PTYs and serves them to a browser
// over a multiplexed WebSocket, locally or through a Paseo-compatible relay.
package main

import (
	"context"
	"crypto/tls"
	"fmt"
	"log"
	"net"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/mdp/qrterminal/v3"

	"github.com/smallnest/agentty/internal/auth"
	"github.com/smallnest/agentty/internal/config"
	"github.com/smallnest/agentty/internal/netinfo"
	"github.com/smallnest/agentty/internal/relayclient"
	"github.com/smallnest/agentty/internal/server"
	"github.com/smallnest/agentty/internal/serverid"
	"github.com/smallnest/agentty/internal/terminal"
)

func runDaemon() {
	cfg := config.FromEnv()
	cfg.ServerID = serverid.LoadOrCreate(cfg.AgenttyHome, cfg.ServerID)

	log.Printf("agentty %s (commit %s, built %s)", version, commit, date)

	adminToken, err := loadOrCreateAdminToken(cfg.AgenttyHome)
	if err != nil {
		log.Fatalf("admin credential: %v", err)
	}
	cfg.AdminToken = adminToken
	writeRuntime(cfg.AgenttyHome, cfg.Listen, tlsEnabled(cfg))

	manager := terminal.NewManager(cfg.IdleTimeout)
	manager.SetHome(cfg.AgenttyHome)
	manager.SetBackend(cfg.Backend)
	go manager.Janitor(time.Minute)

	tmux := terminal.NewTmuxBackend(cfg.AgenttyHome, "agentty")
	if !tmux.Available() {
		tmux = nil
	}
	switch {
	case !cfg.Persist:
		log.Printf("agentty: persistence disabled; sessions end with the daemon")
	case tmux != nil:
		log.Printf("agentty: persistence via tmux (%s)", cfg.AgenttyHome)
	default:
		log.Printf("agentty: persistence via built-in host (%s)", cfg.AgenttyHome)
	}
	if err := manager.Restore(tmux, cfg.ReplayBytes); err != nil {
		log.Printf("agentty: restore: %v", err)
	}

	var store *auth.Store
	if cfg.AuthEnabled {
		var err error
		if cfg.PairingToken != "" {
			store = auth.NewTokenStore(cfg.PairingToken, cfg.SessionTTL)
		} else {
			store, err = auth.NewStore(cfg.AgenttyHome, cfg.Password, cfg.SessionTTL, log.Default())
			if err != nil {
				log.Fatalf("auth: %v", err)
			}
		}
		printAuth(cfg, store)
		if !isLoopback(cfg.Listen) && !tlsEnabled(cfg) {
			log.Printf("agentty: WARNING listening on %s without TLS. Devices on your network can see pairing material and cookies in plaintext. Set AGENTTY_TLS_CERT_FILE/KEY or a TLS tunnel; bind AGENTTY_LISTEN=127.0.0.1:7070 for local-only access.", cfg.Listen)
		}
	} else {
		log.Printf("agentty: WARNING authentication disabled (AGENTTY_AUTH_ENABLED=false)")
	}

	if cfg.RelayEnabled {
		if cfg.RelayEndpoint == "" {
			log.Printf("agentty: relay enabled but AGENTTY_RELAY_ENDPOINT is empty; skipping")
		} else {
			connector := relayclient.New(cfg, manager, log.Default())
			go connector.Run(context.Background())
			log.Printf("agentty relay: %s (serverId=%s, tls=%v)", cfg.RelayEndpoint, cfg.ServerID, cfg.RelayUseTLS)
			log.Printf("agentty relay client URL: %s", relayClientURL(cfg))
			log.Printf("agentty relay UI URL: %s", relayUIURL(cfg))
		}
	}

	httpServer := &http.Server{
		Addr:    cfg.Listen,
		Handler: server.New(cfg, manager, store).Handler(),
	}

	scheme := "http"
	if tlsEnabled(cfg) {
		scheme = "https"
	}
	log.Printf("agentty listening (default agent=%s):", cfg.DefaultAgent)
	for _, address := range listenURLs(cfg) {
		log.Printf("  %s", address)
	}

	if scheme == "https" {
		cert, loadErr := tls.LoadX509KeyPair(cfg.TLSCertFile, cfg.TLSKeyFile)
		if loadErr != nil {
			log.Fatalf("loading TLS key pair: %v", loadErr)
		}
		httpServer.TLSConfig = &tls.Config{Certificates: []tls.Certificate{cert}}
		err = httpServer.ListenAndServeTLS("", "")
	} else {
		err = httpServer.ListenAndServe()
	}
	if err != nil && err != http.ErrServerClosed {
		log.Fatalf("server: %v", err)
	}
}

func printAuth(cfg config.Config, store *auth.Store) {
	if store.Mode() == "password" {
		log.Printf("agentty auth: password mode (AGENTTY_PASSWORD)")
	} else {
		log.Printf("agentty auth: recovery token (treat it like a password): %s", store.Token())
	}

	code, expiry := store.NewPairingCode()
	link := publicBaseURL(cfg) + "/#pair=" + code
	log.Printf("agentty auth: one-time pairing code (valid until %s)", expiry.Format(time.RFC3339))
	log.Printf("  code: %s", code)
	log.Printf("  link: %s", link)
	log.Printf("agentty auth: scan the QR from a device on the same network")
	qrterminal.GenerateHalfBlock(link, qrterminal.L, os.Stdout)
}

func publicBaseURL(cfg config.Config) string {
	if cfg.PublicURL != "" {
		return strings.TrimRight(cfg.PublicURL, "/")
	}
	scheme := "http"
	if tlsEnabled(cfg) {
		scheme = "https"
	}
	host, port, err := net.SplitHostPort(cfg.Listen)
	if err != nil {
		return scheme + "://" + cfg.Listen
	}
	switch host {
	case "", "0.0.0.0", "::":
		host = advertisedIP(cfg)
	case "127.0.0.1", "::1", "localhost":
		host = "localhost"
	}
	return fmt.Sprintf("%s://%s", scheme, net.JoinHostPort(host, port))
}

func advertisedIP(cfg config.Config) string {
	if cfg.Interface != "" {
		if ip := netinfo.IPv4Of(cfg.Interface); ip != "" && !net.ParseIP(ip).IsLoopback() {
			return ip
		}
		log.Printf("agentty: interface %q has no usable IPv4 address; auto-selecting", cfg.Interface)
	}
	if ip := netinfo.Best(); ip != "" {
		return ip
	}
	return "127.0.0.1"
}

// listenURLs prints every address a client might use. A wildcard bind gets a
// localhost URL plus each LAN IPv4, so the operator can pick the right one.
func listenURLs(cfg config.Config) []string {
	scheme := "http"
	if tlsEnabled(cfg) {
		scheme = "https"
	}
	host, port, err := net.SplitHostPort(cfg.Listen)
	if err != nil {
		return []string{scheme + "://" + cfg.Listen}
	}
	switch host {
	case "", "0.0.0.0", "::":
		format := func(ip string) string {
			return fmt.Sprintf("%s://%s", scheme, net.JoinHostPort(ip, port))
		}
		urls := []string{format("localhost")}
		preferred := advertisedIP(cfg)
		if preferred != "" && preferred != "127.0.0.1" {
			urls = append(urls, format(preferred))
		}
		for _, ip := range netinfo.All() {
			if ip == preferred {
				continue
			}
			urls = append(urls, format(ip))
		}
		return urls
	default:
		return []string{fmt.Sprintf("%s://%s", scheme, net.JoinHostPort(host, port))}
	}
}

func isLoopback(listen string) bool {
	host, _, err := net.SplitHostPort(listen)
	if err != nil {
		return false
	}
	switch host {
	case "127.0.0.1", "::1", "localhost":
		return true
	}
	return false
}

func tlsEnabled(cfg config.Config) bool {
	return cfg.TLSCertFile != "" && cfg.TLSKeyFile != ""
}

func relayClientURL(cfg config.Config) string {
	scheme := "ws"
	if cfg.RelayUseTLS {
		scheme = "wss"
	}
	query := url.Values{}
	query.Set("serverId", cfg.ServerID)
	query.Set("role", "client")
	query.Set("v", "2")
	return fmt.Sprintf("%s://%s/ws?%s", scheme, cfg.RelayEndpoint, query.Encode())
}

func relayUIURL(cfg config.Config) string {
	query := url.Values{}
	query.Set("relay", cfg.RelayEndpoint)
	query.Set("server", cfg.ServerID)
	if cfg.RelayUseTLS {
		query.Set("tls", "1")
	} else {
		query.Set("tls", "0")
	}
	return fmt.Sprintf("http://%s/?%s", cfg.Listen, query.Encode())
}
