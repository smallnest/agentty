package auth

import (
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"time"
)

// Device is an enrolled browser or CLI. Only the SHA-256 digest of its secret
// is stored; the secret itself lives in the client's HttpOnly cookie.
type Device struct {
	ID         string    `json:"id"`
	Name       string    `json:"name"`
	SecretHash string    `json:"secretHash"`
	CreatedAt  time.Time `json:"createdAt"`
	LastSeen   time.Time `json:"lastSeen"`
	ExpiresAt  time.Time `json:"expiresAt"`
}

// DeviceView is the redacted shape returned by the devices API.
type DeviceView struct {
	ID        string    `json:"id"`
	Name      string    `json:"name"`
	CreatedAt time.Time `json:"createdAt"`
	LastSeen  time.Time `json:"lastSeen"`
	ExpiresAt time.Time `json:"expiresAt"`
}

// CreateDevice enrols a new device and returns its id and secret.
func (s *Store) CreateDevice(name string) (string, string, error) {
	if s == nil {
		return "", "", os.ErrInvalid
	}
	id := "dev_" + hex.EncodeToString(randomBytes(8))
	secret := randomToken()
	now := time.Now()

	device := &Device{
		ID:         id,
		Name:       name,
		SecretHash: base64.RawURLEncoding.EncodeToString(sha256Bytes(secret)),
		CreatedAt:  now,
		LastSeen:   now,
		ExpiresAt:  now.Add(s.deviceTTL),
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	s.devices[id] = device
	return id, secret, s.saveDevicesLocked()
}

// AuthenticateDevice validates a device secret and slides its expiry.
func (s *Store) AuthenticateDevice(id, secret string) bool {
	if s == nil || id == "" || secret == "" {
		return false
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	device := s.devices[id]
	if device == nil {
		return false
	}
	now := time.Now()
	if now.After(device.ExpiresAt) {
		delete(s.devices, id)
		_ = s.saveDevicesLocked()
		return false
	}

	expected, err := base64.RawURLEncoding.DecodeString(device.SecretHash)
	if err != nil {
		return false
	}
	if subtle.ConstantTimeCompare(sha256Bytes(secret), expected) != 1 {
		return false
	}

	device.LastSeen = now
	device.ExpiresAt = now.Add(s.deviceTTL)
	return true
}

// RevokeDevice deletes a device immediately.
func (s *Store) RevokeDevice(id string) bool {
	if s == nil || id == "" {
		return false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.devices[id]; !ok {
		return false
	}
	delete(s.devices, id)
	_ = s.saveDevicesLocked()
	return true
}

// ListDevices returns the enrolled devices without their secrets.
func (s *Store) ListDevices() []DeviceView {
	if s == nil {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]DeviceView, 0, len(s.devices))
	for _, device := range s.devices {
		out = append(out, DeviceView{
			ID:        device.ID,
			Name:      device.Name,
			CreatedAt: device.CreatedAt,
			LastSeen:  device.LastSeen,
			ExpiresAt: device.ExpiresAt,
		})
	}
	return out
}

func (s *Store) saveDevicesLocked() error {
	if s.home == "" {
		return nil
	}
	if err := os.MkdirAll(s.home, 0o700); err != nil {
		return err
	}
	data, err := json.MarshalIndent(s.devices, "", "  ")
	if err != nil {
		return err
	}
	path := filepath.Join(s.home, "devices.json")
	temp := path + ".tmp"
	if err := os.WriteFile(temp, data, 0o600); err != nil {
		return err
	}
	return os.Rename(temp, path)
}

func (s *Store) loadDevices() error {
	if s.home == "" {
		return nil
	}
	data, err := os.ReadFile(filepath.Join(s.home, "devices.json"))
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	var devices map[string]*Device
	if err := json.Unmarshal(data, &devices); err != nil {
		return err
	}
	now := time.Now()
	for id, device := range devices {
		if device == nil || now.After(device.ExpiresAt) {
			delete(devices, id)
			continue
		}
		s.devices[id] = device
	}
	return nil
}

func sha256Bytes(value string) []byte {
	sum := sha256.Sum256([]byte(value))
	return sum[:]
}
