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

const sessionCookie = "zeta_session"

type Session struct {
	Username  string    `json:"username"`
	Email     string    `json:"email"`
	Groups    []string  `json:"groups"`
	ExpiresAt time.Time `json:"expiresAt"`
}

type sessionCtxKey struct{}

func SessionFromContext(ctx context.Context) (Session, bool) {
	s, ok := ctx.Value(sessionCtxKey{}).(Session)
	return s, ok
}

func withSession(ctx context.Context, s Session) context.Context {
	return context.WithValue(ctx, sessionCtxKey{}, s)
}

func (s *Service) setSession(w http.ResponseWriter, session Session) {
	payload, _ := json.Marshal(session)
	mac := hmac.New(sha256.New, s.sessionKey)
	mac.Write(payload)
	value := base64.RawURLEncoding.EncodeToString(payload) + "." + base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookie,
		Value:    value,
		Path:     "/",
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		Secure:   s.cfg.SecureCookie,
		MaxAge:   int(s.ttl.Seconds()),
	})
}

func (s *Service) clearSession(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookie,
		Value:    "",
		Path:     "/",
		MaxAge:   -1,
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		Secure:   s.cfg.SecureCookie,
	})
}

func (s *Service) sessionFromRequest(r *http.Request) (Session, error) {
	cookie, err := r.Cookie(sessionCookie)
	if err != nil {
		return Session{}, err
	}
	parts := strings.Split(cookie.Value, ".")
	if len(parts) != 2 {
		return Session{}, errors.New("session invalide")
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil {
		return Session{}, err
	}
	sig, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return Session{}, err
	}
	mac := hmac.New(sha256.New, s.sessionKey)
	mac.Write(payload)
	if !hmac.Equal(mac.Sum(nil), sig) {
		return Session{}, errors.New("signature de session invalide")
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
