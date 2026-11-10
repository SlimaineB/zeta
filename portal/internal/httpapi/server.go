package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"golang.org/x/net/websocket"
	apierrs "k8s.io/apimachinery/pkg/api/errors"

	"zeta/portal/internal/auth"
	"zeta/portal/internal/builds"
	"zeta/portal/internal/config"
	"zeta/portal/internal/domain"
	synchub "zeta/portal/internal/sync"
	"zeta/portal/internal/ui"
)

const maxContextUpload = 512 << 20

type Server struct {
	cfg    config.Config
	builds *builds.Service
	auth   *auth.Service
	sync   *synchub.Hub
}

func New(cfg config.Config, buildService *builds.Service, authService *auth.Service, syncHub *synchub.Hub) *Server {
	return &Server{cfg: cfg, builds: buildService, auth: authService, sync: syncHub}
}

func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()

	protected := func(h http.HandlerFunc) http.Handler {
		return s.auth.RequireAuth(s.sameOriginGuard(h))
	}

	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("ok\n"))
	})
	mux.HandleFunc("GET /login", s.login)
	mux.Handle("POST /api/login", s.sameOriginGuard(http.HandlerFunc(s.auth.DevLogin)))
	mux.Handle("POST /api/logout", s.sameOriginGuard(http.HandlerFunc(s.auth.Logout)))
	mux.HandleFunc("GET /oidc/login", s.auth.StartOIDC)
	mux.HandleFunc("GET /oidc/callback", s.auth.OIDCCallback)

	mux.Handle("GET /api/me", protected(s.auth.Me))
	mux.Handle("GET /api/profiles", protected(s.profiles))
	mux.Handle("GET /api/builds", protected(s.listBuilds))
	mux.Handle("POST /api/builds", protected(s.submitBuild))
	mux.Handle("GET /api/builds/{id}/logs", protected(s.logs))
	mux.Handle("GET /api/builds/{id}/metrics", protected(s.metrics))
	mux.Handle("GET /api/builds/{id}/artifact", protected(s.artifact))
	mux.Handle("GET /api/volumes", protected(s.volumes))
	mux.Handle("POST /api/volumes/{name}/resize", protected(s.resizeVolume))
	mux.Handle("DELETE /api/volumes/{name}", protected(s.deleteVolume))
	mux.Handle("POST /api/envs", protected(s.createEnv))
	mux.Handle("GET /api/envs", protected(s.listEnvs))
	mux.Handle("POST /api/envs/{id}/activate", protected(s.activateEnv))
	mux.Handle("POST /api/envs/{id}/deactivate", protected(s.deactivateEnv))
	mux.Handle("DELETE /api/envs/{id}", protected(s.deleteEnv))
	mux.Handle("GET /api/envs/{id}/exec", protected(s.envExec))
	mux.HandleFunc("GET /api/sync/{id}/agent", s.syncAgent)
	mux.Handle("GET /api/sync/{id}/client", protected(s.syncClient))

	mux.HandleFunc("GET /", s.static)
	return mux
}

func (s *Server) sameOriginGuard(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet, http.MethodHead, http.MethodOptions:
			next.ServeHTTP(w, r)
			return
		}
		if err := safeRequestOrigin(r); err != nil {
			writeError(w, http.StatusForbidden, err)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func requestHost(r *http.Request) string {
	host := r.Host
	if forwarded := r.Header.Get("X-Forwarded-Host"); forwarded != "" {
		host = strings.TrimSpace(strings.Split(forwarded, ",")[0])
	}
	return host
}

func hostName(host string) string {
	host = strings.TrimSpace(host)
	if h, _, err := net.SplitHostPort(host); err == nil {
		return strings.Trim(h, "[]")
	}
	return strings.Trim(host, "[]")
}

func originMatchesHost(origin, host string) error {
	u, err := url.Parse(origin)
	if err != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") {
		return fmt.Errorf("invalid origin")
	}
	if !strings.EqualFold(hostName(u.Host), hostName(host)) {
		return fmt.Errorf("origin %q does not match host %q", origin, host)
	}
	return nil
}

func sameOriginHeader(r *http.Request) error {
	origin := strings.TrimSpace(r.Header.Get("Origin"))
	if origin == "" {
		return fmt.Errorf("missing origin")
	}
	return originMatchesHost(origin, requestHost(r))
}

func safeRequestOrigin(r *http.Request) error {
	if site := strings.ToLower(strings.TrimSpace(r.Header.Get("Sec-Fetch-Site"))); site != "" && site != "same-origin" && site != "none" {
		return fmt.Errorf("cross-site request rejected")
	}
	if origin := strings.TrimSpace(r.Header.Get("Origin")); origin != "" {
		return originMatchesHost(origin, requestHost(r))
	}
	if referer := strings.TrimSpace(r.Header.Get("Referer")); referer != "" {
		u, err := url.Parse(referer)
		if err != nil || u.Host == "" {
			return fmt.Errorf("invalid referer")
		}
		if !strings.EqualFold(hostName(u.Host), hostName(requestHost(r))) {
			return fmt.Errorf("cross-origin referer rejected")
		}
	}
	return nil
}

func serveWebSocket(w http.ResponseWriter, r *http.Request, h func(*websocket.Conn), checkOrigin bool) {
	server := websocket.Server{Handler: h}
	if checkOrigin {
		server.Handshake = func(_ *websocket.Config, req *http.Request) error {
			return sameOriginHeader(req)
		}
	}
	server.ServeHTTP(w, r)
}

func bearerToken(r *http.Request) string {
	const prefix = "Bearer "
	value := strings.TrimSpace(r.Header.Get("Authorization"))
	if len(value) > len(prefix) && strings.EqualFold(value[:len(prefix)], prefix) {
		return strings.TrimSpace(value[len(prefix):])
	}
	return ""
}

func sessionOwner(session auth.Session) string {
	if email := strings.TrimSpace(session.Email); email != "" {
		return email
	}
	return strings.TrimSpace(session.Username)
}

func canManageEnv(session auth.Session, owner string) bool {
	owner = strings.TrimSpace(owner)
	if owner == "" || isAdmin(session) {
		return true
	}
	return strings.EqualFold(owner, strings.TrimSpace(session.Email)) ||
		strings.EqualFold(owner, strings.TrimSpace(session.Username))
}

func isAdmin(session auth.Session) bool {
	for _, group := range session.Groups {
		switch strings.ToLower(strings.TrimSpace(group)) {
		case "admin", "admins":
			return true
		}
	}
	return false
}

func (s *Server) login(w http.ResponseWriter, r *http.Request) {
	if s.auth.IsOIDC() {
		target := "/oidc/login"
		if next := r.URL.Query().Get("next"); next != "" {
			target += "?next=" + url.QueryEscape(next)
		}
		http.Redirect(w, r, target, http.StatusFound)
		return
	}
	s.serveFile(w, r, "web/login.html")
}

func (s *Server) profiles(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, s.builds.Profiles())
}

func (s *Server) listBuilds(w http.ResponseWriter, r *http.Request) {
	builds, err := s.builds.List(r.Context())
	if err != nil {
		writeError(w, http.StatusBadGateway, err)
		return
	}
	writeJSON(w, http.StatusOK, builds)
}

func (s *Server) submitBuild(w http.ResponseWriter, r *http.Request) {
	if strings.HasPrefix(r.Header.Get("Content-Type"), "multipart/form-data") {
		s.submitContextBuild(w, r)
		return
	}
	var req domain.SubmitRequest
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, fmt.Errorf("JSON invalide"))
		return
	}
	session, _ := auth.SessionFromContext(r.Context())
	id, err := s.builds.Submit(r.Context(), req, sessionOwner(session))
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]string{"id": id})
}

func (s *Server) submitContextBuild(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, maxContextUpload+1<<20)
	mr, err := r.MultipartReader()
	if err != nil {
		writeError(w, http.StatusBadRequest, fmt.Errorf("multipart invalide"))
		return
	}
	var req domain.SubmitRequest
	var contextPath string
	cleanup := func() {
		if contextPath != "" {
			_ = s.builds.DeleteContext(r.Context(), contextPath)
		}
	}
	for {
		part, err := mr.NextPart()
		if err == io.EOF {
			break
		}
		if err != nil {
			cleanup()
			var maxErr *http.MaxBytesError
			if errors.As(err, &maxErr) {
				writeError(w, http.StatusRequestEntityTooLarge, fmt.Errorf("context too large"))
				return
			}
			writeError(w, http.StatusBadRequest, fmt.Errorf("invalid multipart"))
			return
		}
		switch part.FormName() {
		case "profile", "repo", "commit", "sync":
			data, readErr := io.ReadAll(io.LimitReader(part, 1<<20))
			_ = part.Close()
			if readErr != nil {
				cleanup()
				writeError(w, http.StatusBadRequest, fmt.Errorf("invalid field %s", part.FormName()))
				return
			}
			switch part.FormName() {
			case "profile":
				req.Profile = strings.TrimSpace(string(data))
			case "repo":
				req.Repo = strings.TrimSpace(string(data))
			case "commit":
				req.Commit = strings.TrimSpace(string(data))
			case "sync":
				req.Sync = strings.EqualFold(strings.TrimSpace(string(data)), "true") || strings.TrimSpace(string(data)) == "1"
			}
		case "context":
			if contextPath != "" {
				_ = part.Close()
				cleanup()
				writeError(w, http.StatusBadRequest, fmt.Errorf("duplicate context"))
				return
			}
			contextPath, err = s.builds.StoreContext(r.Context(), part)
			_ = part.Close()
			if err != nil {
				cleanup()
				var maxErr *http.MaxBytesError
				if errors.As(err, &maxErr) {
					writeError(w, http.StatusRequestEntityTooLarge, fmt.Errorf("context too large"))
					return
				}
				writeError(w, http.StatusBadRequest, err)
				return
			}
		default:
			_, _ = io.Copy(io.Discard, part)
			_ = part.Close()
		}
	}
	if contextPath == "" {
		writeError(w, http.StatusBadRequest, fmt.Errorf("missing context"))
		return
	}
	session, _ := auth.SessionFromContext(r.Context())
	syncToken := ""
	if req.Sync {
		syncToken = synchub.NewToken()
	}
	id, err := s.builds.SubmitContext(r.Context(), req, sessionOwner(session), contextPath, syncToken)
	if err != nil {
		cleanup()
		writeError(w, http.StatusBadRequest, err)
		return
	}
	if syncToken != "" {
		s.sync.Create(id, syncToken)
	}
	writeJSON(w, http.StatusAccepted, map[string]string{"id": id})
}

func (s *Server) createEnv(w http.ResponseWriter, r *http.Request) {
	var req domain.EnvRequest
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, fmt.Errorf("invalid JSON"))
		return
	}
	session, _ := auth.SessionFromContext(r.Context())
	env, err := s.builds.CreateEnvironment(r.Context(), req, sessionOwner(session), synchub.NewToken())
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	writeJSON(w, http.StatusCreated, env)
}

func (s *Server) listEnvs(w http.ResponseWriter, r *http.Request) {
	envs, err := s.builds.Environments(r.Context())
	if err != nil {
		writeError(w, http.StatusBadGateway, err)
		return
	}
	writeJSON(w, http.StatusOK, envs)
}

func authorizeOwner(w http.ResponseWriter, session auth.Session, kind, id, owner string) bool {
	if !canManageEnv(session, owner) {
		writeError(w, http.StatusForbidden, fmt.Errorf("%s %s belongs to %s", kind, id, owner))
		return false
	}
	return true
}

func (s *Server) authorizeEnv(w http.ResponseWriter, r *http.Request, id string) bool {
	session, _ := auth.SessionFromContext(r.Context())
	owner, err := s.builds.EnvironmentOwner(r.Context(), id)
	if err != nil {
		if apierrs.IsNotFound(err) {
			writeError(w, http.StatusNotFound, fmt.Errorf("environment %s not found; run 'zeta init' first", id))
			return false
		}
		writeError(w, http.StatusNotFound, err)
		return false
	}
	return authorizeOwner(w, session, "environment", id, owner)
}

func (s *Server) authorizeBuild(w http.ResponseWriter, r *http.Request, id string) bool {
	session, _ := auth.SessionFromContext(r.Context())
	owner, err := s.builds.BuildOwner(r.Context(), id)
	if err != nil {
		writeError(w, http.StatusNotFound, err)
		return false
	}
	return authorizeOwner(w, session, "build", id, owner)
}

func (s *Server) authorizeSyncTarget(w http.ResponseWriter, r *http.Request, id string) bool {
	session, _ := auth.SessionFromContext(r.Context())
	if owner, err := s.builds.BuildOwner(r.Context(), id); err == nil {
		return authorizeOwner(w, session, "build", id, owner)
	}
	if owner, err := s.builds.EnvironmentOwner(r.Context(), id); err == nil {
		return authorizeOwner(w, session, "environment", id, owner)
	}
	writeError(w, http.StatusNotFound, fmt.Errorf("sync target %s not found", id))
	return false
}

func (s *Server) activateEnv(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if !s.authorizeEnv(w, r, id) {
		return
	}
	token, err := s.builds.EnvironmentToken(r.Context(), id)
	if err != nil {
		writeError(w, http.StatusNotFound, err)
		return
	}
	s.sync.Ensure(id, token)
	s.sync.Touch(id)
	if err := s.builds.ActivateEnvironment(r.Context(), id); err != nil {
		writeError(w, http.StatusBadGateway, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"id": id, "active": true})
}

func (s *Server) deactivateEnv(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if !s.authorizeEnv(w, r, id) {
		return
	}
	if err := s.builds.DeactivateEnvironment(r.Context(), id); err != nil {
		writeError(w, http.StatusBadGateway, err)
		return
	}
	s.sync.Delete(id)
	s.sync.Forget(id)
	writeJSON(w, http.StatusOK, map[string]any{"id": id, "active": false})
}

func (s *Server) deleteEnv(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if !s.authorizeEnv(w, r, id) {
		return
	}
	if err := s.builds.DeleteEnvironment(r.Context(), id); err != nil {
		writeError(w, http.StatusBadGateway, err)
		return
	}
	s.sync.Delete(id)
	s.sync.Forget(id)
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) envExec(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if !s.authorizeEnv(w, r, id) {
		return
	}
	serveWebSocket(w, r, func(ws *websocket.Conn) {
		defer ws.Close()
		s.sync.BeginExec(id)
		defer s.sync.EndExec(id)
		ctx, cancel := context.WithCancel(r.Context())
		defer cancel()
		stdin, stdinWriter := io.Pipe()
		output := &execOutput{ws: ws}
		sizes := newExecSizeQueue()
		defer sizes.close()
		go func() {
			for {
				var msg []byte
				if err := websocket.Message.Receive(ws, &msg); err != nil {
					_ = stdinWriter.CloseWithError(err)
					cancel()
					return
				}
				if len(msg) == 0 {
					continue
				}
				switch msg[0] {
				case 'i':
					if _, err := stdinWriter.Write(msg[1:]); err != nil {
						cancel()
						return
					}
				case 'r':
					var size struct {
						Cols uint16 `json:"cols"`
						Rows uint16 `json:"rows"`
					}
					if json.Unmarshal(msg[1:], &size) == nil {
						sizes.push(domain.TerminalSize{Width: size.Cols, Height: size.Rows})
					}
				}
			}
		}()
		err := s.builds.ExecEnvironment(ctx, id, domain.ExecOptions{
			Container: "env",
			Command:   []string{"sh", "-c", "cd /workspace && (command -v bash >/dev/null && exec bash -i || exec sh -i)"},
			Stdin:     stdin,
			Stdout:    output,
			TTY:       true,
			SizeQueue: sizes,
		})
		_ = stdinWriter.Close()
		if err != nil && ctx.Err() == nil {
			_, _ = output.Write([]byte("\r\n[zeta] " + err.Error() + "\r\n"))
		}
	}, true)
}

type execOutput struct {
	ws *websocket.Conn
	mu sync.Mutex
}

func (o *execOutput) Write(p []byte) (int, error) {
	o.mu.Lock()
	defer o.mu.Unlock()
	frame := make([]byte, len(p)+1)
	frame[0] = 'o'
	copy(frame[1:], p)
	if err := websocket.Message.Send(o.ws, frame); err != nil {
		return 0, err
	}
	return len(p), nil
}

type execSizeQueue struct {
	ch   chan domain.TerminalSize
	once sync.Once
}

func newExecSizeQueue() *execSizeQueue {
	return &execSizeQueue{ch: make(chan domain.TerminalSize, 8)}
}

func (q *execSizeQueue) push(size domain.TerminalSize) {
	select {
	case q.ch <- size:
	default:
	}
}

func (q *execSizeQueue) close() {
	q.once.Do(func() { close(q.ch) })
}

func (q *execSizeQueue) Next() *domain.TerminalSize {
	size, ok := <-q.ch
	if !ok {
		return nil
	}
	return &size
}

func shouldReapEnvironment(env domain.Environment, active bool, idle, timeout time.Duration) bool {
	if !env.Active || timeout <= 0 || active {
		return false
	}
	return idle >= timeout
}

// RunIdleReaper periodically scales down environments that have had no sync
// client or interactive shell for longer than timeout. A non-positive timeout
// disables the reaper.
func (s *Server) RunIdleReaper(ctx context.Context, timeout time.Duration) {
	if timeout <= 0 {
		return
	}
	if envs, err := s.builds.Environments(ctx); err == nil {
		for _, env := range envs {
			if env.Active {
				s.sync.Touch(env.ID)
			}
		}
	}
	interval := time.Minute
	if timeout < interval {
		interval = timeout
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
		envs, err := s.builds.Environments(ctx)
		if err != nil {
			log.Printf("env reaper: %v", err)
			continue
		}
		for _, env := range envs {
			if env.Active && !s.sync.HasActivity(env.ID) {
				// New or post-restart environment: grant a full idle window
				// instead of reaping it on the first tick.
				s.sync.Touch(env.ID)
				continue
			}
			if !shouldReapEnvironment(env, s.sync.Active(env.ID), s.sync.IdleFor(env.ID), timeout) {
				continue
			}
			if err := s.builds.DeactivateEnvironment(ctx, env.ID); err != nil {
				log.Printf("env reaper: deactivate %s: %v", env.ID, err)
				continue
			}
			s.sync.Delete(env.ID)
			s.sync.Forget(env.ID)
			log.Printf("env reaper: %s scaled to zero after %s idle", env.ID, timeout)
		}
	}
}

func (s *Server) ensureEnvSession(ctx context.Context, id string) {
	if s.sync.Has(id) {
		return
	}
	if token, err := s.builds.EnvironmentToken(ctx, id); err == nil {
		s.sync.Ensure(id, token)
	}
}

func (s *Server) syncAgent(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	token := bearerToken(r)
	if token == "" {
		token = r.URL.Query().Get("token")
	}
	if token == "" {
		http.Error(w, "missing token", http.StatusUnauthorized)
		return
	}
	s.ensureEnvSession(r.Context(), id)
	serveWebSocket(w, r, func(ws *websocket.Conn) {
		defer ws.Close()
		_ = s.sync.AttachAgent(id, token, ws)
	}, false)
}

func (s *Server) syncClient(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if !s.authorizeSyncTarget(w, r, id) {
		return
	}
	s.ensureEnvSession(r.Context(), id)
	serveWebSocket(w, r, func(ws *websocket.Conn) {
		defer ws.Close()
		s.sync.BeginClient(id)
		defer s.sync.EndClient(id)
		_ = s.sync.AttachClient(id, ws)
	}, true)
}

func (s *Server) logs(w http.ResponseWriter, r *http.Request) {
	if !s.authorizeBuild(w, r, r.PathValue("id")) {
		return
	}
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("X-Accel-Buffering", "no")
	flusher, _ := w.(http.Flusher)
	if err := s.builds.StreamLogs(r.Context(), r.PathValue("id"), &flushWriter{w: w, f: flusher}); err != nil {
		fmt.Fprintf(w, "\n[erreur logs: %v]\n", err)
	}
}

func (s *Server) metrics(w http.ResponseWriter, r *http.Request) {
	if !s.authorizeBuild(w, r, r.PathValue("id")) {
		return
	}
	stats, err := s.builds.Metrics(r.Context(), r.PathValue("id"))
	if err != nil {
		writeError(w, http.StatusBadGateway, err)
		return
	}
	writeJSON(w, http.StatusOK, stats)
}

func (s *Server) volumes(w http.ResponseWriter, r *http.Request) {
	volumes, err := s.builds.Volumes(r.Context())
	if err != nil {
		writeError(w, http.StatusBadGateway, err)
		return
	}
	writeJSON(w, http.StatusOK, volumes)
}

func (s *Server) resizeVolume(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Size string `json:"size"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, fmt.Errorf("JSON invalide"))
		return
	}
	name := r.PathValue("name")
	if strings.TrimSpace(name) == "" || strings.TrimSpace(req.Size) == "" {
		writeError(w, http.StatusBadRequest, fmt.Errorf("nom et taille requis"))
		return
	}
	if err := s.builds.ResizeVolume(r.Context(), name, req.Size); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (s *Server) deleteVolume(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	if strings.TrimSpace(name) == "" {
		writeError(w, http.StatusBadRequest, fmt.Errorf("nom requis"))
		return
	}
	if err := s.builds.DeleteVolume(r.Context(), name); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (s *Server) artifact(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if !s.authorizeBuild(w, r, id) {
		return
	}
	w.Header().Set("Content-Type", "application/x-tar")
	w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=%q", id+".tar"))
	if err := s.builds.Artifact(r.Context(), id, w); err != nil {
		log.Printf("artefacts %s: %v", id, err)
		writeError(w, http.StatusBadGateway, err)
	}
}

func (s *Server) static(w http.ResponseWriter, r *http.Request) {
	name := "web/index.html"
	switch r.URL.Path {
	case "/", "/index.html":
		name = "web/index.html"
	case "/app.js":
		name = "web/app.js"
	case "/style.css":
		name = "web/style.css"
	default:
		http.NotFound(w, r)
		return
	}
	s.serveFile(w, r, name)
}

func (s *Server) serveFile(w http.ResponseWriter, r *http.Request, name string) {
	data, err := ui.FS.ReadFile(name)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	switch {
	case strings.HasSuffix(name, ".html"):
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
	case strings.HasSuffix(name, ".js"):
		w.Header().Set("Content-Type", "application/javascript; charset=utf-8")
	case strings.HasSuffix(name, ".css"):
		w.Header().Set("Content-Type", "text/css; charset=utf-8")
	}
	http.ServeContent(w, r, name, time.Time{}, bytes.NewReader(data))
}

type flushWriter struct {
	w io.Writer
	f http.Flusher
}

func (f *flushWriter) Write(p []byte) (int, error) {
	n, err := f.w.Write(p)
	if f.f != nil {
		f.f.Flush()
	}
	return n, err
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, status int, err error) {
	writeJSON(w, status, map[string]string{"error": err.Error()})
}
