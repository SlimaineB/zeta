package httpapi

import (
	"context"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strings"
	"sync"
	"time"

	"zeta/portal/internal/builds"
)

type ideProxy struct {
	builds  *builds.Service
	mu      sync.Mutex
	entries map[string]*ideEntry
}

type ideEntry struct {
	target *url.URL
	close  func()
}

func newIDEProxy(buildService *builds.Service) *ideProxy {
	return &ideProxy{builds: buildService, entries: map[string]*ideEntry{}}
}

func (p *ideProxy) acquire(id string) (*url.URL, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if entry := p.entries[id]; entry != nil {
		return entry.target, nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()
	if err := p.builds.EnsureEnvironmentIDE(ctx, id); err != nil {
		return nil, err
	}
	forward, err := p.builds.ForwardEnvironmentIDE(ctx, id)
	if err != nil {
		return nil, err
	}
	target := &url.URL{Scheme: "http", Host: forward.Address}
	p.entries[id] = &ideEntry{target: target, close: forward.Close}
	return target, nil
}

func (p *ideProxy) invalidate(id string) {
	p.mu.Lock()
	entry := p.entries[id]
	delete(p.entries, id)
	p.mu.Unlock()
	if entry != nil && entry.close != nil {
		entry.close()
	}
}

func (p *ideProxy) Close(id string) { p.invalidate(id) }

func (s *Server) enableEnvIDE(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if !s.authorizeEnv(w, r, id) {
		return
	}
	if _, err := s.ide.acquire(id); err != nil {
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
	id := r.PathValue("id")
	if !s.authorizeEnv(w, r, id) {
		return
	}
	target, err := s.ide.acquire(id)
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
	s.sync.BeginClient(id)
	defer s.sync.EndClient(id)
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
			s.ide.invalidate(id)
			http.Error(w, "IDE indisponible", http.StatusBadGateway)
		},
		FlushInterval: -1,
	}
	proxy.ServeHTTP(w, r)
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
