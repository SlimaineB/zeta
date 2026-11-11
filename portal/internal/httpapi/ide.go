package httpapi

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strings"
	"sync"
	"time"

	"zeta/portal/internal/builds"
)

const ideAcquireTimeout = 5 * time.Minute

type ideKey struct {
	envID   string
	shareID string
}

type ideProxy struct {
	builds  *builds.Service
	mu      sync.Mutex
	entries map[ideKey]*ideEntry
}

type ideEntry struct {
	target *url.URL
	close  func()
}

func newIDEProxy(buildService *builds.Service) *ideProxy {
	return &ideProxy{builds: buildService, entries: map[ideKey]*ideEntry{}}
}

func (p *ideProxy) acquire(id string) (*url.URL, error) {
	return p.acquireKey(ideKey{envID: id})
}

func (p *ideProxy) acquireShare(id, shareID string) (*url.URL, error) {
	return p.acquireKey(ideKey{envID: id, shareID: shareID})
}

func (p *ideProxy) acquireKey(key ideKey) (*url.URL, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if entry := p.entries[key]; entry != nil {
		return entry.target, nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), ideAcquireTimeout)
	defer cancel()
	if err := p.builds.EnsureEnvironmentIDE(ctx, key.envID); err != nil {
		return nil, err
	}
	forward, err := p.builds.ForwardEnvironmentIDE(ctx, key.envID)
	if err != nil {
		return nil, err
	}
	target := &url.URL{Scheme: "http", Host: forward.Address}
	if err := waitIDEHTTP(ctx, target); err != nil {
		forward.Close()
		return nil, err
	}
	p.entries[key] = &ideEntry{target: target, close: forward.Close}
	return target, nil
}

func waitIDEHTTP(ctx context.Context, target *url.URL) error {
	client := &http.Client{
		Timeout: 2 * time.Second,
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
	for {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, target.String()+"/", nil)
		if err != nil {
			return err
		}
		resp, err := client.Do(req)
		if err == nil {
			_, _ = io.Copy(io.Discard, resp.Body)
			_ = resp.Body.Close()
			if resp.StatusCode < http.StatusInternalServerError {
				return nil
			}
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf("IDE pas encore prêt: %w", ctx.Err())
		case <-time.After(250 * time.Millisecond):
		}
	}
}

func (p *ideProxy) invalidate(key ideKey) {
	p.mu.Lock()
	entry := p.entries[key]
	delete(p.entries, key)
	p.mu.Unlock()
	if entry != nil && entry.close != nil {
		entry.close()
	}
}

func (p *ideProxy) Close(id string) {
	p.mu.Lock()
	entries := make([]*ideEntry, 0)
	for key, entry := range p.entries {
		if key.envID == id {
			entries = append(entries, entry)
			delete(p.entries, key)
		}
	}
	p.mu.Unlock()
	for _, entry := range entries {
		if entry.close != nil {
			entry.close()
		}
	}
}

func (p *ideProxy) CloseShare(id, shareID string) {
	p.invalidate(ideKey{envID: id, shareID: shareID})
}

func (s *Server) enableEnvIDE(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if !s.authorizeEnv(w, r, id) {
		return
	}
	identity := s.requestIdentity(r)
	var err error
	if identity.guest {
		_, err = s.ide.acquireShare(id, identity.shareID)
	} else {
		_, err = s.ide.acquire(id)
	}
	if err != nil {
		writeError(w, http.StatusBadGateway, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{
		"url": "/api/envs/" + url.PathEscape(id) + "/ide/",
	})
}

func (s *Server) redirectEnvIDE(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if !s.authorizeEnv(w, r, id) {
		return
	}
	target := "/api/envs/" + url.PathEscape(id) + "/ide/"
	if r.URL.RawQuery != "" {
		target += "?" + r.URL.RawQuery
	}
	http.Redirect(w, r, target, http.StatusTemporaryRedirect)
}

func (s *Server) envIDE(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Referrer-Policy", "no-referrer")
	id := r.PathValue("id")
	if !s.authorizeEnv(w, r, id) {
		return
	}
	identity := s.requestIdentity(r)
	key := ideKey{envID: id}
	if identity.guest {
		key.shareID = identity.shareID
	}
	target, err := s.ide.acquireKey(key)
	if err != nil {
		writeError(w, http.StatusBadGateway, err)
		return
	}
	prefix := "/api/envs/" + id + "/ide"
	path := strings.TrimPrefix(r.URL.Path, prefix)
	if path == "" {
		path = "/"
	}
	query := r.URL.RawQuery
	s.sync.Touch(id)
	if isWebSocketRequest(r) {
		release := s.sync.BeginIDE(id, identity.key, identity.name, identity.shareID, identity.guest)
		defer release()
	}
	proxy := &httputil.ReverseProxy{
		Rewrite: func(pr *httputil.ProxyRequest) {
			pr.SetURL(target)
			pr.Out.URL.Path = path
			pr.Out.URL.RawPath = ""
			pr.Out.URL.RawQuery = query
			pr.Out.Host = pr.In.Host
			pr.Out.Header.Set("X-Forwarded-Host", pr.In.Host)
			if pr.In.TLS != nil {
				pr.Out.Header.Set("X-Forwarded-Proto", "https")
			} else {
				pr.Out.Header.Set("X-Forwarded-Proto", "http")
			}
		},
		ModifyResponse: func(resp *http.Response) error {
			if location := resp.Header.Get("Location"); location != "" {
				resp.Header.Set("Location", rewriteIDELocation(prefix, location))
			}
			return nil
		},
		ErrorHandler: func(w http.ResponseWriter, r *http.Request, err error) {
			s.ide.invalidate(key)
			http.Error(w, "IDE indisponible", http.StatusBadGateway)
		},
		FlushInterval: -1,
	}
	proxy.ServeHTTP(w, r)
}

func isWebSocketRequest(r *http.Request) bool {
	return strings.EqualFold(r.Header.Get("Upgrade"), "websocket")
}

func rewriteIDELocation(prefix, location string) string {
	if location == "" || strings.HasPrefix(location, "http://") || strings.HasPrefix(location, "https://") {
		return location
	}
	if location == prefix || strings.HasPrefix(location, prefix+"/") {
		return location
	}
	switch {
	case strings.HasPrefix(location, "/"):
		return prefix + location
	case strings.HasPrefix(location, "./"):
		return prefix + "/" + strings.TrimPrefix(location, "./")
	case strings.HasPrefix(location, "../"):
		return location
	default:
		return prefix + "/" + location
	}
}
