package api

import (
	"bytes"
	"compress/gzip"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func compressionResponse(t *testing.T, method string, headers map[string]string, handler http.HandlerFunc) *httptest.ResponseRecorder {
	t.Helper()
	request := httptest.NewRequest(method, "/x", nil)
	for name, value := range headers {
		request.Header.Set(name, value)
	}
	response := httptest.NewRecorder()
	compressionMiddleware(handler).ServeHTTP(response, request)
	return response
}

func writeBody(contentType string, body string, extra ...string) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", contentType)
		for index := 0; index+1 < len(extra); index += 2 {
			w.Header().Set(extra[index], extra[index+1])
		}
		_, _ = io.WriteString(w, body)
	}
}

func TestCompressionMatrix(t *testing.T) {
	large := strings.Repeat(`{"title":"compressible"}`, 200)
	gzipAccepted := map[string]string{"Accept-Encoding": "br, gzip"}
	for _, test := range []struct {
		name       string
		method     string
		headers    map[string]string
		handler    http.HandlerFunc
		compressed bool
		vary       bool
	}{
		{"large json", http.MethodGet, gzipAccepted, writeBody("application/json", large), true, true},
		{"large js", http.MethodGet, gzipAccepted, writeBody("text/javascript; charset=utf-8", large), true, true},
		{"large svg", http.MethodGet, gzipAccepted, writeBody("image/svg+xml", large), true, true},
		{"no accept-encoding", http.MethodGet, nil, writeBody("application/json", large), false, true},
		{"gzip refused by q=0", http.MethodGet, map[string]string{"Accept-Encoding": "gzip;q=0"}, writeBody("application/json", large), false, true},
		{"small body", http.MethodGet, gzipAccepted, writeBody("application/json", `{"ok":true}`), false, true},
		{"binary type", http.MethodGet, gzipAccepted, writeBody("image/png", large), false, false},
		{"already encoded", http.MethodGet, gzipAccepted, writeBody("application/json", large, "Content-Encoding", "br"), false, false},
		{"download", http.MethodGet, gzipAccepted, writeBody("application/json", large, "Content-Disposition", "attachment"), false, false},
		{"head", http.MethodHead, gzipAccepted, writeBody("application/json", large), false, false},
		{"range request", http.MethodGet, map[string]string{"Accept-Encoding": "gzip", "Range": "bytes=0-9"}, writeBody("application/json", large), false, false},
		{"partial content", http.MethodGet, gzipAccepted, func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusPartialContent)
			_, _ = io.WriteString(w, large)
		}, false, false},
		{"not modified", http.MethodGet, gzipAccepted, func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusNotModified)
		}, false, false},
		{"declared small length", http.MethodGet, gzipAccepted, func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "text/plain")
			w.Header().Set("Content-Length", "2")
			_, _ = io.WriteString(w, "ok")
		}, false, true},
	} {
		t.Run(test.name, func(t *testing.T) {
			response := compressionResponse(t, test.method, test.headers, test.handler)
			encoded := response.Header().Get("Content-Encoding") == "gzip"
			if encoded != test.compressed {
				t.Fatalf("Content-Encoding = %q, want gzip=%v", response.Header().Get("Content-Encoding"), test.compressed)
			}
			if vary := strings.Contains(strings.Join(response.Header().Values("Vary"), ","), "Accept-Encoding"); vary != test.vary {
				t.Fatalf("Vary = %v, want Accept-Encoding=%v", response.Header().Values("Vary"), test.vary)
			}
			body := response.Body.Bytes()
			if encoded {
				if response.Header().Get("Content-Length") != "" {
					t.Fatal("compressed response kept Content-Length")
				}
				reader, err := gzip.NewReader(bytes.NewReader(body))
				if err != nil {
					t.Fatal(err)
				}
				if body, err = io.ReadAll(reader); err != nil {
					t.Fatal(err)
				}
			}
			request := httptest.NewRequest(test.method, "/x", nil)
			plain := httptest.NewRecorder()
			test.handler(plain, request)
			if response.Code != plain.Code || !bytes.Equal(body, plain.Body.Bytes()) {
				t.Fatalf("status/body = %d/%d bytes, want %d/%d bytes", response.Code, len(body), plain.Code, plain.Body.Len())
			}
		})
	}
}

// flushRecorder counts flushes that reach the connection.
type flushRecorder struct {
	*httptest.ResponseRecorder
	flushes int
	atFlush []string
}

func (r *flushRecorder) Flush() {
	r.flushes++
	r.atFlush = append(r.atFlush, r.Body.String())
}

func TestCompressionPassesEventStreamThroughWithFlush(t *testing.T) {
	request := httptest.NewRequest(http.MethodGet, "/api/realtime", nil)
	request.Header.Set("Accept-Encoding", "gzip")
	recorder := &flushRecorder{ResponseRecorder: httptest.NewRecorder()}
	compressionMiddleware(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, "event: hello\n\n")
		if err := http.NewResponseController(w).Flush(); err != nil {
			t.Errorf("Flush() = %v", err)
		}
		w.(http.Flusher).Flush()
	})).ServeHTTP(recorder, request)
	if recorder.Header().Get("Content-Encoding") != "" || recorder.Header().Get("Vary") != "" {
		t.Fatalf("event stream headers = %v", recorder.Header())
	}
	if recorder.flushes != 2 || recorder.atFlush[0] != "event: hello\n\n" {
		t.Fatalf("flushes = %d, bodies at flush = %q", recorder.flushes, recorder.atFlush)
	}
}

func TestCompressionFlushDeliversCompressedPrefix(t *testing.T) {
	request := httptest.NewRequest(http.MethodGet, "/x", nil)
	request.Header.Set("Accept-Encoding", "gzip")
	recorder := &flushRecorder{ResponseRecorder: httptest.NewRecorder()}
	large := strings.Repeat("a", 4096)
	compressionMiddleware(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		_, _ = io.WriteString(w, large)
		http.NewResponseController(w).Flush()
		_, _ = io.WriteString(w, "tail")
	})).ServeHTTP(recorder, request)
	if recorder.flushes != 1 || recorder.atFlush[0] == "" {
		t.Fatalf("flushes = %d, flushed bytes = %d", recorder.flushes, len(recorder.atFlush))
	}
	reader, err := gzip.NewReader(recorder.Body)
	if err != nil {
		t.Fatal(err)
	}
	body, err := io.ReadAll(reader)
	if err != nil || string(body) != large+"tail" {
		t.Fatalf("decoded body = %d bytes, err = %v", len(body), err)
	}
}

func TestCompressionUnwrapReachesUnderlyingWriter(t *testing.T) {
	recorder := httptest.NewRecorder()
	writer := &compressionWriter{ResponseWriter: recorder}
	if writer.Unwrap() != recorder {
		t.Fatal("Unwrap did not return the wrapped writer")
	}
}

func TestAcceptsGzip(t *testing.T) {
	for value, want := range map[string]bool{
		"": false, "gzip": true, "GZIP": true, "br, gzip;q=0.5": true, "gzip;q=0": false,
		"*": true, "identity": false, "deflate, br": false,
	} {
		if got := acceptsGzip([]string{value}); got != want {
			t.Errorf("acceptsGzip(%q) = %v, want %v", value, got, want)
		}
	}
}

func TestRouterCompressesAdminAssetsAndKeepsSmallErrorsPlain(t *testing.T) {
	script := strings.Repeat("console.log('pgbase');\n", 200)
	handler := mustRouter(t, Dependencies{AdminUI: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/javascript; charset=utf-8")
		w.Header().Set("ETag", `"asset"`)
		http.ServeContent(w, r, "app.js", time.Time{}, strings.NewReader(script))
	})})

	request := httptest.NewRequest(http.MethodGet, "/admin/assets/app-abcdefgh.js", nil)
	request.Header.Set("Accept-Encoding", "gzip")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK || response.Header().Get("Content-Encoding") != "gzip" ||
		response.Header().Get("ETag") != `"asset"` || response.Header().Get("Accept-Ranges") != "" {
		t.Fatalf("asset status=%d headers=%v", response.Code, response.Header())
	}
	reader, err := gzip.NewReader(response.Body)
	if err != nil {
		t.Fatal(err)
	}
	if body, err := io.ReadAll(reader); err != nil || string(body) != script {
		t.Fatalf("decoded asset = %d bytes, err = %v", len(body), err)
	}

	request = httptest.NewRequest(http.MethodGet, "/api/missing", nil)
	request.Header.Set("Accept-Encoding", "gzip")
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusNotFound || response.Header().Get("Content-Encoding") != "" ||
		!strings.Contains(response.Body.String(), "not_found") {
		t.Fatalf("small error status=%d headers=%v body=%q", response.Code, response.Header(), response.Body.String())
	}
}
