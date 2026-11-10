package auth

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/coreos/go-oidc/v3/oidc"
	"golang.org/x/oauth2"

	"zeta/portal/internal/config"
)

type Service struct {
	cfg         config.AuthConfig
	sessionKey  []byte
	ttl         time.Duration
	devUsers    map[string]devUser
	provider    *oidc.Provider
	oauthConfig *oauth2.Config
	verifier    *oidc.IDTokenVerifier
	states      sync.Map
}

type devUser struct {
	Password string
	Email    string
	Groups   []string
}

type oidcState struct {
	Nonce       string
	Verifier    string
	RedirectURL string
	Created     time.Time
}

func New(ctx context.Context, cfg config.AuthConfig) (*Service, error) {
	ttl, err := time.ParseDuration(cfg.OIDCSessionTTL)
	if err != nil || ttl <= 0 {
		ttl = 12 * time.Hour
	}
	key := []byte(cfg.OIDCSessionKey)
	if len(key) == 0 {
		key = randomBytes(32)
	}
	s := &Service{
		cfg:        cfg,
		sessionKey: key,
		ttl:        ttl,
		devUsers:   defaultDevUsers(),
	}
	if cfg.OIDCIssuer != "" {
		provider, err := oidc.NewProvider(ctx, cfg.OIDCIssuer)
		if err != nil {
			return nil, err
		}
		scopes := strings.Split(cfg.OIDCScopes, ",")
		for i := range scopes {
			scopes[i] = strings.TrimSpace(scopes[i])
		}
		s.provider = provider
		s.oauthConfig = &oauth2.Config{
			ClientID:     cfg.OIDCClientID,
			ClientSecret: cfg.OIDCClientSecret,
			RedirectURL:  cfg.OIDCRedirectURL,
			Scopes:       scopes,
			Endpoint:     provider.Endpoint(),
		}
		s.verifier = provider.Verifier(&oidc.Config{ClientID: cfg.OIDCClientID})
	}
	return s, nil
}

func (s *Service) IsOIDC() bool { return s.provider != nil }
func (s *Service) IsDev() bool  { return s.provider == nil && s.cfg.OIDCDev }

func (s *Service) RequireAuth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		session, err := s.sessionFromRequest(r)
		if err != nil {
			if strings.HasPrefix(r.URL.Path, "/api/") {
				writeJSONError(w, http.StatusUnauthorized, "not authenticated")
				return
			}
			http.Redirect(w, r, "/login", http.StatusFound)
			return
		}
		next.ServeHTTP(w, r.WithContext(withSession(r.Context(), session)))
	})
}

func (s *Service) DevLogin(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if s.IsOIDC() {
		writeJSONError(w, http.StatusBadRequest, "OIDC configured; use /oidc/login")
		return
	}
	var body struct {
		Username string `json:"username"`
		Password string `json:"password"`
	}
	if err := decodeJSON(r, &body); err != nil {
		writeJSONError(w, http.StatusBadRequest, "JSON invalide")
		return
	}
	u, ok := s.devUsers[strings.ToLower(strings.TrimSpace(body.Username))]
	if !ok || u.Password != body.Password {
		writeJSONError(w, http.StatusUnauthorized, "identifiants invalides")
		return
	}
	session := Session{
		Username:  strings.Split(u.Email, "@")[0],
		Email:     u.Email,
		Groups:    u.Groups,
		ExpiresAt: time.Now().Add(s.ttl),
	}
	s.setSession(w, session)
	writeJSON(w, http.StatusOK, map[string]any{"email": session.Email, "groups": session.Groups})
}

func (s *Service) Logout(w http.ResponseWriter, r *http.Request) {
	s.clearSession(w)
	http.Redirect(w, r, "/", http.StatusFound)
}

func (s *Service) StartOIDC(w http.ResponseWriter, r *http.Request) {
	if s.provider == nil {
		http.Redirect(w, r, "/login", http.StatusFound)
		return
	}
	state := hex.EncodeToString(randomBytes(16))
	nonce := hex.EncodeToString(randomBytes(16))
	verifier := oauth2.GenerateVerifier()
	redirectURL := s.cfg.OIDCRedirectURL
	if redirectURL == "" {
		redirectURL = baseURL(r) + "/oidc/callback"
	}
	s.states.Store(state, oidcState{Nonce: nonce, Verifier: verifier, RedirectURL: redirectURL, Created: time.Now()})
	cfg := *s.oauthConfig
	cfg.RedirectURL = redirectURL
	http.Redirect(w, r, cfg.AuthCodeURL(state, oidc.Nonce(nonce), oauth2.S256ChallengeOption(verifier)), http.StatusFound)
}

func (s *Service) OIDCCallback(w http.ResponseWriter, r *http.Request) {
	if s.provider == nil {
		http.NotFound(w, r)
		return
	}
	state := r.URL.Query().Get("state")
	if state == "" {
		http.Error(w, "state manquant", http.StatusBadRequest)
		return
	}
	raw, ok := s.states.LoadAndDelete(state)
	if !ok {
		http.Error(w, "unknown or expired state", http.StatusBadRequest)
		return
	}
	st := raw.(oidcState)
	if time.Since(st.Created) > 10*time.Minute {
		http.Error(w, "state expired", http.StatusBadRequest)
		return
	}
	if e := r.URL.Query().Get("error"); e != "" {
		http.Error(w, "OIDC: "+e+" "+r.URL.Query().Get("error_description"), http.StatusBadRequest)
		return
	}
	cfg := *s.oauthConfig
	cfg.RedirectURL = st.RedirectURL
	token, err := cfg.Exchange(r.Context(), r.URL.Query().Get("code"), oauth2.VerifierOption(st.Verifier))
	if err != nil {
		http.Error(w, "OIDC code exchange failed: "+err.Error(), http.StatusBadGateway)
		return
	}
	rawIDToken, _ := token.Extra("id_token").(string)
	if rawIDToken == "" {
		http.Error(w, "id_token absent", http.StatusBadGateway)
		return
	}
	idToken, err := s.verifier.Verify(r.Context(), rawIDToken)
	if err != nil {
		http.Error(w, "OIDC verification failed: "+err.Error(), http.StatusUnauthorized)
		return
	}
	var claims struct {
		Email             string   `json:"email"`
		PreferredUsername string   `json:"preferred_username"`
		Groups            []string `json:"groups"`
	}
	if err := idToken.Claims(&claims); err != nil {
		http.Error(w, "claims OIDC invalides", http.StatusBadGateway)
		return
	}
	username := claims.PreferredUsername
	if username == "" {
		username = idToken.Subject
	}
	email := claims.Email
	if email == "" {
		email = idToken.Subject
	}
	s.setSession(w, Session{Username: username, Email: email, Groups: claims.Groups, ExpiresAt: time.Now().Add(s.ttl)})
	http.Redirect(w, r, "/", http.StatusFound)
}

func (s *Service) Me(w http.ResponseWriter, r *http.Request) {
	session, ok := SessionFromContext(r.Context())
	if !ok {
		writeJSONError(w, http.StatusUnauthorized, "not authenticated")
		return
	}
	writeJSON(w, http.StatusOK, session)
}

func defaultDevUsers() map[string]devUser {
	return map[string]devUser{
		"admin@example.com": {Password: "admin", Email: "admin@example.com", Groups: []string{"admins"}},
		"dev@example.com":   {Password: "dev", Email: "dev@example.com", Groups: []string{"devs"}},
		"alice@example.com": {Password: "alice", Email: "alice@example.com", Groups: []string{"backend"}},
	}
}

func randomBytes(n int) []byte {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	return b
}

func baseURL(r *http.Request) string {
	scheme := "http"
	if r.TLS != nil {
		scheme = "https"
	}
	if v := r.Header.Get("X-Forwarded-Proto"); v != "" {
		scheme = strings.Split(v, ",")[0]
	}
	host := r.Host
	if v := r.Header.Get("X-Forwarded-Host"); v != "" {
		host = strings.Split(v, ",")[0]
	}
	return scheme + "://" + host
}
