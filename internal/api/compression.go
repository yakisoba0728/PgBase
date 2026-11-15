package api

import (
	"compress/gzip"
	"mime"
	"net/http"
	"strconv"
	"strings"
	"sync"
)

// minimumCompressedBytes keeps tiny bodies plain: below about one KB gzip's
// header and CPU cost buy nothing.
const minimumCompressedBytes = 1024

var gzipWriters = sync.Pool{New: func() any {
	writer, _ := gzip.NewWriterLevel(nil, gzip.DefaultCompression)
	return writer
}}

// compressionMiddleware gzips compressible responses for clients that accept
// it. It decides per response once it has seen the headers and either the
// declared length or the first KB of body; anything it does not compress is
// written through byte for byte.
func compressionMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodHead || r.Header.Get("Range") != "" {
			next.ServeHTTP(w, r)
			return
		}
		writer := &compressionWriter{ResponseWriter: w, acceptsGzip: acceptsGzip(r.Header.Values("Accept-Encoding"))}
		defer writer.finish()
		next.ServeHTTP(writer, r)
	})
}

func acceptsGzip(values []string) bool {
	for _, value := range values {
		for _, part := range strings.Split(value, ",") {
			coding, params, _ := strings.Cut(strings.TrimSpace(part), ";")
			if coding = strings.TrimSpace(coding); !strings.EqualFold(coding, "gzip") && coding != "*" {
				continue
			}
			if name, q, found := strings.Cut(strings.TrimSpace(params), "="); found && strings.EqualFold(strings.TrimSpace(name), "q") {
				if weight, err := strconv.ParseFloat(strings.TrimSpace(q), 64); err != nil || weight == 0 {
					return false
				}
			}
			return true
		}
	}
	return false
}

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

const (
	compressionPending = iota
	compressionPlain
	compressionGzip
)

type compressionWriter struct {
	http.ResponseWriter
	acceptsGzip bool
	status      int
	mode        int
	pending     []byte
	gzip        *gzip.Writer
}

// Unwrap lets http.ResponseController reach capabilities this writer does not
// implement itself, such as Hijack and write deadlines.
func (w *compressionWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }

func (w *compressionWriter) WriteHeader(status int) {
	if w.status != 0 {
		return // superfluous, as on a real ResponseWriter
	}
	if status < http.StatusOK {
		w.ResponseWriter.WriteHeader(status)
		return
	}
	w.status = status
	header := w.Header()
	eligible := status != http.StatusNoContent && status != http.StatusNotModified && status != http.StatusPartialContent &&
		header.Get("Content-Encoding") == "" && header.Get("Content-Disposition") == "" &&
		compressibleType(header.Get("Content-Type"))
	if eligible {
		header.Add("Vary", "Accept-Encoding")
	}
	if !eligible || !w.acceptsGzip {
		w.plain()
		return
	}
	if length, err := strconv.Atoi(header.Get("Content-Length")); err == nil && length < minimumCompressedBytes {
		w.plain()
	}
}

func (w *compressionWriter) plain() {
	w.mode = compressionPlain
	w.ResponseWriter.WriteHeader(w.status)
}

func (w *compressionWriter) startGzip() {
	header := w.Header()
	header.Del("Content-Length")
	header.Del("Accept-Ranges")
	header.Set("Content-Encoding", "gzip")
	w.mode = compressionGzip
	w.ResponseWriter.WriteHeader(w.status)
	w.gzip = gzipWriters.Get().(*gzip.Writer)
	w.gzip.Reset(w.ResponseWriter)
}

func (w *compressionWriter) Write(body []byte) (int, error) {
	if w.status == 0 {
		w.WriteHeader(http.StatusOK)
	}
	switch w.mode {
	case compressionPlain:
		return w.ResponseWriter.Write(body)
	case compressionGzip:
		return w.gzip.Write(body)
	}
	if len(w.pending)+len(body) < minimumCompressedBytes {
		w.pending = append(w.pending, body...)
		return len(body), nil
	}
	w.startGzip()
	if err := w.flushPending(); err != nil {
		return 0, err
	}
	return w.gzip.Write(body)
}

func (w *compressionWriter) flushPending() error {
	pending := w.pending
	w.pending = nil
	if len(pending) == 0 {
		return nil
	}
	var err error
	if w.mode == compressionGzip {
		_, err = w.gzip.Write(pending)
	} else {
		_, err = w.ResponseWriter.Write(pending)
	}
	return err
}

// FlushError is implemented here rather than left to Unwrap: otherwise a
// controller flush would reach the connection past a header and body this
// writer is still holding.
func (w *compressionWriter) FlushError() error {
	if w.status == 0 {
		w.WriteHeader(http.StatusOK)
	}
	if w.mode == compressionPending {
		w.plain()
	}
	if err := w.flushPending(); err != nil {
		return err
	}
	if w.mode == compressionGzip {
		if err := w.gzip.Flush(); err != nil {
			return err
		}
	}
	return http.NewResponseController(w.ResponseWriter).Flush()
}

func (w *compressionWriter) Flush() { _ = w.FlushError() }

func (w *compressionWriter) finish() {
	if w.status == 0 && w.mode == compressionPending {
		// The handler wrote nothing at all; let the server send its default.
		return
	}
	if w.mode == compressionPending {
		w.plain()
	}
	_ = w.flushPending()
	if w.gzip != nil {
		_ = w.gzip.Close()
		w.gzip.Reset(nil)
		gzipWriters.Put(w.gzip)
		w.gzip = nil
	}
}
