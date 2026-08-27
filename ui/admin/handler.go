package admin

import (
	"bytes"
	"errors"
	"io/fs"
	"mime"
	"net/http"
	"path"
	"regexp"
	"strings"
	"time"
)

const contentSecurityPolicy = "default-src 'none'; script-src 'self'; style-src 'self'; img-src 'self' data:; connect-src 'self'; font-src 'self'; frame-ancestors 'none'; base-uri 'self'; form-action 'self'"

var hashedAsset = regexp.MustCompile(`-[A-Za-z0-9_-]{8,}\.[A-Za-z0-9]+$`)

// Handler returns an HTTP handler for the embedded administrator SPA.
func Handler() (http.Handler, error) {
	dist, err := fs.Sub(Dist, "dist")
	if err != nil {
		return nil, err
	}
	index, err := fs.ReadFile(dist, "index.html")
	if err != nil {
		return nil, err
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		setSecurityHeaders(w.Header())
		if r.URL.Path == "/admin" {
			if r.Method != http.MethodGet && r.Method != http.MethodHead {
				http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
				return
			}
			http.Redirect(w, r, "/admin/", http.StatusPermanentRedirect)
			return
		}
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
			return
		}
		if !strings.HasPrefix(r.URL.Path, "/admin/") || unsafePath(r.URL.Path, r.URL.RawPath) {
			http.NotFound(w, r)
			return
		}

		name := strings.TrimPrefix(r.URL.Path, "/admin/")
		if name == "" {
			serveIndex(w, r, index)
			return
		}
		info, statErr := fs.Stat(dist, name)
		if statErr == nil && !info.IsDir() {
			contents, readErr := fs.ReadFile(dist, name)
			if readErr != nil {
				http.Error(w, "Static asset unavailable", http.StatusInternalServerError)
				return
			}
			if contentType := mime.TypeByExtension(path.Ext(name)); contentType != "" {
				w.Header().Set("Content-Type", contentType)
			}
			if strings.HasPrefix(name, "assets/") && hashedAsset.MatchString(path.Base(name)) {
				w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
			} else {
				w.Header().Set("Cache-Control", "no-cache")
			}
			http.ServeContent(w, r, name, info.ModTime(), bytes.NewReader(contents))
			return
		}
		if statErr != nil && !errors.Is(statErr, fs.ErrNotExist) {
			http.Error(w, "Static asset unavailable", http.StatusInternalServerError)
			return
		}
		if strings.HasPrefix(name, "assets/") || path.Ext(name) != "" {
			http.NotFound(w, r)
			return
		}
		serveIndex(w, r, index)
	}), nil
}

func setSecurityHeaders(header http.Header) {
	header.Set("Content-Security-Policy", contentSecurityPolicy)
	header.Set("X-Content-Type-Options", "nosniff")
	header.Set("Referrer-Policy", "no-referrer")
}

func serveIndex(w http.ResponseWriter, r *http.Request, index []byte) {
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	http.ServeContent(w, r, "index.html", time.Time{}, strings.NewReader(string(index)))
}

func unsafePath(decoded, raw string) bool {
	if strings.Contains(decoded, "\\") || strings.ContainsRune(decoded, 0) || (raw != "" && strings.Contains(strings.ToLower(raw), "%5c")) {
		return true
	}
	for _, segment := range strings.Split(strings.TrimPrefix(decoded, "/"), "/") {
		if segment == "." || segment == ".." {
			return true
		}
	}
	return false
}
