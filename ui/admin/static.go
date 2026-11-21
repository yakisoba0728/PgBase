package admin

import (
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/base64"
	"io"
	"io/fs"
	"mime"
	"net/http"
	"path"
	"strconv"
	"strings"
	"sync"
	"time"
)

// minimumCompressedBytes matches the API compression middleware: below about
// one KB gzip's header and CPU cost buy nothing, so such bodies stay plain.
const minimumCompressedBytes = 1024

// staticAsset is one embedded administrator file served from memory-backed
// state that is computed at most once per process: its content ETag on first
// use and, for compressible types, its gzip encoding on the first request that
// accepts it. Files nobody asks for, or asks for only uncompressed, never get
// a gzip copy, so the resident cost stays at what clients actually use.
//
// The gzip representation is served here with its own strong ETag and
// Content-Encoding, which the API compression middleware leaves untouched, so
// a response is never compressed twice and nothing is recompressed per request.
type staticAsset struct {
	// fsys and name locate the file; data, when non-nil, is the body instead.
	fsys         fs.FS
	name         string
	data         []byte
	contentType  string
	cacheControl string
	compressible bool

	metadataOnce sync.Once
	etag         string
	size         int
	metadataErr  error

	gzipOnce sync.Once
	gzip     []byte
	gzipETag string
	gzipErr  error
}

func newStaticAsset(fsys fs.FS, name string, data []byte, contentType, cacheControl string) *staticAsset {
	return &staticAsset{
		fsys: fsys, name: name, data: data,
		contentType: contentType, cacheControl: cacheControl,
		compressible: contentType != "" && compressibleType(contentType),
	}
}

// embeddedAssets indexes every file of the embedded build once, so a request
// is a map lookup rather than a filesystem stat plus a full read.
func embeddedAssets(dist fs.FS) (map[string]*staticAsset, error) {
	assets := make(map[string]*staticAsset)
	err := fs.WalkDir(dist, ".", func(name string, entry fs.DirEntry, err error) error {
		if err != nil || entry.IsDir() {
			return err
		}
		cacheControl := "no-cache"
		if strings.HasPrefix(name, "assets/") && hashedAsset.MatchString(path.Base(name)) {
			cacheControl = "public, max-age=31536000, immutable"
		}
		assets[name] = newStaticAsset(dist, name, nil, mime.TypeByExtension(path.Ext(name)), cacheControl)
		return nil
	})
	return assets, err
}

func (a *staticAsset) contents() ([]byte, error) {
	if a.data != nil {
		return a.data, nil
	}
	return fs.ReadFile(a.fsys, a.name)
}

func (a *staticAsset) metadata() error {
	a.metadataOnce.Do(func() {
		body, err := a.contents()
		if err != nil {
			a.metadataErr = err
			return
		}
		a.etag = contentETag(body)
		a.size = len(body)
	})
	return a.metadataErr
}

// gzipped compresses the file once, at the best ratio: the cost is paid on the
// first gzip request rather than on every one.
func (a *staticAsset) gzipped() ([]byte, string, error) {
	a.gzipOnce.Do(func() {
		body, err := a.contents()
		if err != nil {
			a.gzipErr = err
			return
		}
		var buffer bytes.Buffer
		writer, err := gzip.NewWriterLevel(&buffer, gzip.BestCompression)
		if err == nil {
			_, err = writer.Write(body)
		}
		if err == nil {
			err = writer.Close()
		}
		if err != nil {
			a.gzipErr = err
			return
		}
		a.gzip = bytes.Clone(buffer.Bytes())
		// A distinct strong validator per representation: a cache must never
		// treat the gzip bytes and the identity bytes as interchangeable.
		a.gzipETag = strings.TrimSuffix(a.etag, `"`) + `-gzip"`
	})
	return a.gzip, a.gzipETag, a.gzipErr
}

// open returns the identity body for http.ServeContent. Embedded files are
// seekable, so they are streamed without copying them per request.
func (a *staticAsset) open() (io.ReadSeeker, func(), error) {
	if a.data != nil {
		return bytes.NewReader(a.data), func() {}, nil
	}
	file, err := a.fsys.Open(a.name)
	if err != nil {
		return nil, nil, err
	}
	if seeker, ok := file.(io.ReadSeeker); ok {
		return seeker, func() { _ = file.Close() }, nil
	}
	_ = file.Close()
	body, err := fs.ReadFile(a.fsys, a.name)
	if err != nil {
		return nil, nil, err
	}
	return bytes.NewReader(body), func() {}, nil
}

func (a *staticAsset) serve(w http.ResponseWriter, r *http.Request) {
	if err := a.metadata(); err != nil {
		http.Error(w, "Static asset unavailable", http.StatusInternalServerError)
		return
	}
	header := w.Header()
	if a.contentType != "" {
		header.Set("Content-Type", a.contentType)
	}
	header.Set("Cache-Control", a.cacheControl)
	if a.compressible {
		header.Add("Vary", "Accept-Encoding")
		// A byte range is always taken from the identity representation, the
		// only one whose offsets a client can reason about.
		if a.size >= minimumCompressedBytes && r.Header.Get("Range") == "" && acceptsGzip(r.Header.Values("Accept-Encoding")) {
			a.serveGzip(w, r)
			return
		}
	}
	body, closeBody, err := a.open()
	if err != nil {
		http.Error(w, "Static asset unavailable", http.StatusInternalServerError)
		return
	}
	defer closeBody()
	header.Set("ETag", a.etag)
	http.ServeContent(w, r, a.name, time.Time{}, body)
}

// serveGzip answers with the cached gzip representation. The request carries
// no Range header, the method is GET or HEAD, and the embedded files have no
// modification time, so only the entity-tag preconditions apply.
func (a *staticAsset) serveGzip(w http.ResponseWriter, r *http.Request) {
	body, etag, err := a.gzipped()
	if err != nil {
		http.Error(w, "Static asset unavailable", http.StatusInternalServerError)
		return
	}
	header := w.Header()
	header.Set("ETag", etag)
	if match := r.Header.Get("If-Match"); match != "" && !etagListMatches(match, etag, false) {
		w.WriteHeader(http.StatusPreconditionFailed)
		return
	}
	if noneMatch := r.Header.Get("If-None-Match"); noneMatch != "" && etagListMatches(noneMatch, etag, true) {
		header.Del("Content-Type")
		w.WriteHeader(http.StatusNotModified)
		return
	}
	header.Set("Content-Encoding", "gzip")
	header.Set("Content-Length", strconv.Itoa(len(body)))
	w.WriteHeader(http.StatusOK)
	if r.Method != http.MethodHead {
		_, _ = w.Write(body)
	}
}

func contentETag(body []byte) string {
	sum := sha256.Sum256(body)
	return `"` + base64.RawURLEncoding.EncodeToString(sum[:18]) + `"`
}

// etagListMatches reports whether an If-Match (strong comparison) or
// If-None-Match (weak comparison) field value selects etag, following the
// entity-tag grammar of RFC 9110 section 8.8.3.
func etagListMatches(list, etag string, weak bool) bool {
	for {
		list = strings.TrimLeft(list, " \t,")
		if list == "" {
			return false
		}
		if list[0] == '*' {
			return true
		}
		candidate, rest, ok := scanETag(list)
		if !ok {
			return false
		}
		if candidate == etag || weak && strings.TrimPrefix(candidate, "W/") == strings.TrimPrefix(etag, "W/") {
			return true
		}
		list = rest
	}
}

func scanETag(value string) (etag, rest string, ok bool) {
	start := 0
	if strings.HasPrefix(value, "W/") {
		start = 2
	}
	if len(value)-start < 2 || value[start] != '"' {
		return "", "", false
	}
	for i := start + 1; i < len(value); i++ {
		switch c := value[i]; {
		case c == '"':
			return value[:i+1], value[i+1:], true
		case c == 0x21 || c >= 0x23 && c <= 0x7E || c >= 0x80:
		default:
			return "", "", false
		}
	}
	return "", "", false
}

// acceptsGzip mirrors the API compression middleware's negotiation (RFC 9110
// section 12.5.3): an explicit coding takes precedence over '*' across the
// whole field, and a refusal or malformed weight is never turned into consent
// by list order. The two must agree so a response this handler leaves
// uncompressed is one the middleware would also leave uncompressed.
func acceptsGzip(values []string) bool {
	type preference struct{ listed, allowed bool }
	var explicit, wildcard preference
	for _, value := range values {
		for _, part := range strings.Split(value, ",") {
			coding, params, weighted := strings.Cut(strings.TrimSpace(part), ";")
			var target *preference
			switch coding = strings.TrimSpace(coding); {
			case strings.EqualFold(coding, "gzip"):
				target = &explicit
			case coding == "*":
				target = &wildcard
			default:
				continue
			}
			allowed := !weighted || positiveEncodingQuality(params)
			if target.listed {
				target.allowed = target.allowed && allowed
			} else {
				target.listed, target.allowed = true, allowed
			}
		}
	}
	if explicit.listed {
		return explicit.allowed
	}
	return wildcard.listed && wildcard.allowed
}

// positiveEncodingQuality accepts only the qvalue grammar of RFC 9110 section
// 12.4.2 and reports whether the weight is above zero.
func positiveEncodingQuality(params string) bool {
	name, value, ok := strings.Cut(strings.TrimSpace(params), "=")
	if !ok || !strings.EqualFold(strings.TrimSpace(name), "q") {
		return false
	}
	value = strings.TrimSpace(value)
	if len(value) == 0 || (value[0] != '0' && value[0] != '1') {
		return false
	}
	if len(value) > 1 && (value[1] != '.' || len(value) > 5) {
		return false
	}
	positive := value[0] == '1'
	for i := 2; i < len(value); i++ {
		if value[i] < '0' || value[i] > '9' || (value[0] == '1' && value[i] != '0') {
			return false
		}
		positive = positive || value[i] != '0'
	}
	return positive
}

// compressibleType matches the API compression middleware's set of types.
// Already-compressed formats such as woff2 and png are excluded.
func compressibleType(contentType string) bool {
	media, _, err := mime.ParseMediaType(contentType)
	if err != nil {
		return false
	}
	switch {
	case media == "text/event-stream":
		return false
	case strings.HasPrefix(media, "text/"), media == "application/json", strings.HasSuffix(media, "+json"),
		media == "application/javascript", media == "image/svg+xml":
		return true
	}
	return false
}
