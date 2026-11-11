package auth

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"
)

const (
	sessionCookie = "zeta_session"
	oidcCookie    = "zeta_oidc"
)

type Session struct {
	Username  string    `json:"username"`
	Email     string    `json:"email"`
	Groups    []string  `json:"groups"`
	ExpiresAt time.Time `json:"expiresAt"`
	Guest     bool      `json:"guest,omitempty"`
	ShareID   string    `json:"shareId,omitempty"`
	ShareEnv  string    `json:"shareEnv,omitempty"`
}

type sessionCtxKey struct{}

func SessionFromContext(ctx context.Context) (Session, bool) {
	s, ok := ctx.Value(sessionCtxKey{}).(Session)
	return s, ok
}

func withSession(ctx context.Context, s Session) context.Context {
	return context.WithValue(ctx, sessionCtxKey{}, s)
}

func WithSession(ctx context.Context, s Session) context.Context {
	return withSession(ctx, s)
}

func (s *Service) RequestSession(r *http.Request) (Session, error) {
	return s.sessionFromRequest(r)
}

func (s *Service) setSession(w http.ResponseWriter, r *http.Request, session Session) {
	payload, _ := json.Marshal(session)
	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookie,
		Value:    s.signValue(payload),
		Path:     "/",
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		Secure:   s.cookieSecure(r),
		MaxAge:   int(s.ttl.Seconds()),
	})
}

func (s *Service) clearSession(w http.ResponseWriter, r *http.Request) {
	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookie,
		Value:    "",
		Path:     "/",
		MaxAge:   -1,
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		Secure:   s.cookieSecure(r),
	})
}

func (s *Service) clearFlow(w http.ResponseWriter, r *http.Request) {
	http.SetCookie(w, &http.Cookie{
		Name:     oidcCookie,
		Value:    "",
		Path:     "/",
		MaxAge:   -1,
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		Secure:   s.cookieSecure(r),
	})
}

func (s *Service) sessionFromRequest(r *http.Request) (Session, error) {
	cookie, err := r.Cookie(sessionCookie)
	if err != nil {
		return Session{}, err
	}
	payload, err := s.verifyValue(cookie.Value)
	if err != nil {
		return Session{}, err
	}
	var session Session
	if err := json.Unmarshal(payload, &session); err != nil {
		return Session{}, err
	}
	if time.Now().After(session.ExpiresAt) {
		return Session{}, errors.New("session expired")
	}
	return session, nil
}

func (s *Service) signValue(payload []byte) string {
	mac := hmac.New(sha256.New, s.sessionKey)
	mac.Write(payload)
	return base64.RawURLEncoding.EncodeToString(payload) + "." + base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}

func (s *Service) verifyValue(value string) ([]byte, error) {
	parts := strings.Split(value, ".")
	if len(parts) != 2 {
		return nil, errors.New("invalid signature")
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil {
		return nil, errors.New("invalid signature")
	}
	signature, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return nil, errors.New("invalid signature")
	}
	mac := hmac.New(sha256.New, s.sessionKey)
	mac.Write(payload)
	if !hmac.Equal(signature, mac.Sum(nil)) {
		return nil, errors.New("invalid signature")
	}
	return payload, nil
}
