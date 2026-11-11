package sync

import (
	"testing"
	"time"
)

func TestHubActivity(t *testing.T) {
	h := NewHub()
	if h.Active("env") {
		t.Fatal("environment should not be active yet")
	}
	if h.IdleFor("env") < time.Hour {
		t.Fatal("never-active environment should be considered idle for a long time")
	}
	h.Touch("env")
	if h.Active("env") {
		t.Fatal("touching alone should not mark it active")
	}
	h.BeginClient("env")
	if !h.Active("env") {
		t.Fatal("environment should be active with a client")
	}
	h.EndClient("env")
	if h.Active("env") {
		t.Fatal("environment should be idle after the client leaves")
	}
	h.BeginExec("env")
	if !h.Active("env") {
		t.Fatal("environment should be active with a shell")
	}
	h.EndExec("env")
	if h.Active("env") {
		t.Fatal("environment should be idle after the shell closes")
	}
	h.Forget("env")
	if h.IdleFor("env") < time.Hour {
		t.Fatal("forget should clear activity")
	}
}

func TestHubEnsureIdempotent(t *testing.T) {
	h := NewHub()
	if h.Has("env") {
		t.Fatal("session should not exist yet")
	}
	h.Ensure("env", "tok")
	if !h.Has("env") {
		t.Fatal("session should exist")
	}
	h.Ensure("env", "tok2")
	if !h.Has("env") {
		t.Fatal("session should still exist")
	}
	h.mu.Lock()
	token := h.sessions["env"].token
	h.mu.Unlock()
	if token != "tok2" {
		t.Fatalf("token = %q, want tok2", token)
	}
	h.Delete("env")
	if h.Has("env") {
		t.Fatal("session should be deleted")
	}
}

func TestPresenceTracksConnections(t *testing.T) {
	h := NewHub()
	release := h.BeginIDE("env", "user:dev", "dev@example.com", "", false)
	if !h.Active("env") {
		t.Fatal("IDE connection should keep the environment active")
	}
	participants := h.Presence("env")
	if len(participants) != 1 || participants[0].Kind != "ide" || participants[0].Name != "dev@example.com" {
		t.Fatalf("unexpected presence: %+v", participants)
	}
	release()
	if h.Active("env") {
		t.Fatal("released IDE connection should not keep the environment active")
	}
	if len(h.Presence("env")) != 0 {
		t.Fatalf("presence not cleared: %+v", h.Presence("env"))
	}
}

func TestPresenceAggregatesAndTracksShares(t *testing.T) {
	h := NewHub()
	release1 := h.BeginShell("env", "share:abc", "Alice", "abc", true)
	release2 := h.BeginShell("env", "share:abc", "Alice", "abc", true)
	participants := h.Presence("env")
	if len(participants) != 1 || participants[0].Count != 2 || !participants[0].Guest || participants[0].ShareID != "abc" {
		t.Fatalf("unexpected presence: %+v", participants)
	}
	release1()
	if participants := h.Presence("env"); len(participants) != 1 || participants[0].Count != 1 {
		t.Fatalf("unexpected presence after release: %+v", participants)
	}
	release2()
	if len(h.Presence("env")) != 0 {
		t.Fatal("presence not cleared")
	}
}
