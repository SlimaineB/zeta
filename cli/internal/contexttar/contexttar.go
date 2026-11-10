package contexttar

import (
	"archive/tar"
	"compress/gzip"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"strings"
)

func DefaultExcludes() []string {
	return []string{
		".git",
		".venv",
		"build",
		"dist",
		"__pycache__",
		"node_modules",
		".pytest_cache",
		".mypy_cache",
		".ruff_cache",
		".tox",
		".cache",
		".zeta",
		".zeta-ready",
	}
}

// EnvExcludes is the mirror scope for persistent environments: dependencies and
// caches stay remote, but build outputs (build/, dist/) are synced back so the
// user gets them on the local filesystem.
func EnvExcludes() []string {
	return []string{
		".git",
		".venv",
		"__pycache__",
		"node_modules",
		".pytest_cache",
		".mypy_cache",
		".ruff_cache",
		".tox",
		".cache",
		".zeta",
		".zeta-ready",
	}
}

func WriteFile(dest, root string, excludes []string) (int64, error) {
	f, err := os.Create(dest)
	if err != nil {
		return 0, err
	}
	if err := Write(f, root, excludes); err != nil {
		_ = f.Close()
		_ = os.Remove(dest)
		return 0, err
	}
	if err := f.Close(); err != nil {
		_ = os.Remove(dest)
		return 0, err
	}
	info, err := os.Stat(dest)
	if err != nil {
		return 0, err
	}
	return info.Size(), nil
}

func Write(w io.Writer, root string, excludes []string) error {
	root, err := filepath.Abs(root)
	if err != nil {
		return err
	}
	info, err := os.Stat(root)
	if err != nil {
		return err
	}
	if !info.IsDir() {
		return fmt.Errorf("the context is not a directory: %s", root)
	}

	gw := gzip.NewWriter(w)
	tw := tar.NewWriter(gw)
	walkErr := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
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
		if shouldExclude(rel, d.IsDir(), excludes) {
			if d.IsDir() {
				return fs.SkipDir
			}
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		mode := info.Mode()
		if !mode.IsRegular() && !mode.IsDir() && mode&os.ModeSymlink == 0 {
			return nil
		}
		link := ""
		if mode&os.ModeSymlink != 0 {
			link, err = os.Readlink(p)
			if err != nil {
				return err
			}
		}
		hdr, err := tar.FileInfoHeader(info, link)
		if err != nil {
			return err
		}
		hdr.Name = rel
		if mode.IsDir() {
			hdr.Name += "/"
		}
		hdr.Uid = 0
		hdr.Gid = 0
		hdr.Uname = ""
		hdr.Gname = ""
		hdr.Mode = int64(mode.Perm())
		hdr.Format = tar.FormatPAX
		if err := tw.WriteHeader(hdr); err != nil {
			return err
		}
		if !mode.IsRegular() {
			return nil
		}
		f, err := os.Open(p)
		if err != nil {
			return err
		}
		_, copyErr := io.Copy(tw, f)
		closeErr := f.Close()
		if copyErr != nil {
			return copyErr
		}
		return closeErr
	})
	if walkErr != nil {
		_ = tw.Close()
		_ = gw.Close()
		return walkErr
	}
	if err := tw.Close(); err != nil {
		_ = gw.Close()
		return err
	}
	return gw.Close()
}

func Match(rel string, isDir bool, patterns []string) bool {
	return shouldExclude(rel, isDir, patterns)
}

func shouldExclude(rel string, isDir bool, patterns []string) bool {
	rel = path.Clean(filepath.ToSlash(rel))
	for _, raw := range patterns {
		pattern := strings.TrimSpace(filepath.ToSlash(raw))
		if pattern == "" || strings.HasPrefix(pattern, "#") || strings.HasPrefix(pattern, "!") {
			continue
		}
		pattern = strings.TrimPrefix(pattern, "./")
		pattern = strings.TrimSuffix(pattern, "/")
		if pattern == "" || pattern == "." {
			continue
		}
		if rel == pattern || strings.HasPrefix(rel, pattern+"/") {
			return true
		}
		if ok, _ := path.Match(pattern, rel); ok {
			return true
		}
		if !strings.Contains(pattern, "/") {
			if ok, _ := path.Match(pattern, path.Base(rel)); ok {
				return true
			}
		}
		if isDir {
			if ok, _ := path.Match(pattern, path.Base(rel)); ok {
				return true
			}
		}
	}
	return false
}
