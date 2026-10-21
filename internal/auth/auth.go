// Package auth implements bootstrap credentials, one-time pairing codes, and
// per-device credentials.
//
// The bootstrap secret (a 256-bit random token, or an optional Argon2id-hashed
// password) is only used to enrol a device. Devices hold their own 256-bit
// secret and can be revoked individually, which is a better model than one
// long-lived shared secret.
package auth

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"golang.org/x/crypto/argon2"
)

const (
	// FailureLimit is the number of failed logins allowed per IP per window.
	FailureLimit = 10

	failureWindow = time.Minute

	// FailureDelay slows brute-force attempts. It is applied by the HTTP layer.
	FailureDelay = 200 * time.Millisecond

	// MinPasswordLength is enforced for AGENTTY_PASSWORD. Length matters more
	// than character-class rules; prefer a passphrase.
	MinPasswordLength = 12

	// PairingCodeTTL is how long a one-time pairing code stays valid.
	PairingCodeTTL = 10 * time.Minute

	// Argon2id parameters follow OWASP guidance for a local, low-volume login
	// path: 64 MiB, 3 passes, one lane.
	argonTime    = 3
	argonMemory  = 64 * 1024 // KiB
	argonThreads = 1
	argonKeyLen  = 32
)

// commonPasswords rejects the most obvious choices even when they are long
// enough. A passphrase or the random token mode is still better.
var commonPasswords = map[string]struct{}{
	"password":       {},
	"password1":      {},
	"password123":    {},
	"password1234":   {},
	"123456789012":   {},
	"qwertyuiop12":   {},
	"qwertyuiopasdf": {},
	"administrator":  {},
	"letmein12345":   {},
	"iloveyou1234":   {},
	"welcome12345":   {},
	"changeme1234":   {},
	"trustno12345":   {},
}

// Store holds the bootstrap credential, pairing codes, and enrolled devices.
type Store struct {
	home      string
	mode      string // "token" or "password"
	token     string // bootstrap token, for the recovery notice
	digest    [32]byte
	salt      []byte
	hash      []byte
	deviceTTL time.Duration
	logger    *log.Logger

	mu       sync.Mutex
	codes    map[string]time.Time
	devices  map[string]*Device
	failures map[string]*failureState

	// verifySlots bounds concurrent Argon2 verification so a login flood cannot
	// exhaust memory.
	verifySlots chan struct{}
}

type failureState struct {
	count int
	reset time.Time
}

// NewStore builds the credential store and loads persisted devices. An empty
// password selects token mode and reads or creates home/pairing-token.
func NewStore(home, password string, deviceTTL time.Duration, logger *log.Logger) (*Store, error) {
	store := &Store{
		home:        home,
		deviceTTL:   deviceTTL,
		logger:      logger,
		codes:       map[string]time.Time{},
		devices:     map[string]*Device{},
		failures:    map[string]*failureState{},
		verifySlots: make(chan struct{}, 4),
	}
	if err := store.loadDevices(); err != nil {
		return nil, err
	}

	if strings.TrimSpace(password) != "" {
		if err := ValidatePassword(password); err != nil {
			return nil, err
		}
		salt := randomBytes(16)
		store.mode = "password"
		store.salt = salt
		store.hash = argon2.IDKey([]byte(password), salt, argonTime, argonMemory, argonThreads, argonKeyLen)
		return store, nil
	}

	token, err := loadOrCreateToken(home)
	if err != nil {
		return nil, err
	}
	store.mode = "token"
	store.token = token
	store.digest = sha256.Sum256([]byte(token))
	return store, nil
}

// NewTokenStore builds an in-memory token store. It is used for
// AGENTTY_PAIRING_TOKEN and tests.
func NewTokenStore(token string, deviceTTL time.Duration) *Store {
	return &Store{
		mode:        "token",
		token:       token,
		digest:      sha256.Sum256([]byte(token)),
		deviceTTL:   deviceTTL,
		codes:       map[string]time.Time{},
		devices:     map[string]*Device{},
		failures:    map[string]*failureState{},
		verifySlots: make(chan struct{}, 4),
	}
}

// Mode reports "token" or "password".
func (s *Store) Mode() string {
	if s == nil {
		return "disabled"
	}
	return s.mode
}

// Token returns the bootstrap token, or "" in password mode.
func (s *Store) Token() string {
	if s == nil {
		return ""
	}
	return s.token
}

// ValidatePassword enforces a minimum length and rejects obvious choices.
func ValidatePassword(password string) error {
	if utf8.RuneCountInString(password) < MinPasswordLength {
		return fmt.Errorf("password must be at least %d characters", MinPasswordLength)
	}
	if _, ok := commonPasswords[strings.ToLower(password)]; ok {
		return fmt.Errorf("password is too common; use a passphrase or the pairing token")
	}
	return nil
}

// VerifyBootstrap checks the bootstrap token or password in constant time.
func (s *Store) VerifyBootstrap(secret string) bool {
	if s == nil || secret == "" {
		return false
	}
	switch s.mode {
	case "token":
		sum := sha256.Sum256([]byte(secret))
		return subtle.ConstantTimeCompare(sum[:], s.digest[:]) == 1
	case "password":
		got := argon2.IDKey([]byte(secret), s.salt, argonTime, argonMemory, argonThreads, argonKeyLen)
		return subtle.ConstantTimeCompare(got, s.hash) == 1
	default:
		return false
	}
}

// Exchange turns a bootstrap secret or a one-time pairing code into a new
// device. It returns the device id and secret to store in the client cookie.
func (s *Store) Exchange(secret, name string) (string, string, bool) {
	if s == nil || secret == "" {
		return "", "", false
	}
	if name = strings.TrimSpace(name); name == "" {
		name = "browser"
	}

	authorized := s.VerifyBootstrap(secret) || s.ConsumePairingCode(secret)
	if !authorized {
		return "", "", false
	}

	id, deviceSecret, err := s.CreateDevice(name)
	if err != nil {
		if s.logger != nil {
			s.logger.Printf("auth: create device: %v", err)
		}
		return "", "", false
	}
	return id, deviceSecret, true
}

// NewPairingCode mints a single-use code with a short TTL.
func (s *Store) NewPairingCode() (string, time.Time) {
	code := base64.RawURLEncoding.EncodeToString(randomBytes(16))
	expiry := time.Now().Add(PairingCodeTTL)

	s.mu.Lock()
	defer s.mu.Unlock()
	now := time.Now()
	for existing, expires := range s.codes {
		if now.After(expires) {
			delete(s.codes, existing)
		}
	}
	s.codes[code] = expiry
	return code, expiry
}

// ConsumePairingCode validates and atomically burns a pairing code.
func (s *Store) ConsumePairingCode(code string) bool {
	if s == nil || code == "" {
		return false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	expiry, ok := s.codes[code]
	if !ok {
		return false
	}
	delete(s.codes, code)
	return time.Now().Before(expiry)
}

// TryVerifySlot reserves one of the bounded verification slots.
func (s *Store) TryVerifySlot() bool {
	if s == nil {
		return true
	}
	select {
	case s.verifySlots <- struct{}{}:
		return true
	default:
		return false
	}
}

// ReleaseVerifySlot returns a slot reserved by TryVerifySlot.
func (s *Store) ReleaseVerifySlot() {
	if s == nil {
		return
	}
	<-s.verifySlots
}

// Allow reports whether ip may attempt a login right now.
func (s *Store) Allow(ip string) bool {
	if s == nil {
		return true
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	state := s.failures[ip]
	if state == nil {
		return true
	}
	if time.Now().After(state.reset) {
		delete(s.failures, ip)
		return true
	}
	return state.count < FailureLimit
}

// RecordFailure counts a failed login for ip.
func (s *Store) RecordFailure(ip string) {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	now := time.Now()
	state := s.failures[ip]
	if state == nil || now.After(state.reset) {
		s.failures[ip] = &failureState{count: 1, reset: now.Add(failureWindow)}
		return
	}
	state.count++
}

func loadOrCreateToken(home string) (string, error) {
	path := filepath.Join(home, "pairing-token")
	if data, err := os.ReadFile(path); err == nil {
		if token := strings.TrimSpace(string(data)); token != "" {
			return token, nil
		}
	}

	token := randomToken()
	if err := os.MkdirAll(home, 0o700); err != nil {
		return "", err
	}
	if err := os.WriteFile(path, []byte(token+"\n"), 0o600); err != nil {
		return "", err
	}
	return token, nil
}

func randomToken() string {
	return base64.RawURLEncoding.EncodeToString(randomBytes(32))
}

func randomBytes(n int) []byte {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	return b
}
