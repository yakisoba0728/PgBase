package pgbase

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"

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
	Status  int               `json:"status"`
	Headers map[string]string `json:"headers"`
	JSON    json.RawMessage   `json:"json"`
	Body    *string           `json:"body"`
}

func (runtime *javaScriptRuntime) serveRoute(descriptor jsvm.Route, w http.ResponseWriter, r *http.Request) {
	maximum := descriptor.MaximumRequestBytes
	if maximum == 0 {
		maximum = 1 << 20
	}
	body, err := io.ReadAll(io.LimitReader(r.Body, maximum+1))
	if err != nil || int64(len(body)) > maximum {
		http.Error(w, "extension request body is invalid", http.StatusRequestEntityTooLarge)
		return
	}
	request := javaScriptRouteRequest{
		Method: r.Method, Path: r.URL.Path, PathValues: routePathValues(descriptor.Pattern, r),
		Query: cloneQuery(r.URL.Query()), Headers: safeJavaScriptHeaders(r.Header),
	}
	if principal, ok := PrincipalFromContext(r.Context()); ok {
		request.Principal = &principal
	}
	contentType := strings.ToLower(strings.TrimSpace(strings.Split(r.Header.Get("Content-Type"), ";")[0]))
	if len(body) > 0 && contentType == "application/json" {
		decoder := json.NewDecoder(bytes.NewReader(body))
		decoder.UseNumber()
		var value any
		if decoder.Decode(&value) != nil || ensureRouteJSONEnd(decoder) != nil {
			http.Error(w, "extension request JSON is invalid", http.StatusBadRequest)
			return
		}
		request.JSON = body
	} else if len(body) > 0 {
		request.Body = string(body)
	}
	input, err := json.Marshal(request)
	if err != nil {
		http.Error(w, "extension request is invalid", http.StatusBadRequest)
		return
	}
	host := runtime.bridge.forRequest(r.Context())
	output, err := runtime.engine.Invoke(r.Context(), jsvm.Invocation{
		Handler: descriptor.Handler, Capabilities: descriptor.Capabilities, Input: input, Host: host,
	})
	if err != nil {
		writeJavaScriptRouteError(w, err)
		return
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
	if response.JSON != nil {
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
	}
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
	if response.Status < 200 || response.Status > 599 || (response.JSON != nil && response.Body != nil) {
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
		switch executionError.Code {
		case jsvm.CodeBusy, jsvm.CodeRuntimeClosed:
			status = http.StatusServiceUnavailable
		case jsvm.CodeTimeout:
			status = http.StatusGatewayTimeout
		case jsvm.CodeCapabilityDenied:
			status = http.StatusForbidden
		}
	}
	http.Error(w, fmt.Sprintf("JavaScript extension failed: %s", http.StatusText(status)), status)
}

// extensionBridge is populated only after PgBase's typed domain operations are
// constructed. Route code cannot install or replace it.
