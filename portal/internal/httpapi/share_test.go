package httpapi

import (
	"testing"
	"time"
)

func TestShareStoreLifecycle(t *testing.T) {
	store := newShareStore()
	view, token, err := store.Create("env", "Alice", "dev@example.com", time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if view.ID == "" || view.EnvID != "env" || view.Label != "Alice" || token == "" {
		t.Fatalf("unexpected share: %+v token=%q", view, token)
	}
	got, ok := store.Lookup(token)
	if !ok || got.ID != view.ID {
		t.Fatalf("lookup failed: %+v ok=%v", got, ok)
	}
	if got.LastUsedAt == nil {
		t.Fatal("lastUsedAt not updated")
	}
	if shares := store.List("env"); len(shares) != 1 {
		t.Fatalf("unexpected shares: %+v", shares)
	}
	if !store.Revoke("env", view.ID) {
		t.Fatal("revoke failed")
	}
	if _, ok := store.Lookup(token); ok {
		t.Fatal("revoked token still valid")
	}
	if shares := store.List("env"); len(shares) != 0 {
		t.Fatalf("revoked share still listed: %+v", shares)
	}
}

func TestShareStoreRejectsExpiredAndBadTTL(t *testing.T) {
	store := newShareStore()
	if _, _, err := store.Create("env", "", "dev", time.Second); err == nil {
		t.Fatal("expected TTL rejection")
	}
	view, token, err := store.Create("env", "", "dev", time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	store.mu.Lock()
	store.byID[view.ID].ExpiresAt = time.Now().Add(-time.Second)
	store.mu.Unlock()
	if _, ok := store.Lookup(token); ok {
		t.Fatal("expired share accepted")
	}
}

func TestShareStoreEnforcesTTLBounds(t *testing.T) {
	store := newShareStore()
	for _, ttl := range []time.Duration{30 * time.Second, 8 * 24 * time.Hour} {
		if _, _, err := store.Create("env", "", "dev", ttl); err == nil {
			t.Fatalf("TTL %s accepted", ttl)
		}
	}
	if _, _, err := store.Create("env", "", "dev", time.Minute); err != nil {
		t.Fatalf("minimum TTL rejected: %v", err)
	}
}

func TestShareStoreEnforcesPerEnvLimit(t *testing.T) {
	store := newShareStore()
	for i := 0; i < maxSharesPerEnv; i++ {
		if _, _, err := store.Create("env", "", "dev", time.Hour); err != nil {
			t.Fatalf("share %d: %v", i, err)
		}
	}
	if _, _, err := store.Create("env", "", "dev", time.Hour); err == nil {
		t.Fatal("share limit not enforced")
	}
	store.RevokeEnv("env")
	if _, _, err := store.Create("env", "", "dev", time.Hour); err != nil {
		t.Fatalf("create after revoke: %v", err)
	}
}

func TestShareStoreExpireClosesConnections(t *testing.T) {
	store := newShareStore()
	expired := ""
	store.onExpire = func(envID, shareID string) {
		if envID != "env" || shareID == "" {
			t.Fatalf("unexpected expiry callback: env=%q share=%q", envID, shareID)
		}
		expired = shareID
	}
	view, token, err := store.Create("env", "", "dev", time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	closer := &testCloser{}
	store.Register(view.ID, closer)
	store.expire(view.ID)
	if !closer.closed {
		t.Fatal("connection not closed on expiry")
	}
	if expired != view.ID {
		t.Fatalf("expiry callback share = %q, want %q", expired, view.ID)
	}
	if _, ok := store.Lookup(token); ok {
		t.Fatal("expired token still valid")
	}
}

func TestShareStoreRevokeClosesConnections(t *testing.T) {
	store := newShareStore()
	view, _, err := store.Create("env", "", "dev", time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	closer := &testCloser{}
	unregister := store.Register(view.ID, closer)
	store.Revoke("env", view.ID)
	if !closer.closed {
		t.Fatal("connection not closed on revoke")
	}
	unregister()
}

type testCloser struct{ closed bool }

func (c *testCloser) Close() error { c.closed = true; return nil }
