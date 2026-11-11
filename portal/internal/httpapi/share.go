package httpapi

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"io"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"

	synchub "zeta/portal/internal/sync"
)

const (
	shareCookieName = "zeta_share"
	defaultShareTTL = time.Hour
	minShareTTL     = time.Minute
	maxShareTTL     = 7 * 24 * time.Hour
	maxSharesPerEnv = 20
)

type share struct {
	ID         string
	EnvID      string
	Label      string
	CreatedBy  string
	CreatedAt  time.Time
	ExpiresAt  time.Time
	LastUsedAt *time.Time
	tokenHash  [32]byte
	closers    map[io.Closer]struct{}
	timer      *time.Timer
}

type shareView struct {
	ID          string                `json:"id"`
	EnvID       string                `json:"envId"`
	Label       string                `json:"label"`
	CreatedBy   string                `json:"createdBy"`
	CreatedAt   time.Time             `json:"createdAt"`
	ExpiresAt   time.Time             `json:"expiresAt"`
	LastUsedAt  *time.Time            `json:"lastUsedAt,omitempty"`
	URL         string                `json:"url,omitempty"`
	Connections []synchub.Participant `json:"connections,omitempty"`
}

type shareStore struct {
	mu       sync.Mutex
	byID     map[string]*share
	byHash   map[string]*share
	onExpire func(envID, shareID string)
}

func newShareStore() *shareStore {
	return &shareStore{byID: map[string]*share{}, byHash: map[string]*share{}}
}

func (s *shareStore) Create(envID, label, createdBy string, ttl time.Duration) (shareView, string, error) {
	if ttl <= 0 {
		ttl = defaultShareTTL
	}
	if ttl < minShareTTL || ttl > maxShareTTL {
		return shareView{}, "", errors.New("la durée du partage doit être comprise entre 1m et 168h")
	}
	token, err := randomShareToken()
	if err != nil {
		return shareView{}, "", err
	}
	id, err := randomShareID()
	if err != nil {
		return shareView{}, "", err
	}
	now := time.Now()
	sh := &share{
		ID:        id,
		EnvID:     envID,
		Label:     label,
		CreatedBy: createdBy,
		CreatedAt: now,
		ExpiresAt: now.Add(ttl),
		tokenHash: sha256.Sum256([]byte(token)),
		closers:   map[io.Closer]struct{}{},
	}
	s.mu.Lock()
	count := 0
	for _, existing := range s.byID {
		if existing.EnvID == envID && now.Before(existing.ExpiresAt) {
			count++
		}
	}
	if count >= maxSharesPerEnv {
		s.mu.Unlock()
		return shareView{}, "", errors.New("trop de partages actifs pour cet environnement")
	}
	s.byID[id] = sh
	s.byHash[string(sh.tokenHash[:])] = sh
	sh.timer = time.AfterFunc(ttl, func() { s.expire(id) })
	s.mu.Unlock()
	return sh.view(), token, nil
}

func (s *shareStore) List(envID string) []shareView {
	now := time.Now()
	s.mu.Lock()
	shares := make([]shareView, 0)
	for _, sh := range s.byID {
		if sh.EnvID == envID && now.Before(sh.ExpiresAt) {
			shares = append(shares, sh.view())
		}
	}
	s.mu.Unlock()
	sort.Slice(shares, func(i, j int) bool {
		return shares[i].CreatedAt.After(shares[j].CreatedAt)
	})
	return shares
}

func (s *shareStore) Lookup(token string) (shareView, bool) {
	if strings.TrimSpace(token) == "" {
		return shareView{}, false
	}
	hash := sha256.Sum256([]byte(token))
	now := time.Now()
	s.mu.Lock()
	sh := s.byHash[string(hash[:])]
	if sh == nil {
		s.mu.Unlock()
		return shareView{}, false
	}
	if !now.Before(sh.ExpiresAt) {
		envID, shareID := sh.EnvID, sh.ID
		closers := s.deleteLocked(sh)
		s.mu.Unlock()
		closeAll(closers)
		if s.onExpire != nil {
			s.onExpire(envID, shareID)
		}
		return shareView{}, false
	}
	sh.LastUsedAt = &now
	view := sh.view()
	s.mu.Unlock()
	return view, true
}

func (s *shareStore) Revoke(envID, id string) bool {
	s.mu.Lock()
	sh := s.byID[id]
	if sh == nil || sh.EnvID != envID {
		s.mu.Unlock()
		return false
	}
	closers := s.deleteLocked(sh)
	s.mu.Unlock()
	closeAll(closers)
	return true
}

func (s *shareStore) RevokeEnv(envID string) {
	s.mu.Lock()
	var closers []io.Closer
	for _, sh := range s.byID {
		if sh.EnvID == envID {
			closers = append(closers, s.deleteLocked(sh)...)
		}
	}
	s.mu.Unlock()
	closeAll(closers)
}

func (s *shareStore) Register(shareID string, c io.Closer) func() {
	if shareID == "" || c == nil {
		return func() {}
	}
	s.mu.Lock()
	sh := s.byID[shareID]
	if sh == nil {
		s.mu.Unlock()
		return func() {}
	}
	sh.closers[c] = struct{}{}
	s.mu.Unlock()
	return func() {
		s.mu.Lock()
		if sh := s.byID[shareID]; sh != nil {
			delete(sh.closers, c)
		}
		s.mu.Unlock()
	}
}

func (s *shareStore) expire(id string) {
	s.mu.Lock()
	sh := s.byID[id]
	if sh == nil {
		s.mu.Unlock()
		return
	}
	envID, shareID := sh.EnvID, sh.ID
	closers := s.deleteLocked(sh)
	s.mu.Unlock()
	closeAll(closers)
	if s.onExpire != nil {
		s.onExpire(envID, shareID)
	}
}

func (s *shareStore) deleteLocked(sh *share) []io.Closer {
	if sh.timer != nil {
		sh.timer.Stop()
	}
	delete(s.byID, sh.ID)
	delete(s.byHash, string(sh.tokenHash[:]))
	closers := make([]io.Closer, 0, len(sh.closers))
	for c := range sh.closers {
		closers = append(closers, c)
	}
	return closers
}

func (sh *share) view() shareView {
	return shareView{
		ID:         sh.ID,
		EnvID:      sh.EnvID,
		Label:      sh.Label,
		CreatedBy:  sh.CreatedBy,
		CreatedAt:  sh.CreatedAt,
		ExpiresAt:  sh.ExpiresAt,
		LastUsedAt: sh.LastUsedAt,
	}
}

func (s *Server) shareToken(r *http.Request) string {
	cookie, err := r.Cookie(shareCookieName)
	if err != nil {
		return ""
	}
	return cookie.Value
}

func (s *Server) setShareCookie(w http.ResponseWriter, r *http.Request, token string, expires time.Time) {
	maxAge := int(time.Until(expires).Seconds())
	if maxAge < 1 {
		maxAge = 1
	}
	http.SetCookie(w, &http.Cookie{
		Name:     shareCookieName,
		Value:    token,
		Path:     "/",
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		Secure:   requestScheme(r) == "https",
		MaxAge:   maxAge,
	})
}

func (s *Server) clearShareCookie(w http.ResponseWriter, r *http.Request) {
	http.SetCookie(w, &http.Cookie{
		Name:     shareCookieName,
		Value:    "",
		Path:     "/",
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		Secure:   requestScheme(r) == "https",
		MaxAge:   -1,
	})
}

func randomShareToken() (string, error) {
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(buf), nil
}

func randomShareID() (string, error) {
	buf := make([]byte, 8)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return hex.EncodeToString(buf), nil
}

func closeAll(closers []io.Closer) {
	for _, c := range closers {
		_ = c.Close()
	}
}

func shareConnections(all []synchub.Participant, shareID string) []synchub.Participant {
	if shareID == "" {
		return nil
	}
	out := make([]synchub.Participant, 0)
	for _, p := range all {
		if p.ShareID == shareID {
			out = append(out, p)
		}
	}
	return out
}
