package kube

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"crypto/rand"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"
)

const (
	maxContextUncompressed = int64(2 << 30)
	contextCleanupAge      = 24 * time.Hour
)

func (r *Repository) StoreContext(ctx context.Context, src io.Reader) (string, error) {
	if err := r.EnsurePVCs(ctx); err != nil {
		return "", err
	}
	if err := os.MkdirAll(r.contextDir, 0o755); err != nil {
		return "", fmt.Errorf("preparing context storage: %w", err)
	}
	buf := make([]byte, 8)
	if _, err := rand.Read(buf); err != nil {
		return "", fmt.Errorf("generating context ID: %w", err)
	}
	name := fmt.Sprintf("ctx-%x.tar.gz", buf)
	contextPath := filepath.Join(r.contextDir, name)
	f, err := os.OpenFile(contextPath, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
	if err != nil {
		return "", fmt.Errorf("creating context: %w", err)
	}
	tee := io.TeeReader(src, f)
	if err := validateContextTar(tee); err != nil {
		_ = f.Close()
		_ = os.Remove(contextPath)
		return "", err
	}
	if _, err := io.Copy(io.Discard, tee); err != nil {
		_ = f.Close()
		_ = os.Remove(contextPath)
		return "", fmt.Errorf("reading context: %w", err)
	}
	if err := f.Close(); err != nil {
		_ = os.Remove(contextPath)
		return "", fmt.Errorf("writing context: %w", err)
	}
	return contextPath, nil
}

func (r *Repository) DeleteContext(ctx context.Context, contextPath string) error {
	_ = ctx
	if strings.TrimSpace(contextPath) == "" {
		return nil
	}
	clean := filepath.Clean(contextPath)
	root := filepath.Clean(r.contextDir) + string(os.PathSeparator)
	if !strings.HasPrefix(clean, root) {
		return fmt.Errorf("invalid context path: %s", contextPath)
	}
	if err := os.Remove(clean); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

func (r *Repository) cleanupContexts(maxAge time.Duration) {
	entries, err := os.ReadDir(r.contextDir)
	if err != nil {
		return
	}
	cutoff := time.Now().Add(-maxAge)
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".tar.gz") {
			continue
		}
		info, err := entry.Info()
		if err != nil || !info.ModTime().Before(cutoff) {
			continue
		}
		_ = os.Remove(filepath.Join(r.contextDir, entry.Name()))
	}
}

func validateContextTar(r io.Reader) error {
	gz, err := gzip.NewReader(r)
	if err != nil {
		return fmt.Errorf("invalid gzip archive: %w", err)
	}
	defer gz.Close()
	tr := tar.NewReader(gz)
	var entries int
	var total int64
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return fmt.Errorf("invalid tar archive: %w", err)
		}
		entries++
		if err := validateContextHeader(hdr); err != nil {
			return err
		}
		if hdr.Size < 0 || total+hdr.Size > maxContextUncompressed {
			return fmt.Errorf("context too large")
		}
		total += hdr.Size
		if _, err := io.Copy(io.Discard, tr); err != nil {
			return fmt.Errorf("invalid tar archive: %w", err)
		}
	}
	if entries == 0 {
		return fmt.Errorf("empty context")
	}
	return nil
}

func validateContextHeader(hdr *tar.Header) error {
	name := path.Clean(strings.TrimPrefix(hdr.Name, "./"))
	if name == "." || name == "" {
		if hdr.Typeflag == tar.TypeDir {
			return nil
		}
		return fmt.Errorf("invalid context entry: %q", hdr.Name)
	}
	if strings.ContainsRune(name, 0) || path.IsAbs(name) || name == ".." || strings.HasPrefix(name, "../") {
		return fmt.Errorf("invalid context path: %s", hdr.Name)
	}
	switch hdr.Typeflag {
	case tar.TypeReg, tar.TypeRegA, tar.TypeDir:
		return nil
	case tar.TypeSymlink:
		if hdr.Linkname == "" || path.IsAbs(hdr.Linkname) {
			return fmt.Errorf("invalid symlink: %s", hdr.Name)
		}
		resolved := path.Clean(path.Join(path.Dir(name), hdr.Linkname))
		if resolved == ".." || strings.HasPrefix(resolved, "../") {
			return fmt.Errorf("symlink outside context: %s", hdr.Name)
		}
		return nil
	case tar.TypeLink:
		target := path.Clean(strings.TrimPrefix(hdr.Linkname, "./"))
		if target == "." || path.IsAbs(target) || target == ".." || strings.HasPrefix(target, "../") {
			return fmt.Errorf("invalid hard link: %s", hdr.Name)
		}
		return nil
	default:
		return fmt.Errorf("unsupported file type: %s", hdr.Name)
	}
}
