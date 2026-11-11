package httpapi

import "testing"

type closeCounter struct{ closed int }

func (c *closeCounter) Close() error { c.closed++; return nil }

func TestIDEProxyCloseShareIsolation(t *testing.T) {
	proxy := newIDEProxy(nil)
	ownerCloser := &closeCounter{}
	shareCloser := &closeCounter{}
	ownerKey := ideKey{envID: "env"}
	shareKey := ideKey{envID: "env", shareID: "share"}
	proxy.entries[ownerKey] = &ideEntry{close: func() { ownerCloser.closed++ }}
	proxy.entries[shareKey] = &ideEntry{close: func() { shareCloser.closed++ }}

	proxy.CloseShare("env", "share")
	if shareCloser.closed != 1 {
		t.Fatalf("share closes = %d, want 1", shareCloser.closed)
	}
	if ownerCloser.closed != 0 {
		t.Fatalf("owner closes = %d, want 0", ownerCloser.closed)
	}
	if _, ok := proxy.entries[ownerKey]; !ok {
		t.Fatal("owner entry removed by share close")
	}

	proxy.Close("env")
	if ownerCloser.closed != 1 {
		t.Fatalf("owner closes = %d, want 1", ownerCloser.closed)
	}
	if len(proxy.entries) != 0 {
		t.Fatalf("entries = %d, want 0", len(proxy.entries))
	}
}
