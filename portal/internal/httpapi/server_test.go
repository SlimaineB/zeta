package httpapi

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"zeta/portal/internal/auth"
	"zeta/portal/internal/domain"
)

func TestShouldReapEnvironment(t *testing.T) {
	active := domain.Environment{ID: "env", Active: true}
	cases := []struct {
		name    string
		env     domain.Environment
		busy    bool
		idle    time.Duration
		timeout time.Duration
		want    bool
	}{
		{"busy client", active, true, time.Hour, time.Minute, false},
		{"idle past timeout", active, false, time.Hour, time.Minute, true},
		{"idle under timeout", active, false, time.Second, time.Minute, false},
		{"already scaled down", domain.Environment{ID: "env"}, false, time.Hour, time.Minute, false},
		{"reaper disabled", active, false, time.Hour, 0, false},
	}
	for _, tc := range cases {
		if got := shouldReapEnvironment(tc.env, tc.busy, tc.idle, tc.timeout); got != tc.want {
			t.Errorf("%s: got %v, want %v", tc.name, got, tc.want)
		}
	}
}

func TestSameOriginHeader(t *testing.T) {
	cases := []struct {
		name   string
		host   string
		origin string
		fwd    string
		want   bool
	}{
		{"same host", "zeta.local", "http://zeta.local", "", true},
		{"same host with port", "zeta.local:8080", "http://zeta.local:8080", "", true},
		{"forwarded host", "zeta-portal:8080", "https://zeta.example", "zeta.example", true},
		{"different host", "zeta.local", "http://evil.example", "", false},
		{"missing origin", "zeta.local", "", "", false},
		{"null origin", "zeta.local", "null", "", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "/api/envs/demo/exec", nil)
			req.Host = tc.host
			if tc.origin != "" {
				req.Header.Set("Origin", tc.origin)
			}
			if tc.fwd != "" {
				req.Header.Set("X-Forwarded-Host", tc.fwd)
			}
			if got := sameOriginHeader(req) == nil; got != tc.want {
				t.Fatalf("sameOriginHeader() = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestSafeRequestOrigin(t *testing.T) {
	cases := []struct {
		name    string
		origin  string
		referer string
		site    string
		want    bool
	}{
		{"non-browser", "", "", "", true},
		{"same origin", "http://zeta.local", "", "", true},
		{"cross origin", "http://evil.example", "", "", false},
		{"cross site", "", "", "cross-site", false},
		{"same site subdomain", "", "", "same-site", false},
		{"same origin referer", "", "http://zeta.local/builds", "", true},
		{"cross origin referer", "", "http://evil.example/builds", "", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPost, "/api/builds", nil)
			req.Host = "zeta.local"
			if tc.origin != "" {
				req.Header.Set("Origin", tc.origin)
			}
			if tc.referer != "" {
				req.Header.Set("Referer", tc.referer)
			}
			if tc.site != "" {
				req.Header.Set("Sec-Fetch-Site", tc.site)
			}
			if got := safeRequestOrigin(req) == nil; got != tc.want {
				t.Fatalf("safeRequestOrigin() = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestBearerToken(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("Authorization", "bearer secret")
	if got := bearerToken(req); got != "secret" {
		t.Fatalf("bearerToken() = %q", got)
	}
	req.Header.Set("Authorization", "Basic secret")
	if got := bearerToken(req); got != "" {
		t.Fatalf("bearerToken() = %q, want empty", got)
	}
}

func TestCanManageEnv(t *testing.T) {
	alice := auth.Session{Email: "alice@example.com"}
	admin := auth.Session{Email: "admin@example.com", Groups: []string{"admins"}}
	cases := []struct {
		name    string
		session auth.Session
		owner   string
		want    bool
	}{
		{"legacy ownerless env", alice, "", true},
		{"owner", alice, "alice@example.com", true},
		{"case insensitive owner", alice, "Alice@Example.com", true},
		{"username owner", auth.Session{Username: "alice", Email: "alice@example.com"}, "alice", true},
		{"other owner", alice, "bob@example.com", false},
		{"admin", admin, "bob@example.com", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := canManageEnv(tc.session, tc.owner); got != tc.want {
				t.Fatalf("canManageEnv() = %v, want %v", got, tc.want)
			}
		})
	}
}
