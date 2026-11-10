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
