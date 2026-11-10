package client

import (
	"context"
	"net/http"
	"net/http/httptest"
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
