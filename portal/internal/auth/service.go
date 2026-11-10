package auth

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
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
	oauthConfig *oauth2.Config
	verifier    *oidc.IDTokenVerifier
	userInfoURL string
}

type devUser struct {
	Password string
	Email    string
	Groups   []string
}

type oidcFlow struct {
	State       string `json:"state"`
	Nonce       string `json:"nonce"`
	Verifier    string `json:"verifier"`
	Next        string `json:"next"`
	RedirectURL string `json:"redirect_url"`
	IssuedAt    int64  `json:"issued_at"`
}

type discoveryDocument struct {
	Issuer                string `json:"issuer"`
	AuthorizationEndpoint string `json:"authorization_endpoint"`
	TokenEndpoint         string `json:"token_endpoint"`
	UserinfoEndpoint      string `json:"userinfo_endpoint"`
	JWKSURI               string `json:"jwks_uri"`
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
		if cfg.OIDCClientID == "" {
			return nil, errors.New("OIDC client ID is required")
		}
		if cfg.OIDCUsernameClaim == "" {
			cfg.OIDCUsernameClaim = "preferred_username"
		}
		if cfg.OIDCEmailClaim == "" {
			cfg.OIDCEmailClaim = "email"
		}
		if cfg.OIDCGroupsClaim == "" {
			cfg.OIDCGroupsClaim = "groups"
		}
		scopes := splitList(cfg.OIDCScopes)
		if len(scopes) == 0 {
			scopes = []string{oidc.ScopeOpenID, "profile", "email", "groups"}
		}
		discoveryURL := cfg.OIDCDiscoveryURL
		if discoveryURL == "" {
			discoveryURL = cfg.OIDCIssuer
		}
		doc, err := fetchDiscovery(ctx, discoveryURL)
		if err != nil {
			return nil, fmt.Errorf("OIDC discovery %s: %w", discoveryURL, err)
		}
		if doc.Issuer != cfg.OIDCIssuer {
			return nil, fmt.Errorf("OIDC discovery issuer %q does not match configured issuer %q", doc.Issuer, cfg.OIDCIssuer)
		}
		if doc.JWKSURI == "" {
			return nil, errors.New("OIDC discovery document has no jwks_uri")
		}
		if doc.AuthorizationEndpoint == "" || doc.TokenEndpoint == "" {
			return nil, errors.New("OIDC discovery document is missing authorization or token endpoint")
		}
		endpoint := oauth2.Endpoint{
			AuthURL:  doc.AuthorizationEndpoint,
			TokenURL: doc.TokenEndpoint,
		}
		if cfg.OIDCAuthorizationURL != "" {
			endpoint.AuthURL = cfg.OIDCAuthorizationURL
		}
		s.cfg = cfg
		s.oauthConfig = &oauth2.Config{
			ClientID:     cfg.OIDCClientID,
			ClientSecret: cfg.OIDCClientSecret,
			RedirectURL:  cfg.OIDCRedirectURL,
			Scopes:       scopes,
			Endpoint:     endpoint,
		}
		s.verifier = oidc.NewVerifier(cfg.OIDCIssuer, oidc.NewRemoteKeySet(ctx, doc.JWKSURI), &oidc.Config{ClientID: cfg.OIDCClientID})
		s.userInfoURL = doc.UserinfoEndpoint
	}
	return s, nil
}

func (s *Service) IsOIDC() bool { return s.oauthConfig != nil && s.verifier != nil }
func (s *Service) IsDev() bool  { return !s.IsOIDC() && s.cfg.OIDCDev }

func (s *Service) RequireAuth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		session, err := s.sessionFromRequest(r)
		if err != nil {
			if strings.HasPrefix(r.URL.Path, "/api/") {
				writeJSONError(w, http.StatusUnauthorized, "not authenticated")
				return
			}
			if s.IsOIDC() {
				http.Redirect(w, r, "/oidc/login?next="+url.QueryEscape(r.URL.RequestURI()), http.StatusFound)
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
	s.setSession(w, r, session)
	writeJSON(w, http.StatusOK, map[string]any{"email": session.Email, "groups": session.Groups})
}

func (s *Service) Logout(w http.ResponseWriter, r *http.Request) {
	s.clearSession(w, r)
	s.clearFlow(w, r)
	http.Redirect(w, r, "/", http.StatusFound)
}

func (s *Service) StartOIDC(w http.ResponseWriter, r *http.Request) {
	if !s.IsOIDC() {
		http.Redirect(w, r, "/login", http.StatusFound)
		return
	}
	state := hex.EncodeToString(randomBytes(32))
	nonce := hex.EncodeToString(randomBytes(32))
	verifier := oauth2.GenerateVerifier()
	flow := &oidcFlow{
		State:       state,
		Nonce:       nonce,
		Verifier:    verifier,
		Next:        safeNext(r.URL.Query().Get("next")),
		RedirectURL: resolveRedirectURL(r, s.cfg.OIDCRedirectURL),
		IssuedAt:    time.Now().Unix(),
	}
	if flow.RedirectURL == "" {
		http.Error(w, "OIDC redirect URL is not configured and cannot be derived from the request", http.StatusInternalServerError)
		return
	}
	value, err := s.signFlow(flow)
	if err != nil {
		http.Error(w, "OIDC: "+err.Error(), http.StatusInternalServerError)
		return
	}
	http.SetCookie(w, &http.Cookie{
		Name:     oidcCookie,
		Value:    value,
		Path:     "/",
		MaxAge:   600,
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		Secure:   s.cookieSecure(r),
	})
	cfg := *s.oauthConfig
	cfg.RedirectURL = flow.RedirectURL
	http.Redirect(w, r, cfg.AuthCodeURL(state, oauth2.SetAuthURLParam("nonce", nonce), oauth2.S256ChallengeOption(verifier)), http.StatusFound)
}

func (s *Service) OIDCCallback(w http.ResponseWriter, r *http.Request) {
	if !s.IsOIDC() {
		http.Redirect(w, r, "/login", http.StatusFound)
		return
	}
	if e := r.URL.Query().Get("error"); e != "" {
		s.clearFlow(w, r)
		message := "OIDC: " + e
		if description := r.URL.Query().Get("error_description"); description != "" {
			message += " (" + description + ")"
		}
		http.Error(w, message, http.StatusBadRequest)
		return
	}
	cookie, err := r.Cookie(oidcCookie)
	if err != nil {
		http.Error(w, "OIDC session expired, please try again", http.StatusUnauthorized)
		return
	}
	flow, err := s.parseFlow(cookie.Value)
	if err != nil {
		s.clearFlow(w, r)
		http.Error(w, "invalid OIDC state, please try again", http.StatusUnauthorized)
		return
	}
	if time.Since(time.Unix(flow.IssuedAt, 0)) > 10*time.Minute {
		s.clearFlow(w, r)
		http.Error(w, "OIDC login expired, please try again", http.StatusUnauthorized)
		return
	}
	state := r.URL.Query().Get("state")
	if state == "" || subtle.ConstantTimeCompare([]byte(state), []byte(flow.State)) != 1 {
		s.clearFlow(w, r)
		http.Error(w, "invalid OIDC state, please try again", http.StatusUnauthorized)
		return
	}
	code := r.URL.Query().Get("code")
	if code == "" {
		s.clearFlow(w, r)
		http.Error(w, "OIDC response has no authorization code", http.StatusUnauthorized)
		return
	}
	session, err := s.completeOIDC(r.Context(), code, flow)
	if err != nil {
		s.clearFlow(w, r)
		http.Error(w, "OIDC sign-in failed: "+err.Error(), http.StatusUnauthorized)
		return
	}
	s.setSession(w, r, session)
	s.clearFlow(w, r)
	http.Redirect(w, r, safeNext(flow.Next), http.StatusFound)
}

func (s *Service) completeOIDC(ctx context.Context, code string, flow *oidcFlow) (Session, error) {
	cfg := *s.oauthConfig
	if flow.RedirectURL != "" {
		cfg.RedirectURL = flow.RedirectURL
	}
	token, err := cfg.Exchange(ctx, code, oauth2.VerifierOption(flow.Verifier))
	if err != nil {
		return Session{}, fmt.Errorf("code exchange: %w", err)
	}
	rawIDToken, ok := token.Extra("id_token").(string)
	if !ok || rawIDToken == "" {
		return Session{}, errors.New("response has no id_token")
	}
	idToken, err := s.verifier.Verify(ctx, rawIDToken)
	if err != nil {
		return Session{}, fmt.Errorf("verification: %w", err)
	}
	if subtle.ConstantTimeCompare([]byte(idToken.Nonce), []byte(flow.Nonce)) != 1 {
		return Session{}, errors.New("nonce mismatch")
	}
	claims := map[string]any{}
	if err := idToken.Claims(&claims); err != nil {
		return Session{}, fmt.Errorf("claims: %w", err)
	}
	if extra, err := s.userInfo(ctx, token); err == nil {
		for key, value := range extra {
			if _, exists := claims[key]; !exists {
				claims[key] = value
			}
		}
	}
	username := stringClaim(claims, s.cfg.OIDCUsernameClaim)
	email := stringClaim(claims, s.cfg.OIDCEmailClaim)
	groups := stringListClaim(claims, s.cfg.OIDCGroupsClaim)
	if username == "" {
		username = email
	}
	if username == "" {
		username = idToken.Subject
	}
	if email == "" {
		email = idToken.Subject
	}
	if err := s.authorize(username, email, idToken.Subject, groups); err != nil {
		return Session{}, err
	}
	return Session{
		Username:  username,
		Email:     email,
		Groups:    groups,
		ExpiresAt: time.Now().Add(s.ttl),
	}, nil
}

func (s *Service) userInfo(ctx context.Context, token *oauth2.Token) (map[string]any, error) {
	if s.userInfoURL == "" {
		return nil, nil
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, s.userInfoURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+token.AccessToken)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("userinfo returned %s", resp.Status)
	}
	var claims map[string]any
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&claims); err != nil {
		return nil, err
	}
	return claims, nil
}

func (s *Service) authorize(username, email, subject string, groups []string) error {
	allowedGroups := splitList(s.cfg.OIDCAllowedGroups)
	if len(allowedGroups) > 0 {
		allowed := false
		for _, want := range allowedGroups {
			for _, have := range groups {
				if strings.EqualFold(want, have) {
					allowed = true
					break
				}
			}
			if allowed {
				break
			}
		}
		if !allowed {
			return fmt.Errorf("user %q is not a member of an allowed group", displayUser(username, email, subject))
		}
	}
	allowedUsers := splitList(s.cfg.OIDCAllowedUsers)
	if len(allowedUsers) > 0 {
		allowed := false
		for _, want := range allowedUsers {
			if strings.EqualFold(want, username) || strings.EqualFold(want, email) || strings.EqualFold(want, subject) {
				allowed = true
				break
			}
		}
		if !allowed {
			return fmt.Errorf("user %q is not allowed", displayUser(username, email, subject))
		}
	}
	return nil
}

func (s *Service) Me(w http.ResponseWriter, r *http.Request) {
	session, ok := SessionFromContext(r.Context())
	if !ok {
		writeJSONError(w, http.StatusUnauthorized, "not authenticated")
		return
	}
	writeJSON(w, http.StatusOK, session)
}

func fetchDiscovery(ctx context.Context, discoveryURL string) (discoveryDocument, error) {
	endpoint := strings.TrimRight(discoveryURL, "/") + "/.well-known/openid-configuration"
	if strings.HasSuffix(discoveryURL, "/.well-known/openid-configuration") {
		endpoint = discoveryURL
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return discoveryDocument{}, err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return discoveryDocument{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return discoveryDocument{}, fmt.Errorf("discovery returned %s", resp.Status)
	}
	var doc discoveryDocument
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&doc); err != nil {
		return discoveryDocument{}, err
	}
	return doc, nil
}

func (s *Service) signFlow(flow *oidcFlow) (string, error) {
	payload, err := json.Marshal(flow)
	if err != nil {
		return "", err
	}
	return s.signValue(payload), nil
}

func (s *Service) parseFlow(value string) (*oidcFlow, error) {
	payload, err := s.verifyValue(value)
	if err != nil {
		return nil, err
	}
	var flow oidcFlow
	if err := json.Unmarshal(payload, &flow); err != nil {
		return nil, err
	}
	return &flow, nil
}

func stringClaim(claims map[string]any, name string) string {
	if name == "" {
		return ""
	}
	value, ok := claims[name]
	if !ok {
		return ""
	}
	switch v := value.(type) {
	case string:
		return v
	case []any:
		if len(v) > 0 {
			if s, ok := v[0].(string); ok {
				return s
			}
		}
	case float64:
		return fmt.Sprintf("%v", v)
	}
	return ""
}

func stringListClaim(claims map[string]any, name string) []string {
	if name == "" {
		return nil
	}
	value, ok := claims[name]
	if !ok {
		return nil
	}
	switch v := value.(type) {
	case []any:
		out := make([]string, 0, len(v))
		for _, item := range v {
			if s, ok := item.(string); ok {
				out = append(out, s)
			}
		}
		return out
	case string:
		return splitList(v)
	}
	return nil
}

func splitList(value string) []string {
	fields := strings.FieldsFunc(value, func(r rune) bool {
		return r == ',' || r == ' ' || r == '\t' || r == '\n'
	})
	out := make([]string, 0, len(fields))
	for _, field := range fields {
		if field != "" {
			out = append(out, field)
		}
	}
	return out
}

func displayUser(username, email, subject string) string {
	switch {
	case username != "":
		return username
	case email != "":
		return email
	default:
		return subject
	}
}

func resolveRedirectURL(r *http.Request, configured string) string {
	if configured != "" {
		return configured
	}
	base := baseURL(r)
	if base == "" {
		return ""
	}
	return base + "/oidc/callback"
}

func safeNext(next string) string {
	if next == "" || !strings.HasPrefix(next, "/") || strings.HasPrefix(next, "//") || strings.ContainsAny(next, "\\\r\n") {
		return "/"
	}
	return next
}

func (s *Service) cookieSecure(r *http.Request) bool {
	if s.cfg.SecureCookie {
		return true
	}
	if r.TLS != nil {
		return true
	}
	proto := strings.TrimSpace(strings.Split(r.Header.Get("X-Forwarded-Proto"), ",")[0])
	return strings.EqualFold(proto, "https")
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
		scheme = strings.TrimSpace(strings.Split(v, ",")[0])
	}
	host := r.Host
	if v := r.Header.Get("X-Forwarded-Host"); v != "" {
		host = strings.TrimSpace(strings.Split(v, ",")[0])
	}
	if host == "" {
		return ""
	}
	return scheme + "://" + host
}
