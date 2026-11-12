package sync

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"fmt"
	"sort"
	"sync"
	"time"

	"golang.org/x/net/websocket"
	"golang.org/x/time/rate"
)

type Hub struct {
	mu             sync.Mutex
	sessions       map[string]*session
	activity       map[string]time.Time
	clients        map[string]int
	execs          map[string]int
	presences      map[string]map[string]*presence
	maxBytesPerSec int64
}

type Participant struct {
	ID      string    `json:"id"`
	Name    string    `json:"name"`
	Kind    string    `json:"kind"`
	Since   time.Time `json:"since"`
	ShareID string    `json:"shareId,omitempty"`
	Guest   bool      `json:"guest,omitempty"`
	Count   int       `json:"count,omitempty"`
}

type presence struct {
	Participant
	count int
}

type session struct {
	id         string
	token      string
	agent      *websocket.Conn
	client     *websocket.Conn
	agentDone  chan struct{}
	clientDone chan struct{}
	started    bool
	done       chan struct{}
	closeOnce  sync.Once
	limiter    *rate.Limiter
}

func NewHub(maxBytesPerSec ...int64) *Hub {
	var limit int64
	if len(maxBytesPerSec) > 0 {
		limit = maxBytesPerSec[0]
	}
	return &Hub{
		sessions:       map[string]*session{},
		activity:       map[string]time.Time{},
		clients:        map[string]int{},
		execs:          map[string]int{},
		presences:      map[string]map[string]*presence{},
		maxBytesPerSec: limit,
	}
}

func (h *Hub) newSession(id, token string) *session {
	s := &session{id: id, token: token, done: make(chan struct{})}
	if h.maxBytesPerSec > 0 {
		burst := int(h.maxBytesPerSec)
		if burst < 2<<20 {
			burst = 2 << 20
		}
		s.limiter = rate.NewLimiter(rate.Limit(h.maxBytesPerSec), burst)
	}
	return s
}

// Touch records user activity for an environment, resetting its idle timer.
func (h *Hub) Touch(id string) {
	h.mu.Lock()
	h.activity[id] = time.Now()
	h.mu.Unlock()
}

func (h *Hub) BeginClient(id string) {
	h.mu.Lock()
	h.clients[id]++
	h.activity[id] = time.Now()
	h.mu.Unlock()
}

func (h *Hub) EndClient(id string) {
	h.mu.Lock()
	if h.clients[id] > 0 {
		h.clients[id]--
	}
	h.activity[id] = time.Now()
	h.mu.Unlock()
}

func (h *Hub) BeginExec(id string) {
	h.mu.Lock()
	h.execs[id]++
	h.activity[id] = time.Now()
	h.mu.Unlock()
}

func (h *Hub) EndExec(id string) {
	h.mu.Lock()
	if h.execs[id] > 0 {
		h.execs[id]--
	}
	h.activity[id] = time.Now()
	h.mu.Unlock()
}

func (h *Hub) BeginSyncClient(id, key, name, shareID string, guest bool) func() {
	return h.begin(id, "sync", key, name, shareID, guest, true)
}

func (h *Hub) BeginIDE(id, key, name, shareID string, guest bool) func() {
	return h.begin(id, "ide", key, name, shareID, guest, true)
}

func (h *Hub) BeginShell(id, key, name, shareID string, guest bool) func() {
	return h.begin(id, "shell", key, name, shareID, guest, false)
}

func (h *Hub) begin(id, kind, key, name, shareID string, guest, client bool) func() {
	if key == "" {
		key = NewToken()
	}
	presenceKey := kind + "|" + key
	h.mu.Lock()
	if client {
		h.clients[id]++
	} else {
		h.execs[id]++
	}
	h.activity[id] = time.Now()
	if h.presences[id] == nil {
		h.presences[id] = map[string]*presence{}
	}
	if p := h.presences[id][presenceKey]; p != nil {
		p.count++
		p.Name = name
		p.ShareID = shareID
		p.Guest = guest
	} else {
		h.presences[id][presenceKey] = &presence{
			Participant: Participant{
				ID:      presenceKey,
				Name:    name,
				Kind:    kind,
				Since:   time.Now(),
				ShareID: shareID,
				Guest:   guest,
				Count:   1,
			},
			count: 1,
		}
	}
	h.mu.Unlock()
	var once sync.Once
	return func() {
		once.Do(func() {
			h.mu.Lock()
			if client {
				if h.clients[id] > 0 {
					h.clients[id]--
				}
			} else if h.execs[id] > 0 {
				h.execs[id]--
			}
			if p := h.presences[id][presenceKey]; p != nil {
				p.count--
				if p.count <= 0 {
					delete(h.presences[id], presenceKey)
				} else {
					p.Count = p.count
				}
			}
			if len(h.presences[id]) == 0 {
				delete(h.presences, id)
			}
			h.activity[id] = time.Now()
			h.mu.Unlock()
		})
	}
}

func (h *Hub) Presence(id string) []Participant {
	h.mu.Lock()
	defer h.mu.Unlock()
	out := make([]Participant, 0, len(h.presences[id]))
	for _, p := range h.presences[id] {
		p.Count = p.count
		out = append(out, p.Participant)
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i].Since.Before(out[j].Since)
	})
	return out
}

// Active reports whether a sync client or an interactive shell is attached.
func (h *Hub) Active(id string) bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.clients[id] > 0 || h.execs[id] > 0
}

// HasActivity reports whether the hub has ever seen activity for an environment.
func (h *Hub) HasActivity(id string) bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	_, ok := h.activity[id]
	return ok
}

// IdleFor returns how long the environment has been without activity.
func (h *Hub) IdleFor(id string) time.Duration {
	h.mu.Lock()
	defer h.mu.Unlock()
	last, ok := h.activity[id]
	if !ok {
		return time.Duration(1 << 62)
	}
	return time.Since(last)
}

// Forget drops activity bookkeeping for an environment that was removed.
func (h *Hub) Forget(id string) {
	h.mu.Lock()
	delete(h.activity, id)
	delete(h.clients, id)
	delete(h.execs, id)
	delete(h.presences, id)
	h.mu.Unlock()
}

func NewToken() string {
	buf := make([]byte, 16)
	if _, err := rand.Read(buf); err != nil {
		panic(fmt.Sprintf("generating sync token: %v", err))
	}
	return fmt.Sprintf("%x", buf)
}

func (h *Hub) Create(id, token string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if old := h.sessions[id]; old != nil {
		h.closeLocked(old)
	}
	h.sessions[id] = h.newSession(id, token)
}

// Ensure creates the session if it does not exist yet, or updates its token.
// It is used by long-lived environments whose agent reconnects after portal
// restarts or pod restarts.
func (h *Hub) Ensure(id, token string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if s, ok := h.sessions[id]; ok {
		if token != "" {
			s.token = token
		}
		return
	}
	h.sessions[id] = h.newSession(id, token)
}

func (h *Hub) Has(id string) bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	_, ok := h.sessions[id]
	return ok
}

func (h *Hub) Delete(id string) {
	h.mu.Lock()
	s := h.sessions[id]
	h.mu.Unlock()
	if s != nil {
		h.close(s)
	}
}

func (h *Hub) AttachAgent(id, token string, conn *websocket.Conn) error {
	h.mu.Lock()
	s := h.sessions[id]
	if s == nil {
		h.mu.Unlock()
		return fmt.Errorf("session inconnue")
	}
	if subtle.ConstantTimeCompare([]byte(s.token), []byte(token)) != 1 {
		h.mu.Unlock()
		return fmt.Errorf("token de session invalide")
	}
	if s.agent != nil {
		if s.started {
			h.mu.Unlock()
			return fmt.Errorf("agent already connected")
		}
		if s.agentDone != nil {
			close(s.agentDone)
		}
		_ = s.agent.Close()
		s.agent = nil
		s.agentDone = nil
	}
	s.agent = conn
	s.agentDone = make(chan struct{})
	start := s.client != nil && !s.started
	if start {
		s.started = true
		go h.relay(s)
	}
	done := s.done
	agentDone := s.agentDone
	h.mu.Unlock()
	select {
	case <-done:
	case <-agentDone:
	}
	return nil
}

func (h *Hub) AttachClient(id string, conn *websocket.Conn) error {
	h.mu.Lock()
	s := h.sessions[id]
	if s == nil {
		h.mu.Unlock()
		return fmt.Errorf("session inconnue")
	}
	if s.client != nil {
		if s.started {
			h.mu.Unlock()
			return fmt.Errorf("client already connected")
		}
		if s.clientDone != nil {
			close(s.clientDone)
		}
		_ = s.client.Close()
		s.client = nil
		s.clientDone = nil
	}
	s.client = conn
	s.clientDone = make(chan struct{})
	start := s.agent != nil && !s.started
	if start {
		s.started = true
		go h.relay(s)
	}
	done := s.done
	clientDone := s.clientDone
	h.mu.Unlock()
	select {
	case <-done:
	case <-clientDone:
	}
	return nil
}

func (h *Hub) relay(s *session) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	defer h.close(s)
	errs := make(chan error, 2)
	go func() { errs <- pipe(ctx, s.agent, s.client, s.limiter) }()
	go func() { errs <- pipe(ctx, s.client, s.agent, s.limiter) }()
	<-errs
}

func pipe(ctx context.Context, dst, src *websocket.Conn, limiter *rate.Limiter) error {
	for {
		var msg []byte
		if err := websocket.Message.Receive(src, &msg); err != nil {
			return err
		}
		if limiter != nil {
			if err := limiter.WaitN(ctx, len(msg)); err != nil {
				return err
			}
		}
		if err := websocket.Message.Send(dst, msg); err != nil {
			return err
		}
	}
}

func (h *Hub) close(s *session) {
	s.closeOnce.Do(func() {
		if s.agent != nil {
			_ = s.agent.Close()
		}
		if s.client != nil {
			_ = s.client.Close()
		}
		h.mu.Lock()
		delete(h.sessions, s.id)
		h.mu.Unlock()
		close(s.done)
	})
}

func (h *Hub) closeLocked(s *session) {
	s.closeOnce.Do(func() {
		if s.agent != nil {
			_ = s.agent.Close()
		}
		if s.client != nil {
			_ = s.client.Close()
		}
		delete(h.sessions, s.id)
		close(s.done)
	})
}
