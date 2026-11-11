package sync

import (
	"bytes"
	"encoding/base64"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"zeta/cli/internal/contexttar"
)

type memTransport struct {
	sent []Message
}

func (m *memTransport) Send(msg Message) error {
	m.sent = append(m.sent, msg)
	return nil
}

func (m *memTransport) Receive() (Message, error) { return Message{}, os.ErrClosed }
func (m *memTransport) Close() error              { return nil }

func TestSendInitialIgnoresBuildOutputs(t *testing.T) {
	root := t.TempDir()
	write := func(rel, data string) {
		t.Helper()
		p := filepath.Join(root, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(data), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("src/main.cpp", "main")
	write("build/out.o", "ignored")
	write(".venv/bin/python", "ignored")
	write(".git/config", "ignored")

	tr := &memTransport{}
	s := NewSyncer(root, ScopeWorkspace, contexttar.DefaultExcludes(), tr)
	if err := s.SendInitial(); err != nil {
		t.Fatal(err)
	}
	var paths []string
	for _, msg := range tr.sent {
		if msg.Path != "" {
			paths = append(paths, msg.Path)
		}
	}
	joined := strings.Join(paths, ",")
	if !strings.Contains(joined, "src/main.cpp") {
		t.Fatalf("fichier source absent: %v", paths)
	}
	for _, ignored := range []string{"build", ".venv", ".git"} {
		if strings.Contains(joined, ignored) {
			t.Fatalf("%s ne devrait pas être synchronisé: %v", ignored, paths)
		}
	}
	if tr.sent[len(tr.sent)-1].Type != "sync-done" {
		t.Fatalf("dernier message = %s, attendu sync-done", tr.sent[len(tr.sent)-1].Type)
	}
}

func TestApplyFileAndRejectTraversal(t *testing.T) {
	root := t.TempDir()
	tr := &memTransport{}
	s := NewSyncer(root, ScopeWorkspace, nil, tr)
	msg := Message{Type: "file", Scope: ScopeWorkspace, Path: "src/main.cpp", Mode: 0o644, Data: base64.StdEncoding.EncodeToString([]byte("hello"))}
	if err := s.Apply(msg); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(root, "src", "main.cpp"))
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "hello" {
		t.Fatalf("contenu = %q", data)
	}
	if err := s.Apply(Message{Type: "file", Scope: ScopeWorkspace, Path: "../evil", Data: base64.StdEncoding.EncodeToString([]byte("x"))}); err == nil {
		t.Fatal("traversée de chemin acceptée")
	}
}

func TestApplySymlinkSafety(t *testing.T) {
	root := t.TempDir()
	tr := &memTransport{}
	workspace := NewSyncer(root, ScopeWorkspace, nil, tr)
	if err := workspace.Apply(Message{Type: "symlink", Scope: ScopeWorkspace, Path: "link", Link: "/etc/passwd"}); err == nil {
		t.Fatal("lien absolu accepté dans le workspace")
	}
	artifacts := NewSyncer(root, ScopeArtifacts, nil, tr)
	if err := artifacts.Apply(Message{Type: "symlink", Scope: ScopeArtifacts, Path: "bin/python3", Link: "/usr/local/bin/python3"}); err != nil {
		t.Fatalf("lien absolu d'artefact devrait être ignoré: %v", err)
	}
	if _, err := os.Lstat(filepath.Join(root, "bin", "python3")); !os.IsNotExist(err) {
		t.Fatal("lien absolu d'artefact ne devrait pas être créé")
	}
}

func TestWebsocketOrigin(t *testing.T) {
	cases := map[string]string{
		"ws://zeta.local/api/sync/demo/client":  "http://zeta.local",
		"wss://zeta.example/api/envs/demo/exec": "https://zeta.example",
		"ws://zeta-portal:8080/api/sync/demo":   "http://zeta-portal:8080",
	}
	for raw, want := range cases {
		got, err := websocketOrigin(raw)
		if err != nil {
			t.Fatalf("websocketOrigin(%q): %v", raw, err)
		}
		if got != want {
			t.Fatalf("websocketOrigin(%q) = %q, want %q", raw, got, want)
		}
	}
	if _, err := websocketOrigin("http://"); err == nil {
		t.Fatal("expected invalid URL error")
	}
}

func TestChunkedFileRoundTrip(t *testing.T) {
	source := t.TempDir()
	data := bytes.Repeat([]byte("0123456789abcdef"), 400000)
	if err := os.WriteFile(filepath.Join(source, "big.bin"), data, 0o644); err != nil {
		t.Fatal(err)
	}
	tr := &memTransport{}
	sender := NewSyncer(source, ScopeWorkspace, nil, tr)
	sender.Chunked = true
	if err := sender.SendInitial(); err != nil {
		t.Fatal(err)
	}
	var chunks int
	for _, msg := range tr.sent {
		if msg.Type != "file-chunk" {
			continue
		}
		chunks++
		if msg.Data != "" {
			t.Fatal("un chunk ne doit pas contenir de données base64")
		}
	}
	if chunks < 2 {
		t.Fatalf("chunks = %d, attendu plusieurs chunks", chunks)
	}
	target := t.TempDir()
	receiver := NewSyncer(target, ScopeWorkspace, nil, &memTransport{})
	for _, msg := range tr.sent {
		if msg.Type == "file-chunk" {
			if err := receiver.Apply(msg); err != nil {
				t.Fatal(err)
			}
		}
	}
	got, err := os.ReadFile(filepath.Join(target, "big.bin"))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, data) {
		t.Fatalf("contenu reçu différent: %d octets au lieu de %d", len(got), len(data))
	}
	receiver.mu.Lock()
	hash := receiver.state["big.bin"].Hash
	receiver.mu.Unlock()
	if hash != hashBytes(data) {
		t.Fatalf("hash = %q, attendu %q", hash, hashBytes(data))
	}
}

func TestChunkEncoding(t *testing.T) {
	msg := Message{
		Type:   "file-chunk",
		Scope:  ScopeWorkspace,
		Path:   "a/b.bin",
		Mode:   0o644,
		Offset: 1048576,
		Total:  2097152,
		Hash:   "abc",
		Eof:    true,
		Chunk:  []byte("payload"),
	}
	raw, err := marshalChunk(msg)
	if err != nil {
		t.Fatal(err)
	}
	got, err := unmarshalChunk(raw)
	if err != nil {
		t.Fatal(err)
	}
	if got.Path != msg.Path || got.Offset != msg.Offset || got.Total != msg.Total || got.Hash != msg.Hash || got.Eof != msg.Eof || !bytes.Equal(got.Chunk, msg.Chunk) {
		t.Fatalf("chunk décodé = %+v", got)
	}
}

func TestLegacyLargeFileUsesSingleMessage(t *testing.T) {
	source := t.TempDir()
	data := bytes.Repeat([]byte("legacy"), 50000)
	if err := os.WriteFile(filepath.Join(source, "big.bin"), data, 0o644); err != nil {
		t.Fatal(err)
	}
	tr := &memTransport{}
	sender := NewSyncer(source, ScopeWorkspace, nil, tr)
	if err := sender.SendInitial(); err != nil {
		t.Fatal(err)
	}
	var files int
	for _, msg := range tr.sent {
		if msg.Type == "file-chunk" {
			t.Fatal("un agent legacy ne doit pas recevoir de chunks")
		}
		if msg.Type == "file" && msg.Path == "big.bin" {
			files++
			if msg.Data == "" {
				t.Fatal("fichier legacy sans données base64")
			}
		}
	}
	if files != 1 {
		t.Fatalf("messages file = %d, attendu 1", files)
	}
}
