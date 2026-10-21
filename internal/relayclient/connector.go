// Package relayclient connects agentty to a Paseo-compatible relay.
//
// The relay protocol is v2: the daemon holds one control socket per serverId and
// opens one data socket per client connectionId. The relay forwards opaque
// bytes, so a relay data socket carries exactly the same terminal frames as a
// local WebSocket; only the transport differs.
package relayclient

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/url"
	"sync"
	"time"

	"github.com/gorilla/websocket"

	"github.com/smallnest/agentty/internal/config"
	"github.com/smallnest/agentty/internal/server"
	"github.com/smallnest/agentty/internal/terminal"
)

const (
	controlPingInterval = 10 * time.Second
	controlReadTimeout  = 60 * time.Second
	writeTimeout        = 10 * time.Second
)

type controlMessage struct {
	Type          string   `json:"type"`
	ConnectionIDs []string `json:"connectionIds"`
	ConnectionID  string   `json:"connectionId"`
	TS            int64    `json:"ts"`
}

// Connector owns the relay control socket and one data socket per client.
type Connector struct {
	cfg     config.Config
	manager *terminal.Manager
	logger  *log.Logger

	mu      sync.Mutex
	control *websocket.Conn
	data    map[string]*server.Client
}

// New builds a Connector.
func New(cfg config.Config, manager *terminal.Manager, logger *log.Logger) *Connector {
	return &Connector{
		cfg:     cfg,
		manager: manager,
		logger:  logger,
		data:    map[string]*server.Client{},
	}
}

// Run keeps the relay control socket connected until ctx is cancelled.
func (c *Connector) Run(ctx context.Context) {
	attempt := 0
	for {
		if ctx.Err() != nil {
			return
		}
		connected, err := c.connectControl(ctx)
		c.closeAllData()
		if ctx.Err() != nil {
			return
		}
		if err != nil {
			c.logger.Printf("relay: control: %v", err)
		}
		if connected {
			attempt = 0
		} else {
			attempt++
		}
		delay := backoff(attempt)
		c.logger.Printf("relay: reconnecting in %s", delay)
		select {
		case <-ctx.Done():
			return
		case <-time.After(delay):
		}
	}
}

// connectControl dials the control socket and reads until it closes. It reports
// whether the dial itself succeeded, so the caller can reset its backoff.
func (c *Connector) connectControl(ctx context.Context) (bool, error) {
	endpoint := c.endpoint("server", "")
	conn, _, err := websocket.DefaultDialer.DialContext(ctx, endpoint, nil)
	if err != nil {
		return false, err
	}

	c.mu.Lock()
	c.control = conn
	c.mu.Unlock()

	defer func() {
		c.mu.Lock()
		if c.control == conn {
			c.control = nil
		}
		c.mu.Unlock()
		_ = conn.Close()
	}()

	c.logger.Printf("relay: control connected (%s)", endpoint)

	done := make(chan struct{})
	defer close(done)
	go c.keepalive(conn, done)

	_ = conn.SetReadDeadline(time.Now().Add(controlReadTimeout))
	conn.SetPongHandler(func(string) error {
		return conn.SetReadDeadline(time.Now().Add(controlReadTimeout))
	})
	conn.SetPingHandler(func(appData string) error {
		_ = conn.SetReadDeadline(time.Now().Add(controlReadTimeout))
		return conn.WriteControl(websocket.PongMessage, []byte(appData), time.Now().Add(writeTimeout))
	})

	for {
		_, data, err := conn.ReadMessage()
		if err != nil {
			return true, err
		}
		_ = conn.SetReadDeadline(time.Now().Add(controlReadTimeout))
		c.handleControl(data)
	}
}

func (c *Connector) keepalive(conn *websocket.Conn, done <-chan struct{}) {
	ticker := time.NewTicker(controlPingInterval)
	defer ticker.Stop()
	for {
		select {
		case <-done:
			return
		case <-ticker.C:
			_ = conn.SetWriteDeadline(time.Now().Add(writeTimeout))
			if err := conn.WriteMessage(websocket.PingMessage, nil); err != nil {
				_ = conn.Close()
				return
			}
		}
	}
}

func (c *Connector) handleControl(data []byte) {
	var message controlMessage
	if err := json.Unmarshal(data, &message); err != nil {
		return
	}

	switch message.Type {
	case "ping":
		c.sendControl(map[string]any{"type": "pong", "ts": time.Now().UnixMilli()})
	case "sync":
		for _, connectionID := range message.ConnectionIDs {
			go c.ensureData(connectionID)
		}
	case "connected":
		if message.ConnectionID != "" {
			go c.ensureData(message.ConnectionID)
		}
	case "disconnected":
		if message.ConnectionID != "" {
			c.closeData(message.ConnectionID)
		}
	}
}

func (c *Connector) sendControl(payload any) {
	data, err := json.Marshal(payload)
	if err != nil {
		return
	}
	c.mu.Lock()
	conn := c.control
	c.mu.Unlock()
	if conn == nil {
		return
	}
	_ = conn.SetWriteDeadline(time.Now().Add(writeTimeout))
	_ = conn.WriteMessage(websocket.TextMessage, data)
}

// ensureData opens the daemon data socket for a client connectionId.
func (c *Connector) ensureData(connectionID string) {
	c.mu.Lock()
	if _, exists := c.data[connectionID]; exists {
		c.mu.Unlock()
		return
	}
	c.mu.Unlock()

	endpoint := c.endpoint("server", connectionID)
	conn, _, err := websocket.DefaultDialer.Dial(endpoint, nil)
	if err != nil {
		c.logger.Printf("relay: data %s: %v", connectionID, err)
		return
	}

	client := server.NewClient(c.manager, c.cfg, server.NewWSTransport(conn))

	c.mu.Lock()
	if existing := c.data[connectionID]; existing != nil {
		c.mu.Unlock()
		_ = conn.Close()
		return
	}
	c.data[connectionID] = client
	c.mu.Unlock()

	c.logger.Printf("relay: data attached %s", connectionID)
	go func() {
		client.Run()
		c.mu.Lock()
		delete(c.data, connectionID)
		c.mu.Unlock()
		c.logger.Printf("relay: data detached %s", connectionID)
	}()
}

func (c *Connector) closeData(connectionID string) {
	c.mu.Lock()
	client := c.data[connectionID]
	delete(c.data, connectionID)
	c.mu.Unlock()
	if client != nil {
		client.Close(1001, "client disconnected")
	}
}

func (c *Connector) closeAllData() {
	c.mu.Lock()
	clients := make([]*server.Client, 0, len(c.data))
	for _, client := range c.data {
		clients = append(clients, client)
	}
	c.data = map[string]*server.Client{}
	c.mu.Unlock()
	for _, client := range clients {
		client.Close(1011, "relay reconnect")
	}
}

// endpoint builds a relay WebSocket URL for the configured role.
func (c *Connector) endpoint(role, connectionID string) string {
	scheme := "ws"
	if c.cfg.RelayUseTLS {
		scheme = "wss"
	}
	query := url.Values{}
	query.Set("serverId", c.cfg.ServerID)
	query.Set("role", role)
	query.Set("v", "2")
	if connectionID != "" {
		query.Set("connectionId", connectionID)
	}
	return fmt.Sprintf("%s://%s/ws?%s", scheme, c.cfg.RelayEndpoint, query.Encode())
}

func backoff(attempt int) time.Duration {
	if attempt < 1 {
		attempt = 1
	}
	delay := time.Duration(attempt) * time.Second
	if delay > 30*time.Second {
		delay = 30 * time.Second
	}
	return delay
}
