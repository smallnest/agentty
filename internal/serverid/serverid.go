// Package serverid persists the stable daemon identity used on the relay.
package serverid

import (
	"crypto/rand"
	"encoding/base64"
	"os"
	"path/filepath"
	"strings"
)

// LoadOrCreate returns override when set, otherwise reads or creates a stable
// `srv_` identifier under home.
func LoadOrCreate(home, override string) string {
	if override = strings.TrimSpace(override); override != "" {
		return override
	}
	path := filepath.Join(home, "server-id")
	if data, err := os.ReadFile(path); err == nil {
		if id := strings.TrimSpace(string(data)); id != "" {
			return id
		}
	}

	id := "srv_" + base64.RawURLEncoding.EncodeToString(randomBytes(9))
	if err := os.MkdirAll(home, 0o700); err == nil {
		_ = os.WriteFile(path, []byte(id+"\n"), 0o600)
	}
	return id
}

func randomBytes(n int) []byte {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	return b
}
