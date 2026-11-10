package main

import (
	"archive/tar"
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

func makeTar(t *testing.T, name, content string, mode int64) *bytes.Reader {
	t.Helper()
	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)
	if err := tw.WriteHeader(&tar.Header{Name: name, Mode: mode, Size: int64(len(content)), Typeflag: tar.TypeReg}); err != nil {
		t.Fatal(err)
	}
	if _, err := tw.Write([]byte(content)); err != nil {
		t.Fatal(err)
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	return bytes.NewReader(buf.Bytes())
}

func TestExtractTar(t *testing.T) {
	dest := t.TempDir()
	if err := extractTar(makeTar(t, "bin/hello", "hello", 0o755), dest, nil); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(dest, "bin", "hello"))
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "hello" {
		t.Fatalf("content = %q", data)
	}
}

func TestExtractTarRejectsTraversal(t *testing.T) {
	dest := t.TempDir()
	if err := extractTar(makeTar(t, "../evil", "bad", 0o644), dest, nil); err == nil {
		t.Fatal("expected traversal error")
	}
	if _, err := os.Stat(filepath.Join(filepath.Dir(dest), "evil")); !os.IsNotExist(err) {
		t.Fatalf("traversal file created: %v", err)
	}
}

func TestExtractTarRejectsAbsolute(t *testing.T) {
	dest := t.TempDir()
	if err := extractTar(makeTar(t, "/tmp/evil", "bad", 0o644), dest, nil); err == nil {
		t.Fatal("expected absolute path error")
	}
}

func TestDetectLanguageGo(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module example.com/test\n\ngo 1.26\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := detectLanguage(dir); got != "go" {
		t.Fatalf("detectLanguage() = %q", got)
	}
}

func TestExtractTarExcludesArtifacts(t *testing.T) {
	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)
	files := map[string]string{
		"bin/hello":    "hello",
		"debug.log":    "debug",
		"logs/run.log": "run",
	}
	for name, content := range files {
		if err := tw.WriteHeader(&tar.Header{Name: name, Mode: 0o644, Size: int64(len(content)), Typeflag: tar.TypeReg}); err != nil {
			t.Fatal(err)
		}
		if _, err := tw.Write([]byte(content)); err != nil {
			t.Fatal(err)
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	dest := t.TempDir()
	if err := extractTar(bytes.NewReader(buf.Bytes()), dest, []string{"*.log", "logs"}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dest, "bin", "hello")); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"debug.log", "logs/run.log"} {
		if _, err := os.Stat(filepath.Join(dest, name)); !os.IsNotExist(err) {
			t.Fatalf("%s should be excluded: %v", name, err)
		}
	}
}
