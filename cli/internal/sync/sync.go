package sync

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"

	"golang.org/x/net/websocket"

	"zeta/cli/internal/contexttar"
)

const (
	ScopeWorkspace = "workspace"
	ScopeArtifacts = "artifacts"
)

type Message struct {
	Type  string `json:"type"`
	Scope string `json:"scope,omitempty"`
	Path  string `json:"path,omitempty"`
	Link  string `json:"link,omitempty"`
	Mode  uint32 `json:"mode,omitempty"`
	Size  int64  `json:"size,omitempty"`
	Hash  string `json:"hash,omitempty"`
	Data  string `json:"data,omitempty"`
	Error string `json:"error,omitempty"`
}

type Transport interface {
	Send(Message) error
	Receive() (Message, error)
	Close() error
}

type WSConn struct {
	ws *websocket.Conn
	mu sync.Mutex
}

func Dial(ctx context.Context, rawURL, cookie string) (*WSConn, error) {
	return DialAuth(ctx, rawURL, cookie, "")
}

func DialAuth(ctx context.Context, rawURL, cookie, token string) (*WSConn, error) {
	origin, err := websocketOrigin(rawURL)
	if err != nil {
		return nil, err
	}
	cfg, err := websocket.NewConfig(rawURL, origin)
	if err != nil {
		return nil, err
	}
	if header := cookieHeader(cookie); header != "" {
		cfg.Header.Set("Cookie", header)
	}
	if token = strings.TrimSpace(token); token != "" {
		cfg.Header.Set("Authorization", "Bearer "+token)
	}
	ws, err := cfg.DialContext(ctx)
	if err != nil {
		return nil, err
	}
	return &WSConn{ws: ws}, nil
}

func websocketOrigin(rawURL string) (string, error) {
	u, err := url.Parse(rawURL)
	if err != nil || u.Host == "" {
		return "", fmt.Errorf("invalid WebSocket URL: %s", rawURL)
	}
	scheme := "http"
	if u.Scheme == "wss" {
		scheme = "https"
	}
	return scheme + "://" + u.Host, nil
}

func (c *WSConn) Send(msg Message) error {
	data, err := json.Marshal(msg)
	if err != nil {
		return err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	return websocket.Message.Send(c.ws, string(data))
}

func (c *WSConn) Receive() (Message, error) {
	var raw string
	if err := websocket.Message.Receive(c.ws, &raw); err != nil {
		return Message{}, err
	}
	var msg Message
	if err := json.Unmarshal([]byte(raw), &msg); err != nil {
		return Message{}, err
	}
	return msg, nil
}

func (c *WSConn) Close() error {
	return c.ws.Close()
}

func cookieHeader(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}
	if strings.HasPrefix(strings.ToLower(raw), "cookie:") {
		return raw
	}
	if strings.Contains(raw, "=") {
		return raw
	}
	return "zeta_session=" + raw
}

type FileInfo struct {
	Path string
	Kind string
	Link string
	Mode uint32
	Size int64
	Hash string
}

type Syncer struct {
	Root      string
	Scope     string
	Ignore    []string
	Transport Transport

	mu    sync.Mutex
	state map[string]FileInfo
}

func NewSyncer(root, scope string, ignore []string, transport Transport) *Syncer {
	return &Syncer{Root: root, Scope: scope, Ignore: ignore, Transport: transport, state: map[string]FileInfo{}}
}

func (s *Syncer) scan() (map[string]FileInfo, error) {
	root, err := filepath.Abs(s.Root)
	if err != nil {
		return nil, err
	}
	info, err := os.Stat(root)
	if err != nil {
		if os.IsNotExist(err) {
			return map[string]FileInfo{}, nil
		}
		return nil, err
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("sync root is not a directory: %s", root)
	}
	files := map[string]FileInfo{}
	err = filepath.WalkDir(root, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(root, p)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)
		if rel == "." {
			return nil
		}
		if contexttar.Match(rel, d.IsDir(), s.Ignore) {
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		fi, err := d.Info()
		if err != nil {
			return err
		}
		mode := fi.Mode()
		switch {
		case mode.IsDir():
			files[rel] = FileInfo{Path: rel, Kind: "dir", Mode: uint32(mode.Perm()), Hash: "dir"}
		case mode&os.ModeSymlink != 0:
			link, err := os.Readlink(p)
			if err != nil {
				return err
			}
			files[rel] = FileInfo{Path: rel, Kind: "symlink", Link: link, Mode: uint32(mode.Perm()), Hash: hashString("link:" + link)}
		case mode.IsRegular():
			data, err := os.ReadFile(p)
			if err != nil {
				return err
			}
			files[rel] = FileInfo{Path: rel, Kind: "file", Mode: uint32(mode.Perm()), Size: int64(len(data)), Hash: hashBytes(data)}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return files, nil
}

func (s *Syncer) SendInitial() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	files, err := s.scan()
	if err != nil {
		return err
	}
	for _, rel := range sortedKeys(files) {
		if err := s.sendFile(files[rel]); err != nil {
			return err
		}
	}
	s.state = files
	return s.Transport.Send(Message{Type: "sync-done", Scope: s.Scope})
}

func (s *Syncer) SetStateFromDisk() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	files, err := s.scan()
	if err != nil {
		return err
	}
	s.state = files
	return nil
}

func (s *Syncer) SendChanges() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	current, err := s.scan()
	if err != nil {
		return err
	}
	for _, rel := range sortedKeys(current) {
		cur := current[rel]
		old, ok := s.state[rel]
		if ok && old.Hash == cur.Hash && old.Mode == cur.Mode {
			continue
		}
		if err := s.sendFile(cur); err != nil {
			return err
		}
		s.state[rel] = cur
	}
	for _, rel := range sortedKeys(s.state) {
		if _, ok := current[rel]; ok {
			continue
		}
		if err := s.Transport.Send(Message{Type: "delete", Scope: s.Scope, Path: rel}); err != nil {
			return err
		}
		delete(s.state, rel)
	}
	return nil
}

func (s *Syncer) Apply(msg Message) error {
	if msg.Scope != s.Scope {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	target, err := safeJoin(s.Root, msg.Path)
	if err != nil {
		return err
	}
	switch msg.Type {
	case "dir":
		if err := os.MkdirAll(target, os.FileMode(msg.Mode)&os.ModePerm); err != nil {
			return err
		}
		s.state[msg.Path] = FileInfo{Path: msg.Path, Kind: "dir", Mode: msg.Mode, Hash: "dir"}
	case "file":
		data, err := base64.StdEncoding.DecodeString(msg.Data)
		if err != nil {
			return fmt.Errorf("contenu invalide pour %s: %w", msg.Path, err)
		}
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return err
		}
		tmp, err := os.CreateTemp(filepath.Dir(target), ".zeta-sync-*")
		if err != nil {
			return err
		}
		tmpName := tmp.Name()
		_, writeErr := tmp.Write(data)
		chmodErr := tmp.Chmod(os.FileMode(msg.Mode) & os.ModePerm)
		closeErr := tmp.Close()
		if writeErr != nil || chmodErr != nil || closeErr != nil {
			_ = os.Remove(tmpName)
			if writeErr != nil {
				return writeErr
			}
			if chmodErr != nil {
				return chmodErr
			}
			return closeErr
		}
		if err := os.Rename(tmpName, target); err != nil {
			_ = os.Remove(tmpName)
			return err
		}
		s.state[msg.Path] = FileInfo{Path: msg.Path, Kind: "file", Mode: msg.Mode, Size: int64(len(data)), Hash: hashBytes(data)}
	case "symlink":
		if err := validateLink(s.Root, target, msg.Link); err != nil {
			if s.Scope == ScopeArtifacts {
				return nil
			}
			return err
		}
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return err
		}
		_ = os.Remove(target)
		if err := os.Symlink(msg.Link, target); err != nil {
			return err
		}
		s.state[msg.Path] = FileInfo{Path: msg.Path, Kind: "symlink", Link: msg.Link, Mode: msg.Mode, Hash: hashString("link:" + msg.Link)}
	case "delete":
		if err := os.RemoveAll(target); err != nil && !os.IsNotExist(err) {
			return err
		}
		delete(s.state, msg.Path)
	}
	return nil
}

func (s *Syncer) sendFile(f FileInfo) error {
	msg := Message{Type: f.Kind, Scope: s.Scope, Path: f.Path, Link: f.Link, Mode: f.Mode, Size: f.Size, Hash: f.Hash}
	if f.Kind == "file" {
		target, err := safeJoin(s.Root, f.Path)
		if err != nil {
			return err
		}
		data, err := os.ReadFile(target)
		if err != nil {
			return err
		}
		msg.Data = base64.StdEncoding.EncodeToString(data)
	}
	return s.Transport.Send(msg)
}

func safeJoin(root, rel string) (string, error) {
	if rel == "" || filepath.IsAbs(rel) {
		return "", fmt.Errorf("chemin invalide: %q", rel)
	}
	clean := filepath.Clean(filepath.FromSlash(rel))
	if clean == "." || clean == ".." || strings.HasPrefix(clean, ".."+string(os.PathSeparator)) {
		return "", fmt.Errorf("chemin invalide: %q", rel)
	}
	rootAbs, err := filepath.Abs(root)
	if err != nil {
		return "", err
	}
	target, err := filepath.Abs(filepath.Join(rootAbs, clean))
	if err != nil {
		return "", err
	}
	if target != rootAbs && !strings.HasPrefix(target, rootAbs+string(os.PathSeparator)) {
		return "", fmt.Errorf("chemin hors racine: %q", rel)
	}
	return target, nil
}

func validateLink(root, target, link string) error {
	if link == "" || filepath.IsAbs(link) {
		return fmt.Errorf("lien invalide: %q", link)
	}
	resolved := filepath.Clean(filepath.Join(filepath.Dir(target), filepath.FromSlash(link)))
	rootAbs, err := filepath.Abs(root)
	if err != nil {
		return err
	}
	if resolved != rootAbs && !strings.HasPrefix(resolved, rootAbs+string(os.PathSeparator)) {
		return fmt.Errorf("lien hors racine: %q", link)
	}
	return nil
}

func hashBytes(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

func hashString(s string) string {
	return hashBytes([]byte(s))
}

func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
