package auth

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestTokenVerify(t *testing.T) {
	store := NewTokenStore("secret-token", time.Hour)
	if !store.VerifyBootstrap("secret-token") {
		t.Fatal("expected valid token")
	}
	if store.VerifyBootstrap("wrong") {
		t.Fatal("expected invalid token")
	}
	if store.VerifyBootstrap("") {
		t.Fatal("empty secret must fail")
	}
}

func TestPasswordVerify(t *testing.T) {
	store, err := NewStore(t.TempDir(), "orbit-lantern-cobalt-ridge", time.Hour, nil)
	if err != nil {
		t.Fatalf("new store: %v", err)
	}
	if store.Mode() != "password" {
		t.Fatalf("mode = %q, want password", store.Mode())
	}
	if !store.VerifyBootstrap("orbit-lantern-cobalt-ridge") {
		t.Fatal("expected valid password")
	}
	if store.VerifyBootstrap("orbit-lantern-cobalt-ridgf") {
		t.Fatal("expected invalid password")
	}
}

func TestPasswordPolicy(t *testing.T) {
	if err := ValidatePassword("short"); err == nil {
		t.Fatal("expected a short password to fail")
	}
	if err := ValidatePassword("password1234"); err == nil {
		t.Fatal("expected a common password to fail")
	}
	if err := ValidatePassword("orbit-lantern-cobalt-ridge"); err != nil {
		t.Fatalf("expected a strong passphrase to pass: %v", err)
	}
}

func TestNewStoreRejectsWeakPassword(t *testing.T) {
	if _, err := NewStore(t.TempDir(), "short", time.Hour, nil); err == nil {
		t.Fatal("expected a weak password to be rejected")
	}
}

func TestTokenPersistsAcrossRestarts(t *testing.T) {
	home := t.TempDir()
	first, err := NewStore(home, "", time.Hour, nil)
	if err != nil {
		t.Fatalf("first store: %v", err)
	}
	second, err := NewStore(home, "", time.Hour, nil)
	if err != nil {
		t.Fatalf("second store: %v", err)
	}
	if first.Token() != second.Token() {
		t.Fatal("token changed across restarts")
	}
	info, err := os.Stat(filepath.Join(home, "pairing-token"))
	if err != nil {
		t.Fatalf("token file: %v", err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("token file mode = %o, want 600", info.Mode().Perm())
	}
}

func TestExchangeBootstrapCreatesDevice(t *testing.T) {
	store := NewTokenStore("tok", time.Hour)
	id, secret, ok := store.Exchange("tok", "laptop")
	if !ok || id == "" || secret == "" {
		t.Fatalf("exchange failed: id=%q secret=%q ok=%v", id, secret, ok)
	}
	if !store.AuthenticateDevice(id, secret) {
		t.Fatal("expected the device to authenticate")
	}
	if store.AuthenticateDevice(id, "wrong") {
		t.Fatal("expected a wrong device secret to fail")
	}
}

func TestPairingCodeIsSingleUse(t *testing.T) {
	store := NewTokenStore("tok", time.Hour)
	code, _ := store.NewPairingCode()

	id, secret, ok := store.Exchange(code, "phone")
	if !ok {
		t.Fatal("expected the code to be accepted")
	}
	if !store.AuthenticateDevice(id, secret) {
		t.Fatal("expected the new device to authenticate")
	}
	if _, _, ok := store.Exchange(code, "phone2"); ok {
		t.Fatal("expected the code to be rejected on reuse")
	}
}

func TestPairingCodeExpires(t *testing.T) {
	store := NewTokenStore("tok", time.Hour)
	store.mu.Lock()
	store.codes["expired"] = time.Now().Add(-time.Second)
	store.mu.Unlock()
	if store.ConsumePairingCode("expired") {
		t.Fatal("expected an expired code to fail")
	}
}

func TestRevokeDevice(t *testing.T) {
	store := NewTokenStore("tok", time.Hour)
	id, secret, ok := store.Exchange("tok", "laptop")
	if !ok {
		t.Fatal("exchange failed")
	}
	if !store.RevokeDevice(id) {
		t.Fatal("expected revoke to succeed")
	}
	if store.AuthenticateDevice(id, secret) {
		t.Fatal("revoked device must not authenticate")
	}
	if store.RevokeDevice(id) {
		t.Fatal("double revoke should report false")
	}
}

func TestDevicesPersistAcrossRestarts(t *testing.T) {
	home := t.TempDir()
	first, err := NewStore(home, "", time.Hour, nil)
	if err != nil {
		t.Fatalf("first store: %v", err)
	}
	id, secret, ok := first.Exchange(first.Token(), "laptop")
	if !ok {
		t.Fatal("exchange failed")
	}

	second, err := NewStore(home, "", time.Hour, nil)
	if err != nil {
		t.Fatalf("second store: %v", err)
	}
	if !second.AuthenticateDevice(id, secret) {
		t.Fatal("device did not survive a restart")
	}
	if len(second.ListDevices()) != 1 {
		t.Fatalf("devices = %d, want 1", len(second.ListDevices()))
	}
}

func TestRateLimit(t *testing.T) {
	store := NewTokenStore("t", time.Hour)
	for i := 0; i < FailureLimit; i++ {
		if !store.Allow("1.2.3.4") {
			t.Fatalf("blocked too early at attempt %d", i)
		}
		store.RecordFailure("1.2.3.4")
	}
	if store.Allow("1.2.3.4") {
		t.Fatal("expected block after the failure limit")
	}
	if !store.Allow("5.6.7.8") {
		t.Fatal("a different IP should still be allowed")
	}
}
