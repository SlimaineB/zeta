package sync

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
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

	chunkMagic     = byte(0x5a)
	fileChunkSize  = 1 << 20
	chunkThreshold = 256 << 10
)

type Message struct {
	Type    string `json:"type"`
	Scope   string `json:"scope,omitempty"`
	Path    string `json:"path,omitempty"`
	Link    string `json:"link,omitempty"`
	Mode    uint32 `json:"mode,omitempty"`
	Size    int64  `json:"size,omitempty"`
	Hash    string `json:"hash,omitempty"`
	Data    string `json:"data,omitempty"`
	Error   string `json:"error,omitempty"`
	Version int    `json:"version,omitempty"`
	Offset  int64  `json:"-"`
	Total   int64  `json:"-"`
	Eof     bool   `json:"-"`
	Chunk   []byte `json:"-"`
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
	var data []byte
	var err error
	if msg.Type == "file-chunk" {
		data, err = marshalChunk(msg)
	} else {
		data, err = json.Marshal(msg)
	}
	if err != nil {
		return err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	return websocket.Message.Send(c.ws, data)
}

func (c *WSConn) Receive() (Message, error) {
	var raw []byte
	if err := websocket.Message.Receive(c.ws, &raw); err != nil {
		return Message{}, err
	}
	if len(raw) > 0 && raw[0] == chunkMagic {
		return unmarshalChunk(raw)
	}
	var msg Message
	if err := json.Unmarshal(raw, &msg); err != nil {
		return Message{}, err
	}
	return msg, nil
}

type chunkHeader struct {
	Scope  string `json:"scope"`
	Path   string `json:"path"`
	Offset int64  `json:"offset"`
	Size   int64  `json:"size"`
	Mode   uint32 `json:"mode"`
	Hash   string `json:"hash"`
	Eof    bool   `json:"eof"`
}

func marshalChunk(msg Message) ([]byte, error) {
	header, err := json.Marshal(chunkHeader{
		Scope:  msg.Scope,
		Path:   msg.Path,
		Offset: msg.Offset,
		Size:   msg.Total,
		Mode:   msg.Mode,
		Hash:   msg.Hash,
		Eof:    msg.Eof,
	})
	if err != nil {
		return nil, err
	}
	if uint64(len(header)) > uint64(^uint32(0)) {
		return nil, fmt.Errorf("entête de chunk trop volumineuse")
	}
	buf := make([]byte, 1+4+len(header)+len(msg.Chunk))
	buf[0] = chunkMagic
	binary.BigEndian.PutUint32(buf[1:5], uint32(len(header)))
	copy(buf[5:], header)
	copy(buf[5+len(header):], msg.Chunk)
	return buf, nil
}

func unmarshalChunk(raw []byte) (Message, error) {
	if len(raw) < 5 {
		return Message{}, fmt.Errorf("chunk invalide")
	}
	n := int(binary.BigEndian.Uint32(raw[1:5]))
	if n > len(raw)-5 {
		return Message{}, fmt.Errorf("entête de chunk invalide")
	}
	var header chunkHeader
	if err := json.Unmarshal(raw[5:5+n], &header); err != nil {
		return Message{}, err
	}
	return Message{
		Type:   "file-chunk",
		Scope:  header.Scope,
		Path:   header.Path,
		Offset: header.Offset,
		Total:  header.Size,
		Mode:   header.Mode,
		Hash:   header.Hash,
		Eof:    header.Eof,
		Chunk:  raw[5+n:],
	}, nil
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

	mu      sync.Mutex
	state   map[string]FileInfo
	chunks  map[string]*chunkState
	Chunked bool
}

type chunkState struct {
	file   *os.File
	tmp    string
	target string
	offset int64
	mode   uint32
	hash   string
}

func NewSyncer(root, scope string, ignore []string, transport Transport) *Syncer {
	return &Syncer{
		Root:      root,
		Scope:     scope,
		Ignore:    ignore,
		Transport: transport,
		state:     map[string]FileInfo{},
		chunks:    map[string]*chunkState{},
	}
}

func (s *Syncer) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for key := range s.chunks {
		s.dropChunk(key)
	}
	return nil
}

func (s *Syncer) dropChunk(key string) {
	st := s.chunks[key]
	if st == nil {
		return
	}
	_ = st.file.Close()
	_ = os.Remove(st.tmp)
	delete(s.chunks, key)
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
	case "file-chunk":
		return s.applyFileChunk(target, msg)
	case "dir":
		if err := os.MkdirAll(target, os.FileMode(msg.Mode)&os.ModePerm); err != nil {
			return err
		}
		s.state[msg.Path] = FileInfo{Path: msg.Path, Kind: "dir", Mode: msg.Mode, Hash: "dir"}
	case "file":
		s.dropChunk(msg.Path)
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
		s.dropChunk(msg.Path)
		if err := os.RemoveAll(target); err != nil && !os.IsNotExist(err) {
			return err
		}
		delete(s.state, msg.Path)
	}
	return nil
}

func (s *Syncer) applyFileChunk(target string, msg Message) error {
	if msg.Offset == 0 {
		s.dropChunk(msg.Path)
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return err
		}
		tmp, err := os.CreateTemp(filepath.Dir(target), ".zeta-sync-*")
		if err != nil {
			return err
		}
		if err := tmp.Chmod(os.FileMode(msg.Mode) & os.ModePerm); err != nil {
			_ = tmp.Close()
			_ = os.Remove(tmp.Name())
			return err
		}
		s.chunks[msg.Path] = &chunkState{
			file:   tmp,
			tmp:    tmp.Name(),
			target: target,
			mode:   msg.Mode,
			hash:   msg.Hash,
		}
	}
	st := s.chunks[msg.Path]
	if st == nil {
		return fmt.Errorf("chunk sans début pour %s", msg.Path)
	}
	if msg.Offset != st.offset {
		s.dropChunk(msg.Path)
		return fmt.Errorf("chunk hors séquence pour %s: offset %d, attendu %d", msg.Path, msg.Offset, st.offset)
	}
	if len(msg.Chunk) > 0 {
		if _, err := st.file.Write(msg.Chunk); err != nil {
			s.dropChunk(msg.Path)
			return err
		}
		st.offset += int64(len(msg.Chunk))
	}
	if !msg.Eof {
		return nil
	}
	if err := st.file.Close(); err != nil {
		s.dropChunk(msg.Path)
		return err
	}
	if err := os.Rename(st.tmp, st.target); err != nil {
		_ = os.Remove(st.tmp)
		delete(s.chunks, msg.Path)
		return err
	}
	delete(s.chunks, msg.Path)
	s.state[msg.Path] = FileInfo{Path: msg.Path, Kind: "file", Mode: st.mode, Size: st.offset, Hash: st.hash}
	return nil
}

func (s *Syncer) sendFile(f FileInfo) error {
	if s.Chunked && f.Kind == "file" && f.Size > chunkThreshold {
		return s.sendFileChunks(f)
	}
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

func (s *Syncer) sendFileChunks(f FileInfo) error {
	target, err := safeJoin(s.Root, f.Path)
	if err != nil {
		return err
	}
	file, err := os.Open(target)
	if err != nil {
		return err
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return err
	}
	size := info.Size()
	if size <= chunkThreshold {
		data, err := io.ReadAll(file)
		if err != nil {
			return err
		}
		return s.Transport.Send(Message{
			Type:  "file",
			Scope: s.Scope,
			Path:  f.Path,
			Mode:  f.Mode,
			Size:  int64(len(data)),
			Hash:  f.Hash,
			Data:  base64.StdEncoding.EncodeToString(data),
		})
	}
	buf := make([]byte, fileChunkSize)
	var offset int64
	for offset < size {
		want := int64(len(buf))
		if remaining := size - offset; remaining < want {
			want = remaining
		}
		n, readErr := io.ReadFull(file, buf[:want])
		if n > 0 {
			chunk := make([]byte, n)
			copy(chunk, buf[:n])
			eof := offset+int64(n) == size
			msg := Message{
				Type:   "file-chunk",
				Scope:  s.Scope,
				Path:   f.Path,
				Mode:   f.Mode,
				Size:   size,
				Hash:   f.Hash,
				Offset: offset,
				Total:  size,
				Eof:    eof,
				Chunk:  chunk,
			}
			if err := s.Transport.Send(msg); err != nil {
				return err
			}
			offset += int64(n)
		}
		if readErr != nil && readErr != io.EOF && readErr != io.ErrUnexpectedEOF {
			return readErr
		}
	}
	if offset == 0 {
		return s.Transport.Send(Message{
			Type:  "file-chunk",
			Scope: s.Scope,
			Path:  f.Path,
			Mode:  f.Mode,
			Size:  size,
			Hash:  f.Hash,
			Total: size,
			Eof:   true,
		})
	}
	return nil
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
