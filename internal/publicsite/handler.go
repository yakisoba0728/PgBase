// Package publicsite serves an optional user-owned static site from disk.
package publicsite

import (
	"errors"
	"mime"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strings"
)

var hashedAsset = regexp.MustCompile(`-[A-Za-z0-9_-]{8,}\.[A-Za-z0-9]+$`)

type handler struct {
	root *os.Root
	spa  bool
}

// New constructs a handler rooted at an existing directory. The resolved root
// is retained so every individual opened path can be checked against it after
// symlink resolution.
func New(root string, spa bool) (http.Handler, error) {
	if !filepath.IsAbs(root) {
		return nil, errors.New("public directory must be absolute")
	}
	resolved, err := filepath.EvalSymlinks(root)
	if err != nil {
		return nil, errors.New("resolve public directory")
	}
	info, err := os.Stat(resolved)
	if err != nil || !info.IsDir() {
		return nil, errors.New("public directory must exist and be a directory")
	}
	rootHandle, err := os.OpenRoot(resolved)
	if err != nil {
		return nil, errors.New("open public directory")
	}
	return handler{root: rootHandle, spa: spa}, nil
}

func (h handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		w.Header().Set("Allow", "GET, HEAD")
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if unsafePath(r.URL.Path, r.URL.RawPath) {
		http.NotFound(w, r)
		return
	}
	name := strings.TrimPrefix(r.URL.Path, "/")
	if name == "" || strings.HasSuffix(r.URL.Path, "/") {
		if h.serveNamed(w, r, path.Join(name, "index.html")) {
			return
		}
		http.NotFound(w, r)
		return
	}
	if h.serveNamed(w, r, name) {
		return
	}
	// A directory has only one public representation: its own index.html.
	// Do not turn an extensionless existing directory into an SPA deep link.
	if info, err := h.root.Stat(name); err == nil && info.IsDir() {
		http.NotFound(w, r)
		return
	}
	if h.spa && path.Ext(name) == "" && h.serveNamed(w, r, "index.html") {
		return
	}
	http.NotFound(w, r)
}

func (h handler) serveNamed(w http.ResponseWriter, r *http.Request, name string) bool {
	file, info, err := h.open(name)
	if err != nil {
		return false
	}
	defer func() { _ = file.Close() }()
	if contentType := mime.TypeByExtension(path.Ext(name)); contentType != "" {
		w.Header().Set("Content-Type", contentType)
	}
	if hashedAsset.MatchString(path.Base(name)) {
		w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
	} else {
		w.Header().Set("Cache-Control", "no-cache")
	}
	http.ServeContent(w, r, name, info.ModTime(), file)
	return true
}

func (h handler) open(name string) (*os.File, os.FileInfo, error) {
	if name == "." || name == "" || strings.HasPrefix(name, "/") || unsafePath("/"+name, "") {
		return nil, nil, os.ErrNotExist
	}
	// os.Root keeps a descriptor for the configured root and resolves each
	// component beneath it. Its Open operation follows only in-root relative
	// symlinks, so replacement between validation and opening cannot escape.
	file, err := h.root.Open(name)
	if err != nil {
		return nil, nil, err
	}
	info, err := file.Stat()
	if err != nil || info.IsDir() || !info.Mode().IsRegular() {
		_ = file.Close()
		return nil, nil, os.ErrNotExist
	}
	return file, info, nil
}

func unsafePath(decoded, raw string) bool {
	if strings.Contains(decoded, "\\") || strings.Contains(decoded, "//") || strings.ContainsRune(decoded, 0) || strings.Contains(strings.ToLower(raw), "%5c") {
		return true
	}
	for _, segment := range strings.Split(strings.TrimPrefix(decoded, "/"), "/") {
		if segment == "." || segment == ".." {
			return true
		}
	}
	return false
}
