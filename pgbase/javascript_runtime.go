package pgbase

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"

	"github.com/yakisoba0728/PgBase/internal/api"
	"github.com/yakisoba0728/PgBase/internal/jsvm"
)

type javaScriptRuntime struct {
	bundle       *jsvm.Bundle
	engine       *jsvm.Runtime
	options      JavaScriptOptions
	bridge       *extensionBridge
	hookRemovers []func()
}

func prepareJavaScript(options JavaScriptOptions) (*javaScriptRuntime, error) {
	bundle, err := jsvm.LoadBundle(options.Directory)
	if err != nil {
		return nil, err
	}
	if err := validateJavaScriptRegistration(bundle.Manifest(), options.Capabilities); err != nil {
		return nil, err
	}
	engine, err := jsvm.NewRuntimeWithInitializer(bundle, options.limits(), options.OnInit)
	if err != nil {
		return nil, err
	}
	return &javaScriptRuntime{bundle: bundle, engine: engine, options: options, bridge: &extensionBridge{}}, nil
}

func (runtime *javaScriptRuntime) Close(ctx context.Context) error {
	if runtime == nil || runtime.engine == nil {
		return nil
	}
	runtime.removeHooks()
	return runtime.engine.Close(ctx)
}

func (runtime *javaScriptRuntime) routes() []CustomRoute {
	if runtime == nil {
		return nil
	}
	manifest := runtime.bundle.Manifest()
	routes := make([]CustomRoute, 0, len(manifest.Routes))
	for _, descriptor := range manifest.Routes {
		descriptor := descriptor
		routes = append(routes, CustomRoute{
			Method: descriptor.Method, Pattern: descriptor.Pattern, Access: CustomRouteAccess(descriptor.Access),
			MaximumRequestBytes: descriptor.MaximumRequestBytes, MaximumResponseBytes: descriptor.MaximumResponseBytes,
			StreamResponse: descriptor.StreamResponse,
			Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				runtime.serveRoute(descriptor, w, r)
			}),
		})
	}
	return routes
}

func installJavaScriptRoutes(options *Options, runtime *javaScriptRuntime) error {
	if runtime == nil {
		return nil
	}
	combined := append(append([]CustomRoute(nil), options.CustomRoutes...), runtime.routes()...)
	if err := api.ValidateCustomRoutes(combined); err != nil {
		return fmt.Errorf("JavaScript routes: %w", err)
	}
	options.CustomRoutes = combined
	return nil
}

type javaScriptRouteRequest struct {
	Method     string              `json:"method"`
	Path       string              `json:"path"`
	PathValues map[string]string   `json:"pathValues"`
	Query      map[string][]string `json:"query"`
	Headers    map[string]string   `json:"headers"`
	Principal  *RequestPrincipal   `json:"principal,omitempty"`
	JSON       json.RawMessage     `json:"json,omitempty"`
	Body       string              `json:"body,omitempty"`
}

type javaScriptRouteResponse struct {
	Status    int                       `json:"status"`
	Headers   map[string]string         `json:"headers"`
	JSON      json.RawMessage           `json:"json"`
	Body      *string                   `json:"body"`
	Transport *javaScriptRouteTransport `json:"transport,omitempty"`
}

// javaScriptRouteTransport is declarative. A script never receives a
// ResponseWriter or filesystem path; router admission completed before this
// adapter opens a rooted file or writes a compressed response.
type javaScriptRouteTransport struct {
	Static *javaScriptStaticResponse `json:"static,omitempty"`
	Gzip   bool                      `json:"gzip,omitempty"`
}

type javaScriptStaticResponse struct {
	Root          string `json:"root"`
	Path          string `json:"path"`
	IndexFallback bool   `json:"indexFallback"`
}

type javaScriptRouteBody struct {
	request     *http.Request
	response    http.ResponseWriter
	maximum     int64
	contentType string
	mu          sync.Mutex
	loaded      bool
	body        []byte
	err         error
}

type javaScriptRequestTooLargeError struct{}

func (javaScriptRequestTooLargeError) Error() string       { return "extension request body is too large" }
func (javaScriptRequestTooLargeError) InputTooLarge() bool { return true }

var errJavaScriptRequestTooLarge = javaScriptRequestTooLargeError{}

func (body *javaScriptRouteBody) OpenMultipartField(ctx context.Context, field string) (string, string, int64, io.ReadCloser, error) {
	if body == nil || body.request == nil || field == "" {
		return "", "", 0, nil, errors.New("multipart field is required")
	}
	if _, err := body.read(ctx, 0); err != nil {
		return "", "", 0, nil, err
	}
	file, header, err := body.request.FormFile(field)
	if err != nil {
		return "", "", 0, nil, err
	}
	return header.Filename, header.Header.Get("Content-Type"), header.Size, &contextReadCloser{ctx: ctx, ReadCloser: file}, nil
}

func (body *javaScriptRouteBody) PrepareInput(ctx context.Context, input json.RawMessage, requestedLimit int64) (json.RawMessage, error) {
	raw, err := body.read(ctx, requestedLimit)
	if err != nil {
		return nil, err
	}
	var request javaScriptRouteRequest
	if err := json.Unmarshal(input, &request); err != nil {
		return nil, err
	}
	if len(raw) > 0 && body.contentType == "application/json" {
		decoder := json.NewDecoder(bytes.NewReader(raw))
		decoder.UseNumber()
		var value any
		if decoder.Decode(&value) != nil || ensureRouteJSONEnd(decoder) != nil {
			return nil, errors.New("extension request JSON is invalid")
		}
		request.JSON = raw
	} else if len(raw) > 0 {
		request.Body = string(raw)
	}
	return json.Marshal(request)
}

func (body *javaScriptRouteBody) read(_ context.Context, requestedLimit int64) ([]byte, error) {
	body.mu.Lock()
	defer body.mu.Unlock()
	if body.loaded {
		return body.body, body.err
	}
	maximum := body.maximum
	if requestedLimit > 0 && requestedLimit < maximum {
		maximum = requestedLimit
	}
	if body.request.ContentLength > maximum {
		body.loaded = true
		body.err = errJavaScriptRequestTooLarge
		return nil, body.err
	}
	if strings.HasPrefix(body.contentType, "multipart/form-data") {
		body.request.Body = http.MaxBytesReader(body.response, body.request.Body, maximum)
		body.err = body.request.ParseMultipartForm(maximum)
	} else {
		body.body, body.err = io.ReadAll(io.LimitReader(body.request.Body, maximum+1))
		if body.err != nil || int64(len(body.body)) > maximum {
			body.err = errJavaScriptRequestTooLarge
		}
	}
	body.loaded = true
	return body.body, body.err
}

type contextReadCloser struct {
	ctx context.Context
	io.ReadCloser
}

func (reader *contextReadCloser) Read(buffer []byte) (int, error) {
	if err := reader.ctx.Err(); err != nil {
		return 0, err
	}
	return reader.ReadCloser.Read(buffer)
}

func (runtime *javaScriptRuntime) serveRoute(descriptor jsvm.Route, w http.ResponseWriter, r *http.Request) {
	maximum := descriptor.MaximumRequestBytes
	if maximum == 0 {
		maximum = 1 << 20
	}
	contentType := strings.ToLower(strings.TrimSpace(strings.Split(r.Header.Get("Content-Type"), ";")[0]))
	body := &javaScriptRouteBody{request: r, response: w, maximum: maximum, contentType: contentType}
	defer func() {
		if r.MultipartForm != nil {
			_ = r.MultipartForm.RemoveAll()
		}
	}()
	request := javaScriptRouteRequest{
		Method: r.Method, Path: r.URL.Path, PathValues: routePathValues(descriptor.Pattern, r),
		Query: cloneQuery(r.URL.Query()), Headers: safeJavaScriptHeaders(r.Header),
	}
	if principal, ok := PrincipalFromContext(r.Context()); ok {
		request.Principal = &principal
	}
	input, err := json.Marshal(request)
	if err != nil {
		http.Error(w, "extension request is invalid", http.StatusBadRequest)
		return
	}
	routeHost := runtime.bridge.forRouteRequest(r.Context(), body)
	routeHost.origin = r.RemoteAddr
	if address, _, splitErr := net.SplitHostPort(r.RemoteAddr); splitErr == nil {
		routeHost.origin = address
	}
	host := namespacedHost(routeHost, descriptor.Handler.Module)
	output, err := runtime.engine.Invoke(r.Context(), jsvm.Invocation{
		Handler: descriptor.Handler, Capabilities: descriptor.Capabilities, Input: input, Host: host,
		OnSkipSuccessActivityLog: func() { api.SkipSuccessActivityLog(r.Context()) },
	})
	if err != nil {
		writeJavaScriptRouteError(w, err)
		return
	}
	if invoked, ok := host.(*extensionHost); ok {
		if served := invoked.takeServedObject(); served != nil {
			if !descriptor.StreamResponse || !hasJavaScriptCapability(descriptor.Capabilities, "filesystem") {
				http.Error(w, "extension response is not permitted", http.StatusForbidden)
				return
			}
			if served.path != "" {
				serveJavaScriptLocalPath(w, r, served.path, served.name)
			} else {
				serveJavaScriptExtensionObject(w, r, runtime.bridge.objects, invoked.namespace, *served)
			}
			return
		}
	}
	response, err := decodeJavaScriptRouteResponse(output)
	if err != nil {
		http.Error(w, "extension returned an invalid response", http.StatusInternalServerError)
		return
	}
	for name, value := range response.Headers {
		if !allowedJavaScriptResponseHeader(name) || strings.ContainsAny(value, "\r\n") {
			http.Error(w, "extension returned an invalid response", http.StatusInternalServerError)
			return
		}
		w.Header().Set(name, value)
	}
	if response.Transport != nil && response.Transport.Static != nil {
		if !descriptor.StreamResponse || !hasJavaScriptCapability(descriptor.Capabilities, "filesystem") {
			http.Error(w, "extension response is not permitted", http.StatusForbidden)
			return
		}
		serveJavaScriptStatic(w, r, runtime.options.PocketBaseCompatibility.FilesystemRoot, response.Transport.Static)
		return
	}
	if !descriptor.StreamResponse && responseBodyLength(response) > javaScriptRouteResponseLimit(descriptor) {
		http.Error(w, "extension response is too large", http.StatusInternalServerError)
		return
	}
	if response.JSON != nil {
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
	}
	if response.Transport != nil && response.Transport.Gzip {
		serveJavaScriptGzip(w, r, response)
		return
	}
	writeJavaScriptRouteBody(w, response)
}

func serveJavaScriptLocalPath(w http.ResponseWriter, r *http.Request, path, name string) {
	file, err := os.Open(path)
	if err != nil {
		http.Error(w, "extension file is unavailable", http.StatusNotFound)
		return
	}
	defer func() { _ = file.Close() }()
	info, err := file.Stat()
	if err != nil || info.IsDir() {
		http.Error(w, "extension file is unavailable", http.StatusNotFound)
		return
	}
	w.Header().Set("Content-Type", mime.TypeByExtension(filepath.Ext(name)))
	http.ServeContent(w, r, name, info.ModTime(), file)
}

func serveJavaScriptExtensionObject(w http.ResponseWriter, r *http.Request, objects *ExtensionObjects, namespace string, served extensionServedObject) {
	if objects == nil || namespace == "" {
		http.Error(w, "extension file is unavailable", http.StatusNotFound)
		return
	}
	info, err := objects.Attributes(r.Context(), namespace, served.key)
	if err != nil {
		http.Error(w, "extension file is unavailable", http.StatusNotFound)
		return
	}
	reader, err := objects.Open(r.Context(), namespace, served.key)
	if err != nil {
		http.Error(w, "extension file is unavailable", http.StatusNotFound)
		return
	}
	defer func() { _ = reader.Close() }()
	data, err := io.ReadAll(reader)
	if err != nil {
		http.Error(w, "extension file is unavailable", http.StatusInternalServerError)
		return
	}
	contentType := strings.TrimSpace(strings.Split(http.DetectContentType(data), ";")[0])
	if typed := mime.TypeByExtension(filepath.Ext(served.name)); typed != "" {
		contentType = strings.TrimSpace(strings.Split(typed, ";")[0])
	}
	disposition := "attachment"
	if r.URL.Query().Get("download") != "" {
		disposition = "attachment"
	} else if strings.HasPrefix(contentType, "image/") || strings.HasPrefix(contentType, "audio/") || strings.HasPrefix(contentType, "video/") || contentType == "application/pdf" || strings.HasPrefix(contentType, "text/") {
		disposition = "inline"
	}
	w.Header().Set("Content-Disposition", disposition+"; filename="+strconv.Quote(served.name))
	w.Header().Set("Content-Type", contentType)
	w.Header().Set("Content-Security-Policy", "default-src 'none'; media-src 'self'; style-src 'unsafe-inline'; sandbox")
	w.Header().Set("Cache-Control", "max-age=2592000, stale-while-revalidate=86400")
	http.ServeContent(w, r, served.name, info.UpdatedAt, bytes.NewReader(data))
}
func hasJavaScriptCapability(capabilities []string, wanted string) bool {
	for _, capability := range capabilities {
		if capability == wanted {
			return true
		}
	}
	return false
}

func javaScriptRouteResponseLimit(descriptor jsvm.Route) int {
	if descriptor.MaximumResponseBytes != 0 {
		return descriptor.MaximumResponseBytes
	}
	return 256 << 10
}

func responseBodyLength(response javaScriptRouteResponse) int {
	if response.JSON != nil {
		return len(response.JSON)
	}
	if response.Body != nil {
		return len(*response.Body)
	}
	return 0
}

func serveJavaScriptGzip(w http.ResponseWriter, r *http.Request, response javaScriptRouteResponse) {
	// Do not layer compression onto an already encoded response, and never
	// write a body for HEAD.
	if !acceptsGzip(r.Header.Get("Accept-Encoding")) || w.Header().Get("Content-Encoding") != "" {
		writeJavaScriptRouteBody(w, response)
		return
	}
	w.Header().Set("Content-Encoding", "gzip")
	w.Header().Add("Vary", "Accept-Encoding")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.WriteHeader(response.Status)
	if r.Method == http.MethodHead {
		return
	}
	gz := gzip.NewWriter(w)
	defer func() { _ = gz.Close() }()
	if response.JSON != nil {
		_, _ = gz.Write(response.JSON)
		return
	}
	if response.Body != nil {
		_, _ = io.WriteString(gz, *response.Body)
	}
}

func writeJavaScriptRouteBody(w http.ResponseWriter, response javaScriptRouteResponse) {
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.WriteHeader(response.Status)
	if response.JSON != nil {
		_, _ = w.Write(response.JSON)
		return
	}
	if response.Body != nil {
		_, _ = io.WriteString(w, *response.Body)
	}
}

func acceptsGzip(header string) bool {
	for _, item := range strings.Split(header, ",") {
		parts := strings.Split(strings.TrimSpace(item), ";")
		if !strings.EqualFold(strings.TrimSpace(parts[0]), "gzip") && strings.TrimSpace(parts[0]) != "*" {
			continue
		}
		for _, parameter := range parts[1:] {
			if strings.EqualFold(strings.TrimSpace(parameter), "q=0") || strings.EqualFold(strings.TrimSpace(parameter), "q=0.0") {
				return false
			}
		}
		return true
	}
	return false
}

func serveJavaScriptStatic(w http.ResponseWriter, r *http.Request, configuredRoot string, response *javaScriptStaticResponse) {
	if response == nil || configuredRoot == "" {
		http.NotFound(w, r)
		return
	}
	root, err := javaScriptStaticRoot(configuredRoot, response.Root)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	name, err := javaScriptStaticPath(root, response.Path)
	if err != nil && response.IndexFallback {
		name, err = javaScriptStaticPath(root, "index.html")
	}
	if err != nil {
		http.NotFound(w, r)
		return
	}
	file, err := os.Open(name)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	defer func() { _ = file.Close() }()
	info, err := file.Stat()
	if err != nil || info.IsDir() {
		http.NotFound(w, r)
		return
	}
	if contentType := mime.TypeByExtension(filepath.Ext(name)); contentType != "" {
		w.Header().Set("Content-Type", contentType)
	}
	w.Header().Set("X-Content-Type-Options", "nosniff")
	http.ServeContent(w, r, info.Name(), info.ModTime(), file)
}

func javaScriptStaticRoot(configuredRoot, requestedRoot string) (string, error) {
	if requestedRoot == "" {
		requestedRoot = "."
	}
	if filepath.IsAbs(requestedRoot) {
		return "", errors.New("absolute static roots are not permitted")
	}
	root, err := filepath.EvalSymlinks(configuredRoot)
	if err != nil {
		return "", err
	}
	candidate, err := filepath.EvalSymlinks(filepath.Join(root, filepath.FromSlash(requestedRoot)))
	if err != nil {
		return "", err
	}
	if relative, err := filepath.Rel(root, candidate); err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return "", errors.New("static root escapes configured root")
	}
	return candidate, nil
}

func javaScriptStaticPath(root, requestedPath string) (string, error) {
	clean := filepath.FromSlash(strings.TrimPrefix(filepath.Clean("/"+requestedPath), "/"))
	candidate := filepath.Join(root, clean)
	resolved, err := filepath.EvalSymlinks(candidate)
	if err != nil {
		return "", err
	}
	if relative, err := filepath.Rel(root, resolved); err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return "", errors.New("static path escapes root")
	}
	return resolved, nil
}

func routePathValues(pattern string, request *http.Request) map[string]string {
	result := map[string]string{}
	for _, segment := range strings.Split(pattern, "/") {
		if len(segment) < 3 || segment[0] != '{' || segment[len(segment)-1] != '}' {
			continue
		}
		name := strings.TrimSuffix(segment[1:len(segment)-1], "...")
		if name != "$" {
			result[name] = request.PathValue(name)
		}
	}
	return result
}

func cloneQuery(source map[string][]string) map[string][]string {
	result := make(map[string][]string, len(source))
	for name, values := range source {
		result[name] = append([]string(nil), values...)
	}
	return result
}

func safeJavaScriptHeaders(source http.Header) map[string]string {
	result := map[string]string{}
	for _, name := range []string{"Accept", "Content-Type", "Idempotency-Key", "If-Match", "X-Request-ID"} {
		if value := source.Get(name); value != "" {
			result[name] = value
		}
	}
	return result
}

func decodeJavaScriptRouteResponse(raw []byte) (javaScriptRouteResponse, error) {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	var response javaScriptRouteResponse
	if err := decoder.Decode(&response); err != nil || ensureRouteJSONEnd(decoder) != nil {
		return javaScriptRouteResponse{}, errors.New("invalid response JSON")
	}
	if response.Status < 200 || response.Status > 599 || (response.JSON != nil && response.Body != nil) ||
		(response.Transport != nil && response.Transport.Static != nil && response.Transport.Gzip) {
		return javaScriptRouteResponse{}, errors.New("invalid response status or body")
	}
	if response.JSON != nil && !json.Valid(response.JSON) {
		return javaScriptRouteResponse{}, errors.New("invalid response body")
	}
	return response, nil
}

func ensureRouteJSONEnd(decoder *json.Decoder) error {
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		return errors.New("trailing JSON")
	}
	return nil
}

func allowedJavaScriptResponseHeader(name string) bool {
	switch http.CanonicalHeaderKey(name) {
	case "Cache-Control", "Content-Disposition", "Content-Language", "Content-Type", "Etag", "Location", "Retry-After", "X-Extension":
		return true
	default:
		return false
	}
}

func writeJavaScriptRouteError(w http.ResponseWriter, err error) {
	var executionError *jsvm.Error
	status := http.StatusInternalServerError
	if errors.As(err, &executionError) {
		if executionError.PublicError != nil {
			public := executionError.PublicError
			body, marshalErr := json.Marshal(struct {
				Status  int             `json:"status"`
				Message string          `json:"message"`
				Data    json.RawMessage `json:"data"`
			}{Status: public.Status, Message: public.Message, Data: public.Data})
			if marshalErr != nil {
				http.Error(w, "JavaScript extension failed: Internal Server Error", http.StatusInternalServerError)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(public.Status)
			_, _ = w.Write(body)
			return
		}
		switch executionError.Code {
		case jsvm.CodeBusy, jsvm.CodeRuntimeClosed:
			status = http.StatusServiceUnavailable
		case jsvm.CodeTimeout:
			status = http.StatusGatewayTimeout
		case jsvm.CodeLimitExceeded:
			status = http.StatusRequestEntityTooLarge
		case jsvm.CodeCapabilityDenied:
			status = http.StatusForbidden
		}
	}
	http.Error(w, fmt.Sprintf("JavaScript extension failed: %s", http.StatusText(status)), status)
}

// extensionBridge is populated only after PgBase's typed domain operations are
// constructed. Route code cannot install or replace it.
