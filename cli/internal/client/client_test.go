package client

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestLoginStoresCookie(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/login" {
			t.Fatalf("path = %s", r.URL.Path)
		}
		http.SetCookie(w, &http.Cookie{Name: "zeta_session", Value: "abc.def", Path: "/"})
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"email":"dev@example.com"}`))
	}))
	defer srv.Close()
	c, err := New(srv.URL, "")
	if err != nil {
		t.Fatal(err)
	}
	got, err := c.Login(context.Background(), "dev@example.com", "dev")
	if err != nil {
		t.Fatal(err)
	}
	if got != "abc.def" {
		t.Fatalf("cookie = %q", got)
	}
}

func TestCookieHeader(t *testing.T) {
	if got := cookieHeader("abc.def"); got != "zeta_session=abc.def" {
		t.Fatalf("got %q", got)
	}
	if got := cookieHeader("zeta_session=abc.def"); got != "zeta_session=abc.def" {
		t.Fatalf("got %q", got)
	}
}

func TestSubmitParsesID(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Cookie") != "zeta_session=abc.def" {
			t.Fatalf("cookie = %q", r.Header.Get("Cookie"))
		}
		w.WriteHeader(http.StatusAccepted)
		_, _ = w.Write([]byte(`{"id":"zeta-cpp-123"}`))
	}))
	defer srv.Close()
	c, err := New(srv.URL, "abc.def")
	if err != nil {
		t.Fatal(err)
	}
	id, err := c.Submit(context.Background(), SubmitRequest{Profile: "cpp"})
	if err != nil {
		t.Fatal(err)
	}
	if id != "zeta-cpp-123" {
		t.Fatalf("id = %q", id)
	}
}

func TestEnvExecURL(t *testing.T) {
	c := &Client{BaseURL: "http://zeta.local"}
	raw, err := c.EnvExecURL("demo", []string{"go", "test", "./..."}, false, false)
	if err != nil {
		t.Fatal(err)
	}
	if want := "ws://zeta.local/api/envs/demo/exec"; !strings.HasPrefix(raw, want) {
		t.Fatalf("url = %q, want prefix %q", raw, want)
	}
	if !strings.Contains(raw, "cmd=go") || !strings.Contains(raw, "cmd=test") || !strings.Contains(raw, "cmd=.%2F...") {
		t.Fatalf("missing command args: %q", raw)
	}
	if !strings.Contains(raw, "stdin=0") {
		t.Fatalf("missing stdin=0: %q", raw)
	}
	if strings.Contains(raw, "tty=1") {
		t.Fatalf("unexpected tty: %q", raw)
	}

	raw, err = c.EnvExecURL("demo", nil, true, true)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(raw, "tty=1") || strings.Contains(raw, "stdin=0") {
		t.Fatalf("interactive url = %q", raw)
	}
}

func TestEnvPortForwardURL(t *testing.T) {
	c := &Client{BaseURL: "https://zeta.example"}
	raw, err := c.EnvPortForwardURL("demo", 8080)
	if err != nil {
		t.Fatal(err)
	}
	if raw != "wss://zeta.example/api/envs/demo/portforward?port=8080" {
		t.Fatalf("url = %q", raw)
	}
}
