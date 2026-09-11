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

// UIExtension is one operator-installed administrator UI extension. Its files
// are served below /admin/extensions/{Name}; main.js is concatenated into the
// administrator's module entry point. This package deliberately has no API
// that accepts an extension from a browser request: the host program chooses
// the trusted filesystem during application construction.
type UIExtension struct {
	Name string
	FS   fs.FS
}

// Handler returns the embedded administrator SPA with optional, startup-owned extensions.
func Handler(configured ...[]UIExtension) (http.Handler, error) {
	var extensions []UIExtension
	if len(configured) > 0 {
		extensions = configured[0]
	}
	dist, err := fs.Sub(Dist, "dist")
	if err != nil {
		return nil, err
	}
	index, err := fs.ReadFile(dist, "index.html")
	if err != nil {
		return nil, err
	}
	byName := make(map[string]fs.FS, len(extensions))
	names := make([]string, 0, len(extensions))
	for _, extension := range extensions {
		if extension.Name == "" || extension.FS == nil {
			return nil, errors.New("administrator extension has an empty name or filesystem")
		}
		if _, exists := byName[extension.Name]; exists {
			return nil, errors.New("administrator extension name is duplicated")
		}
		byName[extension.Name] = extension.FS
		names = append(names, extension.Name)
	}
	if len(names) > 0 {
		index = appendExtensionLoader(index)
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
		if name == "extensions.js" {
			serveExtensionMain(w, r, names, byName)
			return
		}
		if extension, asset, ok := extensionAsset(name, byName); ok {
			serveExtensionAsset(w, r, extension, asset)
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
		// A sub-filesystem rejects names that io/fs.ValidPath refuses (trailing
		// slash, invalid UTF-8) with fs.ErrInvalid, not fs.ErrNotExist; both mean
		// the request names no embedded asset.
		if statErr != nil && !errors.Is(statErr, fs.ErrNotExist) && !errors.Is(statErr, fs.ErrInvalid) {
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

func appendExtensionLoader(index []byte) []byte {
	const marker = "</body>"
	loader := `<script type="module" src="/admin/extensions.js"></script>` + marker
	if !strings.Contains(string(index), marker) {
		return index
	}
	return []byte(strings.Replace(string(index), marker, loader, 1))
}

func extensionAsset(name string, extensions map[string]fs.FS) (fs.FS, string, bool) {
	const prefix = "extensions/"
	if !strings.HasPrefix(name, prefix) {
		return nil, "", false
	}
	parts := strings.SplitN(strings.TrimPrefix(name, prefix), "/", 2)
	if len(parts) != 2 || parts[0] == "" || !fs.ValidPath(parts[1]) {
		return nil, "", false
	}
	extension, ok := extensions[parts[0]]
	return extension, parts[1], ok
}

func serveExtensionMain(w http.ResponseWriter, r *http.Request, names []string, extensions map[string]fs.FS) {
	var source strings.Builder
	for _, name := range names {
		file, err := extensions[name].Open("main.js")
		if errors.Is(err, fs.ErrNotExist) {
			continue
		}
		if err != nil {
			http.Error(w, "Extension unavailable", http.StatusInternalServerError)
			return
		}
		contents, err := fs.ReadFile(extensions[name], "main.js")
		_ = file.Close()
		if err != nil {
			http.Error(w, "Extension unavailable", http.StatusInternalServerError)
			return
		}
		source.WriteString("await (async function(){\n")
		source.Write(contents)
		source.WriteString("\n})();\n")
	}
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Content-Type", "text/javascript; charset=utf-8")
	http.ServeContent(w, r, "extensions.js", time.Time{}, strings.NewReader(source.String()))
}

func serveExtensionAsset(w http.ResponseWriter, r *http.Request, extension fs.FS, name string) {
	info, err := fs.Stat(extension, name)
	if errors.Is(err, fs.ErrNotExist) || errors.Is(err, fs.ErrInvalid) || err == nil && info.IsDir() {
		http.NotFound(w, r)
		return
	}
	if err != nil {
		http.Error(w, "Extension unavailable", http.StatusInternalServerError)
		return
	}
	contents, err := fs.ReadFile(extension, name)
	if err != nil {
		http.Error(w, "Extension unavailable", http.StatusInternalServerError)
		return
	}
	if contentType := mime.TypeByExtension(path.Ext(name)); contentType != "" {
		w.Header().Set("Content-Type", contentType)
	}
	if hashedAsset.MatchString(path.Base(name)) {
		w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
	} else {
		w.Header().Set("Cache-Control", "no-cache")
	}
	http.ServeContent(w, r, name, info.ModTime(), bytes.NewReader(contents))
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
