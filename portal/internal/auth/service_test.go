package auth

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"zeta/portal/internal/config"
)

func testService() *Service {
	return &Service{
		cfg:        config.AuthConfig{OIDCIssuer: "https://id.example.com"},
		sessionKey: []byte("0123456789abcdef0123456789abcdef"),
		ttl:        time.Hour,
	}
}

func TestSessionCookieRoundTrip(t *testing.T) {
	s := testService()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rec := httptest.NewRecorder()
	s.setSession(rec, req, Session{
		Username:  "alice",
		Email:     "alice@example.com",
		Groups:    []string{"devs"},
		ExpiresAt: time.Now().Add(time.Hour),
	})

	cookies := rec.Result().Cookies()
	if len(cookies) != 1 || cookies[0].Name != sessionCookie {
		t.Fatalf("cookies = %+v", cookies)
	}
	req.AddCookie(cookies[0])
	session, err := s.sessionFromRequest(req)
	if err != nil {
		t.Fatal(err)
	}
	if session.Username != "alice" || session.Email != "alice@example.com" || len(session.Groups) != 1 {
		t.Fatalf("session = %+v", session)
	}
}

func TestSessionCookieRejectsTampering(t *testing.T) {
	s := testService()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rec := httptest.NewRecorder()
	s.setSession(rec, req, Session{Username: "alice", ExpiresAt: time.Now().Add(time.Hour)})

	cookie := rec.Result().Cookies()[0]
	cookie.Value += "x"
	req.AddCookie(cookie)
	if _, err := s.sessionFromRequest(req); err == nil {
		t.Fatal("expected tampered session to be rejected")
	}
}

func TestSessionCookieExpired(t *testing.T) {
	s := testService()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rec := httptest.NewRecorder()
	s.setSession(rec, req, Session{Username: "alice", ExpiresAt: time.Now().Add(-time.Minute)})

	req.AddCookie(rec.Result().Cookies()[0])
	if _, err := s.sessionFromRequest(req); err == nil {
		t.Fatal("expected expired session to be rejected")
	}
}

func TestFlowCookieRoundTrip(t *testing.T) {
	s := testService()
	flow := &oidcFlow{
		State:       "state",
		Nonce:       "nonce",
		Verifier:    "verifier",
		Next:        "/builds",
		RedirectURL: "https://zeta.example.com/oidc/callback",
		IssuedAt:    time.Now().Unix(),
	}
	value, err := s.signFlow(flow)
	if err != nil {
		t.Fatal(err)
	}
	got, err := s.parseFlow(value)
	if err != nil {
		t.Fatal(err)
	}
	if got.State != flow.State || got.Nonce != flow.Nonce || got.Verifier != flow.Verifier || got.Next != flow.Next {
		t.Fatalf("flow = %+v", got)
	}
}

func TestFlowCookieRejectsTampering(t *testing.T) {
	s := testService()
	value, err := s.signFlow(&oidcFlow{State: "state", IssuedAt: time.Now().Unix()})
	if err != nil {
		t.Fatal(err)
	}
	other := testService()
	other.sessionKey = []byte("another-key-another-key-another-ke")
	if _, err := other.parseFlow(value); err == nil {
		t.Fatal("expected wrong key to be rejected")
	}
}

func TestResolveRedirectURL(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "http://internal/", nil)
	req.Host = "zeta.example.com"
	req.Header.Set("X-Forwarded-Proto", "https")

	if got, want := resolveRedirectURL(req, ""), "https://zeta.example.com/oidc/callback"; got != want {
		t.Fatalf("redirect = %q, want %q", got, want)
	}
	if got, want := resolveRedirectURL(req, "https://custom.example/cb"), "https://custom.example/cb"; got != want {
		t.Fatalf("configured redirect = %q, want %q", got, want)
	}
}

func TestCookieSecure(t *testing.T) {
	s := testService()
	req := httptest.NewRequest(http.MethodGet, "http://zeta.example.com/", nil)
	if s.cookieSecure(req) {
		t.Fatal("plain HTTP request should not use a Secure cookie by default")
	}
	req.Header.Set("X-Forwarded-Proto", "https")
	if !s.cookieSecure(req) {
		t.Fatal("forwarded HTTPS request should use a Secure cookie")
	}
	s.cfg.SecureCookie = true
	req.Header.Del("X-Forwarded-Proto")
	if !s.cookieSecure(req) {
		t.Fatal("SecureCookie should force a Secure cookie")
	}
}

func TestSafeNext(t *testing.T) {
	cases := map[string]string{
		"":                     "/",
		"/builds":              "/builds",
		"/builds?id=1":         "/builds?id=1",
		"//evil.example":       "/",
		"/\\evil.example":      "/",
		"https://evil.example": "/",
	}
	for input, want := range cases {
		if got := safeNext(input); got != want {
			t.Fatalf("safeNext(%q) = %q, want %q", input, got, want)
		}
	}
}

func TestStringClaims(t *testing.T) {
	claims := map[string]any{
		"groups": []any{"devs", "platform"},
		"csv":    "a, b,c",
		"email":  "alice@example.com",
	}
	if got := stringListClaim(claims, "groups"); len(got) != 2 || got[1] != "platform" {
		t.Fatalf("groups = %+v", got)
	}
	if got := stringListClaim(claims, "csv"); len(got) != 3 {
		t.Fatalf("csv = %+v", got)
	}
	if got := stringClaim(claims, "email"); got != "alice@example.com" {
		t.Fatalf("email = %q", got)
	}
}

func TestAuthorizeGroupsAndUsers(t *testing.T) {
	s := testService()
	s.cfg.OIDCAllowedGroups = "devs, platform"
	if err := s.authorize("alice", "alice@example.com", "sub-1", []string{"devs"}); err != nil {
		t.Fatalf("allowed group rejected: %v", err)
	}
	if err := s.authorize("bob", "bob@example.com", "sub-2", []string{"ops"}); err == nil {
		t.Fatal("disallowed group accepted")
	}

	s.cfg.OIDCAllowedGroups = ""
	s.cfg.OIDCAllowedUsers = "alice@example.com"
	if err := s.authorize("alice", "alice@example.com", "sub-1", nil); err != nil {
		t.Fatalf("allowed user rejected: %v", err)
	}
	if err := s.authorize("bob", "bob@example.com", "sub-2", nil); err == nil {
		t.Fatal("disallowed user accepted")
	}
}

func TestNewSeparatesDiscoveryAndAuthorizationURL(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/.well-known/openid-configuration" {
			http.NotFound(w, r)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"issuer":                 "https://id.example.com",
			"authorization_endpoint": "http://internal.example.com/auth",
			"token_endpoint":         "http://internal.example.com/token",
			"userinfo_endpoint":      "http://internal.example.com/userinfo",
			"jwks_uri":               "http://internal.example.com/keys",
		})
	}))
	defer server.Close()

	s, err := New(context.Background(), config.AuthConfig{
		OIDCIssuer:           "https://id.example.com",
		OIDCDiscoveryURL:     server.URL,
		OIDCAuthorizationURL: "https://id.example.com/auth",
		OIDCClientID:         "zeta",
		OIDCSessionKey:       "0123456789abcdef0123456789abcdef",
	})
	if err != nil {
		t.Fatal(err)
	}
	if got := s.oauthConfig.Endpoint.AuthURL; got != "https://id.example.com/auth" {
		t.Fatalf("authorization endpoint = %q", got)
	}
	if got := s.oauthConfig.Endpoint.TokenURL; got != "http://internal.example.com/token" {
		t.Fatalf("token endpoint = %q", got)
	}
	if s.userInfoURL != "http://internal.example.com/userinfo" {
		t.Fatalf("userinfo URL = %q", s.userInfoURL)
	}
	if s.cfg.OIDCUsernameClaim != "preferred_username" || s.cfg.OIDCEmailClaim != "email" || s.cfg.OIDCGroupsClaim != "groups" {
		t.Fatalf("claim defaults = %+v", s.cfg)
	}
}
