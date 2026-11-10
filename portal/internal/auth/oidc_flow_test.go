package auth

import (
	"context"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"math/big"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"zeta/portal/internal/config"
)

func TestCompleteOIDCVerifiesNonceAndMergesUserinfo(t *testing.T) {
	provider := newTestOIDCProvider(t, "test-nonce")
	s, err := New(context.Background(), config.AuthConfig{
		OIDCIssuer:       provider.URL,
		OIDCDiscoveryURL: provider.URL,
		OIDCClientID:     "zeta",
		OIDCClientSecret: "secret",
		OIDCRedirectURL:  "http://portal.example/oidc/callback",
		OIDCSessionKey:   "0123456789abcdef0123456789abcdef",
		OIDCSessionTTL:   "1h",
	})
	if err != nil {
		t.Fatal(err)
	}
	flow := &oidcFlow{
		Nonce:       "test-nonce",
		Verifier:    "verifier",
		RedirectURL: "http://portal.example/oidc/callback",
		IssuedAt:    time.Now().Unix(),
	}
	session, err := s.completeOIDC(context.Background(), "code", flow)
	if err != nil {
		t.Fatal(err)
	}
	if session.Username != "alice" || session.Email != "alice@example.com" {
		t.Fatalf("session = %+v", session)
	}
	if len(session.Groups) != 1 || session.Groups[0] != "devs" {
		t.Fatalf("groups = %+v", session.Groups)
	}
}

func TestCompleteOIDCRejectsNonceMismatch(t *testing.T) {
	provider := newTestOIDCProvider(t, "test-nonce")
	s, err := New(context.Background(), config.AuthConfig{
		OIDCIssuer:       provider.URL,
		OIDCDiscoveryURL: provider.URL,
		OIDCClientID:     "zeta",
		OIDCRedirectURL:  "http://portal.example/oidc/callback",
		OIDCSessionKey:   "0123456789abcdef0123456789abcdef",
	})
	if err != nil {
		t.Fatal(err)
	}
	flow := &oidcFlow{
		Nonce:       "wrong-nonce",
		Verifier:    "verifier",
		RedirectURL: "http://portal.example/oidc/callback",
		IssuedAt:    time.Now().Unix(),
	}
	if _, err := s.completeOIDC(context.Background(), "code", flow); err == nil || !strings.Contains(err.Error(), "nonce") {
		t.Fatalf("expected nonce error, got %v", err)
	}
}

func TestCompleteOIDCEnforcesAllowlist(t *testing.T) {
	provider := newTestOIDCProvider(t, "test-nonce")
	s, err := New(context.Background(), config.AuthConfig{
		OIDCIssuer:        provider.URL,
		OIDCDiscoveryURL:  provider.URL,
		OIDCClientID:      "zeta",
		OIDCRedirectURL:   "http://portal.example/oidc/callback",
		OIDCSessionKey:    "0123456789abcdef0123456789abcdef",
		OIDCAllowedGroups: "ops",
	})
	if err != nil {
		t.Fatal(err)
	}
	flow := &oidcFlow{
		Nonce:       "test-nonce",
		Verifier:    "verifier",
		RedirectURL: "http://portal.example/oidc/callback",
		IssuedAt:    time.Now().Unix(),
	}
	if _, err := s.completeOIDC(context.Background(), "code", flow); err == nil || !strings.Contains(err.Error(), "allowed group") {
		t.Fatalf("expected allowlist error, got %v", err)
	}
}

func newTestOIDCProvider(t *testing.T, nonce string) *httptest.Server {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	var provider *httptest.Server
	mux := http.NewServeMux()
	mux.HandleFunc("/.well-known/openid-configuration", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"issuer":                 provider.URL,
			"authorization_endpoint": provider.URL + "/auth",
			"token_endpoint":         provider.URL + "/token",
			"userinfo_endpoint":      provider.URL + "/userinfo",
			"jwks_uri":               provider.URL + "/keys",
		})
	})
	mux.HandleFunc("/keys", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"keys": []any{map[string]any{
				"kty": "RSA",
				"kid": "test",
				"use": "sig",
				"alg": "RS256",
				"n":   base64.RawURLEncoding.EncodeToString(key.PublicKey.N.Bytes()),
				"e":   base64.RawURLEncoding.EncodeToString(big.NewInt(int64(key.PublicKey.E)).Bytes()),
			}},
		})
	})
	mux.HandleFunc("/token", func(w http.ResponseWriter, r *http.Request) {
		idToken := signTestIDToken(t, key, map[string]any{
			"iss":                provider.URL,
			"aud":                "zeta",
			"sub":                "sub-1",
			"exp":                time.Now().Add(5 * time.Minute).Unix(),
			"iat":                time.Now().Unix(),
			"nonce":              nonce,
			"preferred_username": "alice",
			"email":              "alice@example.com",
		})
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"access_token": "access-token",
			"token_type":   "Bearer",
			"id_token":     idToken,
		})
	})
	mux.HandleFunc("/userinfo", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"groups": []string{"devs"}})
	})
	provider = httptest.NewServer(mux)
	t.Cleanup(provider.Close)
	return provider
}

func signTestIDToken(t *testing.T, key *rsa.PrivateKey, claims map[string]any) string {
	t.Helper()
	header, err := json.Marshal(map[string]any{"alg": "RS256", "kid": "test", "typ": "JWT"})
	if err != nil {
		t.Fatal(err)
	}
	payload, err := json.Marshal(claims)
	if err != nil {
		t.Fatal(err)
	}
	signingInput := base64.RawURLEncoding.EncodeToString(header) + "." + base64.RawURLEncoding.EncodeToString(payload)
	digest := sha256.Sum256([]byte(signingInput))
	signature, err := rsa.SignPKCS1v15(rand.Reader, key, crypto.SHA256, digest[:])
	if err != nil {
		t.Fatal(err)
	}
	return signingInput + "." + base64.RawURLEncoding.EncodeToString(signature)
}

func TestStartOIDCSetsFlowCookieAndNext(t *testing.T) {
	provider := newTestOIDCProvider(t, "test-nonce")
	s, err := New(context.Background(), config.AuthConfig{
		OIDCIssuer:       provider.URL,
		OIDCDiscoveryURL: provider.URL,
		OIDCClientID:     "zeta",
		OIDCRedirectURL:  "http://portal.example/oidc/callback",
		OIDCSessionKey:   "0123456789abcdef0123456789abcdef",
	})
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodGet, "/oidc/login?next=%2Fbuilds%3Fx%3D1", nil)
	rec := httptest.NewRecorder()
	s.StartOIDC(rec, req)
	if rec.Code != http.StatusFound {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	location := rec.Header().Get("Location")
	if !strings.HasPrefix(location, provider.URL+"/auth?") || !strings.Contains(location, "code_challenge=") || !strings.Contains(location, "nonce=") {
		t.Fatalf("location = %q", location)
	}
	cookies := rec.Result().Cookies()
	if len(cookies) != 1 || cookies[0].Name != oidcCookie || cookies[0].Value == "" {
		t.Fatalf("cookies = %+v", cookies)
	}
	flow, err := s.parseFlow(cookies[0].Value)
	if err != nil {
		t.Fatal(err)
	}
	if flow.Next != "/builds?x=1" || flow.State == "" || flow.Nonce == "" || flow.Verifier == "" {
		t.Fatalf("flow = %+v", flow)
	}
}

func TestRequireAuthRedirectsToOIDCWithNext(t *testing.T) {
	provider := newTestOIDCProvider(t, "test-nonce")
	s, err := New(context.Background(), config.AuthConfig{
		OIDCIssuer:       provider.URL,
		OIDCDiscoveryURL: provider.URL,
		OIDCClientID:     "zeta",
		OIDCRedirectURL:  "http://portal.example/oidc/callback",
		OIDCSessionKey:   "0123456789abcdef0123456789abcdef",
	})
	if err != nil {
		t.Fatal(err)
	}
	handler := s.RequireAuth(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatal("protected handler should not run without a session")
	}))
	req := httptest.NewRequest(http.MethodGet, "/builds?x=1", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusFound {
		t.Fatalf("status = %d", rec.Code)
	}
	if got, want := rec.Header().Get("Location"), "/oidc/login?next=%2Fbuilds%3Fx%3D1"; got != want {
		t.Fatalf("location = %q, want %q", got, want)
	}
}
