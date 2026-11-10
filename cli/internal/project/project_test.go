package project

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadMissing(t *testing.T) {
	dir := t.TempDir()
	cfg, root, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.Env) != 0 || cfg.Name != "" {
		t.Fatalf("expected empty config, got %+v", cfg)
	}
	if root != dir {
		t.Fatalf("root = %q, want %q", root, dir)
	}
}

func TestLoadConfig(t *testing.T) {
	dir := t.TempDir()
	content := `name: demo
profile: go
image: golang:1.23
context: src
size: 2Gi
exclude:
  - vendor
env:
  FOO: bar
  PORT: 8080
  DEBUG: true
`
	if err := os.WriteFile(filepath.Join(dir, FileName), []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, root, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Name != "demo" || cfg.Profile != "go" || cfg.Image != "golang:1.23" {
		t.Fatalf("unexpected config: %+v", cfg)
	}
	if cfg.Env["FOO"] != "bar" || cfg.Env["PORT"] != "8080" || cfg.Env["DEBUG"] != "true" {
		t.Fatalf("unexpected env: %+v", cfg.Env)
	}
	if root != filepath.Join(dir, "src") {
		t.Fatalf("root = %q", root)
	}
}
