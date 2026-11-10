package gitutil

import (
	"fmt"
	"os/exec"
	"path/filepath"
	"strings"
)

func RepoRoot() (string, error) {
	out, err := run("rev-parse", "--show-toplevel")
	if err != nil {
		return "", fmt.Errorf("not in a Git repository: %w", err)
	}
	return out, nil
}

func RemoteURL() (string, error) {
	out, err := run("remote", "get-url", "origin")
	if err != nil {
		return "", fmt.Errorf("remote origin not found: %w", err)
	}
	return NormalizeURL(out)
}

func NormalizeURL(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", fmt.Errorf("empty Git URL")
	}
	if strings.HasPrefix(raw, "git@") {
		if idx := strings.Index(raw, ":"); idx > 0 {
			return "ssh://" + raw[:idx] + "/" + strings.TrimPrefix(raw[idx+1:], "/"), nil
		}
	}
	for _, prefix := range []string{"git://", "http://", "https://", "ssh://"} {
		if strings.HasPrefix(raw, prefix) {
			return raw, nil
		}
	}
	return "", fmt.Errorf("unsupported Git URL: %s (use git://, http(s):// or ssh://)", raw)
}

func DefaultCommit() string {
	if branch, err := run("rev-parse", "--abbrev-ref", "HEAD"); err == nil && branch != "HEAD" {
		return branch
	}
	if sha, err := run("rev-parse", "HEAD"); err == nil {
		return sha
	}
	return ""
}

func DirtyIgnoring(paths ...string) bool {
	args := []string{"status", "--porcelain", "--", "."}
	for _, p := range paths {
		if p == "" || filepath.IsAbs(p) {
			continue
		}
		args = append(args, ":(exclude)"+filepath.ToSlash(p))
	}
	out, err := run(args...)
	return err == nil && out != ""
}

func run(args ...string) (string, error) {
	cmd := exec.Command("git", args...)
	out, err := cmd.Output()
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}
