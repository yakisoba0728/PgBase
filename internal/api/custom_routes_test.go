package api

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/webisnoteasy/PgBase/internal/observability"
)

func TestCustomRoutesRejectInvalidRegistration(t *testing.T) {
	valid := CustomRoute{Method: "GET", Pattern: "/reports/{id}", Access: CustomRoutePublic, Handler: http.HandlerFunc(func(http.ResponseWriter, *http.Request) {})}
	for _, tc := range []struct {
		name string
		edit func(*CustomRoute)
	}{
		{"implicit access", func(v *CustomRoute) { v.Access = "" }},
		{"unknown access", func(v *CustomRoute) { v.Access = "session" }},
		{"missing handler", func(v *CustomRoute) { v.Handler = nil }},
		{"all methods", func(v *CustomRoute) { v.Method = "" }},
		{"unknown method", func(v *CustomRoute) { v.Method = "TRACE" }},
		{"reserved namespace", func(v *CustomRoute) { v.Pattern = "/api/v1/foo" }},
		{"reserved admin", func(v *CustomRoute) { v.Pattern = "/admin" }},
		{"root wildcard", func(v *CustomRoute) { v.Pattern = "/{everything...}" }},
		{"root", func(v *CustomRoute) { v.Pattern = "/" }},
		{"host pattern", func(v *CustomRoute) { v.Pattern = "example.test/reports" }},
		{"traversal", func(v *CustomRoute) { v.Pattern = "/reports/../admin" }},
		{"unclean", func(v *CustomRoute) { v.Pattern = "/reports//id" }},
		{"encoded namespace", func(v *CustomRoute) { v.Pattern = "/%61pi/foo" }},
		{"query", func(v *CustomRoute) { v.Pattern = "/reports?x=1" }},
		{"control", func(v *CustomRoute) { v.Pattern = "/reports/\x00" }},
		{"invalid unicode", func(v *CustomRoute) { v.Pattern = "/reports/\xff" }},
		{"malformed wildcard", func(v *CustomRoute) { v.Pattern = "/reports/{id" }},
		{"duplicate wildcard", func(v *CustomRoute) { v.Pattern = "/reports/{id}/{id}" }},
		{"request cap", func(v *CustomRoute) { v.MaximumRequestBytes = maxJSONBodyBytes + 1 }},
		{"negative request cap", func(v *CustomRoute) { v.MaximumRequestBytes = -1 }},
		{"response cap", func(v *CustomRoute) { v.MaximumResponseBytes = maximumCustomRouteResponseBytes + 1 }},
		{"negative response cap", func(v *CustomRoute) { v.MaximumResponseBytes = -1 }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			v := valid
			tc.edit(&v)
			if err := ValidateCustomRoutes([]CustomRoute{v}); err == nil {
				t.Fatal("invalid registration accepted")
			}
		})
	}
	if err := ValidateCustomRoutes([]CustomRoute{valid, valid}); err == nil {
		t.Fatal("duplicate accepted")
	}
	left, right := valid, valid
	left.Pattern, right.Pattern = "/reports/{id}/details", "/reports/latest/{part}"
	if err := ValidateCustomRoutes([]CustomRoute{left, right}); err == nil {
		t.Fatal("ambiguous Go patterns accepted")
	}
	routes := make([]CustomRoute, MaximumCustomRoutes)
	for i := range routes {
		routes[i] = valid
		routes[i].Pattern = fmt.Sprintf("/reports%d/{id}", i)
	}
	if err := ValidateCustomRoutes(routes); err != nil {
		t.Fatal(err)
	}
	if err := ValidateCustomRoutes(append(routes, valid)); err == nil {
		t.Fatal("unbounded extension registry accepted")
	}
}

func TestCustomRouteTransportAndObservation(t *testing.T) {
	metrics := observability.NewMetrics(nil)
	metricToken := []byte("custom-route-metrics-token-32-bytes-long")
	handler := mustRouter(t, Dependencies{
		Metrics: metrics, MetricsExposition: metrics.Handler(metricToken),
		CustomRoutes: []CustomRoute{
			{Method: "GET", Pattern: "/reports/{id}", Access: CustomRoutePublic, Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if _, ok := CustomPrincipalFromContext(r.Context()); ok {
					t.Error("public route interpreted credentials")
				}
				_, _ = io.WriteString(w, r.PathValue("id"))
			})},
			{Method: "POST", Pattern: "/reports/echo", Access: CustomRoutePublic, MaximumRequestBytes: 4, Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				body, err := io.ReadAll(r.Body)
				if err != nil {
					w.WriteHeader(413)
					return
				}
				_, _ = w.Write(body)
			})},
			{Method: "GET", Pattern: "/overflow", Access: CustomRoutePublic, MaximumResponseBytes: 4, Handler: http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				_, _ = io.WriteString(w, "first")
			})},
			{Method: "GET", Pattern: "/panic", Access: CustomRoutePublic, Handler: http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				_, _ = io.WriteString(w, "must not escape")
				panic("test panic")
			})},
		},
	})
	for _, tc := range []struct {
		method, path, body string
		status             int
		contains           string
	}{
		{"GET", "/reports/record-sentinel-9f38", "", 200, "record-sentinel-9f38"},
		{"POST", "/reports/echo", "1234", 200, "1234"},
		{"POST", "/reports/echo", "12345", 413, ""},
		{"GET", "/overflow", "", 500, "response_too_large"},
		{"GET", "/panic", "", 500, "internal_error"},
		{"GET", "/unregistered", "", 404, "not_found"},
		{"POST", "/reports/other", "", 404, "not_found"},
		{"GET", "/health/live", "", 200, "ok"},
	} {
		r := httptest.NewRequest(tc.method, tc.path, strings.NewReader(tc.body))
		r.Header.Set("Authorization", "Bearer ignored-public-token")
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		if w.Code != tc.status || !strings.Contains(w.Body.String(), tc.contains) {
			t.Fatalf("%s %s: %d %s", tc.method, tc.path, w.Code, w.Body.String())
		}
		if strings.Contains(w.Body.String(), "must not escape") || w.Header().Get("X-Content-Type-Options") != "nosniff" || w.Header().Get("X-Request-ID") == "" {
			t.Fatalf("request middleware missing: headers=%v body=%s", w.Header(), w.Body.String())
		}
	}
	r := httptest.NewRequest("GET", "/metrics", nil)
	r.Header.Set("Authorization", "Bearer "+string(metricToken))
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, r)
	if w.Code != 200 || !strings.Contains(w.Body.String(), `/reports/{id}`) || strings.Contains(w.Body.String(), "record-sentinel-9f38") || strings.Contains(w.Body.String(), "ignored-public-token") {
		t.Fatalf("custom route metrics are not bounded by template: %s", w.Body.String())
	}
}

func TestCustomUserRouteAuthentication(t *testing.T) {
	route := CustomRoute{Method: "GET", Pattern: "/profile", Access: CustomRouteUser, Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "public, max-age=3600")
		principal, ok := CustomPrincipalFromContext(r.Context())
		if !ok || principal.Kind != "user" || principal.ID == "" {
			t.Error("missing admitted identity")
		}
		_ = json.NewEncoder(w).Encode(principal)
	})}
	if _, err := NewRouter(completeDependencies(Dependencies{CustomRoutes: []CustomRoute{route}})); err == nil {
		t.Fatal("unavailable authentication module accepted")
	}
	handler := mustRouter(t, Dependencies{CustomRoutes: []CustomRoute{route}, UserAuth: &stubUserAuth{}, RecordAccess: stubRecordAccess{}})
	for _, credential := range []string{"", "Bearer actual-session"} {
		r := httptest.NewRequest("GET", "/profile", nil)
		if credential != "" {
			r.Header.Set("Authorization", credential)
		}
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		want := 401
		if credential != "" {
			want = 200
		}
		if w.Code != want || w.Header().Get("Cache-Control") != "no-store" || strings.Contains(w.Body.String(), "actual-session") {
			t.Fatalf("auth transport: %d %v %s", w.Code, w.Header(), w.Body.String())
		}
	}
}
