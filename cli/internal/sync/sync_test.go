package sync

import (
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
