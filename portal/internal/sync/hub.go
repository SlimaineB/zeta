package sync

import (
	"crypto/rand"
	"crypto/subtle"
	"fmt"
	"sync"
	"time"

	"golang.org/x/net/websocket"
)

type Hub struct {
	mu       sync.Mutex
	sessions map[string]*session
	activity map[string]time.Time
	clients  map[string]int
	execs    map[string]int
}

type session struct {
	id        string
	token     string
	agent     *websocket.Conn
	client    *websocket.Conn
	started   bool
	done      chan struct{}
	closeOnce sync.Once
}

func NewHub() *Hub {
	return &Hub{
		sessions: map[string]*session{},
		activity: map[string]time.Time{},
		clients:  map[string]int{},
		execs:    map[string]int{},
	}
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
	h.sessions[id] = &session{id: id, token: token, done: make(chan struct{})}
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
	h.sessions[id] = &session{id: id, token: token, done: make(chan struct{})}
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
		h.mu.Unlock()
		return fmt.Errorf("agent already connected")
	}
	s.agent = conn
	start := s.agent != nil && s.client != nil && !s.started
	if start {
		s.started = true
		go h.relay(s)
	}
	h.mu.Unlock()
	<-s.done
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
		h.mu.Unlock()
		return fmt.Errorf("client already connected")
	}
	s.client = conn
	start := s.agent != nil && s.client != nil && !s.started
	if start {
		s.started = true
		go h.relay(s)
	}
	h.mu.Unlock()
	<-s.done
	return nil
}

func (h *Hub) relay(s *session) {
	defer h.close(s)
	errs := make(chan error, 2)
	go func() { errs <- pipe(s.agent, s.client) }()
	go func() { errs <- pipe(s.client, s.agent) }()
	<-errs
}

func pipe(dst, src *websocket.Conn) error {
	for {
		var msg string
		if err := websocket.Message.Receive(src, &msg); err != nil {
			return err
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
