package api

import (
	"bytes"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"slices"
	"strings"
	"testing"
	"time"
)

const (
	wireRequestIDPlaceholder = "<request-id>"
	wireTimePlaceholder      = "<time>"
	wireExpiryPlaceholder    = "<expiry>"
	wireDurationPlaceholder  = "<duration>"
)

type wireGolden struct {
	Exchanges []wireExchange `json:"exchanges"`
}

type wireExchange struct {
	Name      string         `json:"name"`
	Method    string         `json:"method"`
	Route     string         `json:"route"`
	Status    int            `json:"status"`
	Headers   []wireHeader   `json:"headers"`
	Cookies   []wireCookie   `json:"cookies"`
	Body      any            `json:"body,omitempty"`
	AccessLog map[string]any `json:"accessLog"`
}

type wireHeader struct {
	Name   string   `json:"name"`
	Values []string `json:"values"`
}

type wireCookie struct {
	Name     string `json:"name"`
	Value    string `json:"value"`
	Path     string `json:"path"`
	Expires  string `json:"expires,omitempty"`
	MaxAge   int    `json:"maxAge"`
	HTTPOnly bool   `json:"httpOnly"`
	Secure   bool   `json:"secure"`
	SameSite string `json:"sameSite"`
}

func TestHTTPWireGolden(t *testing.T) {
	session := validSession()
	tests := []struct {
		name string
		auth *fakeAdminAuth
		make func() *http.Request
	}{
		{
			name: "login success",
			auth: &fakeAdminAuth{loginSession: session},
			make: func() *http.Request {
				request := httptest.NewRequest(http.MethodPost, "/api/admin/auth/login",
					strings.NewReader(`{"email":"admin@example.com","password":"correct horse battery staple"}`))
				request.Header.Set("Content-Type", "application/json")
				request.Header.Set("Origin", "https://pgbase.example")
				return request
			},
		},
		{
			name: "login internal error",
			auth: &fakeAdminAuth{loginErr: errors.New("postgres://admin:secret@database/private SQLSTATE 42P01")},
			make: func() *http.Request {
				request := httptest.NewRequest(http.MethodPost, "/api/admin/auth/login",
					strings.NewReader(`{"email":"admin@example.com","password":"must-not-leak"}`))
				request.Header.Set("Content-Type", "application/json")
				request.Header.Set("Origin", "https://pgbase.example")
				return request
			},
		},
		{
			name: "logout success",
			auth: &fakeAdminAuth{authenticateAdmin: session.Administrator, authenticateExpiry: session.ExpiresAt},
			make: func() *http.Request {
				request := httptest.NewRequest(http.MethodPost, "/api/admin/auth/logout", nil)
				request.Header.Set("Origin", "https://pgbase.example")
				request.Header.Set("X-PgBase-CSRF", validCSRF())
				request.AddCookie(&http.Cookie{Name: adminCookieName, Value: session.RawToken})
				return request
			},
		},
	}

	golden := wireGolden{Exchanges: make([]wireExchange, 0, len(tests))}
	for _, test := range tests {
		var logs bytes.Buffer
		dependencies := testDependencies(test.auth, mustURL("https://pgbase.example"))
		dependencies.CORSAllowedOrigins = []string{"https://pgbase.example"}
		dependencies.Logger = slog.New(slog.NewJSONHandler(&logs, nil))
		response := httptest.NewRecorder()
		request := test.make()
		NewRouter(dependencies).ServeHTTP(response, request)
		golden.Exchanges = append(golden.Exchanges, captureWireExchange(t, test.name, request.Method, response, logs.Bytes()))
	}

	normalizeWireGolden(&golden)
	assertJSONGolden(t, "testdata/http_wire.golden.json", golden)
}

func TestNormalizeWireGoldenOnlyCanonicalizesApprovedVolatileFields(t *testing.T) {
	input := wireGolden{Exchanges: []wireExchange{{
		Headers: []wireHeader{
			{Name: "Date", Values: []string{"Tue, 01 Sep 2026 01:02:03 GMT", "Tue, 01 Sep 2026 01:02:04 GMT"}},
			{Name: "Set-Cookie", Values: []string{"sid=stable; Path=/; Expires=Tue, 01 Sep 2026 02:03:04 GMT; HttpOnly", "theme=dark; Path=/"}},
			{Name: "X-Request-ID", Values: []string{"req-one", "req-two"}},
			{Name: "X-Stable", Values: []string{"2026-09-01T01:02:03Z"}},
		},
		Cookies: []wireCookie{{Name: "sid", Value: "stable", Expires: "2026-09-01T02:03:04Z"}},
		Body: map[string]any{
			"requestId":       "req-three",
			"createdAt":       "2026-09-01T01:02:03Z",
			"updatedAt":       "2026-09-01T01:03:03Z",
			"expiresAt":       "2026-09-01T02:03:04Z",
			"durationMs":      float64(17),
			"requestIdSuffix": "stable",
			"ordinaryTime":    "2026-09-01T01:02:03Z",
		},
		AccessLog: map[string]any{
			"time":        "2026-09-01T01:02:03Z",
			"request_id":  "req-four",
			"duration_ms": float64(4),
			"route":       "/stable/{id}",
		},
	}}}

	normalizeWireGolden(&input)
	got, err := marshalGoldenJSON(input, "")
	if err != nil {
		t.Fatal(err)
	}
	const want = `{"exchanges":[{"name":"","method":"","route":"","status":0,"headers":[{"name":"Date","values":["<time>","<time>"]},{"name":"Set-Cookie","values":["sid=stable; Path=/; Expires=<expiry>; HttpOnly","theme=dark; Path=/"]},{"name":"X-Request-ID","values":["<request-id>","<request-id>"]},{"name":"X-Stable","values":["2026-09-01T01:02:03Z"]}],"cookies":[{"name":"sid","value":"stable","path":"","expires":"<expiry>","maxAge":0,"httpOnly":false,"secure":false,"sameSite":""}],"body":{"createdAt":"<time>","durationMs":"<duration>","expiresAt":"<expiry>","ordinaryTime":"2026-09-01T01:02:03Z","requestId":"<request-id>","requestIdSuffix":"stable","updatedAt":"<time>"},"accessLog":{"duration_ms":"<duration>","request_id":"<request-id>","route":"/stable/{id}","time":"<time>"}}]}`
	if string(bytes.TrimSpace(got)) != want {
		t.Fatalf("normalized wire fixture mismatch\n got: %s\nwant: %s", got, want)
	}
	if gotCount := len(input.Exchanges[0].Headers[2].Values); gotCount != 2 {
		t.Fatalf("request-id header multiplicity = %d, want 2", gotCount)
	}
}

func captureWireExchange(t *testing.T, name, method string, response *httptest.ResponseRecorder, rawLog []byte) wireExchange {
	t.Helper()
	result := response.Result()
	t.Cleanup(func() { _ = result.Body.Close() })

	headerNames := make([]string, 0, len(result.Header))
	for headerName := range result.Header {
		headerNames = append(headerNames, headerName)
	}
	slices.Sort(headerNames)
	headers := make([]wireHeader, 0, len(headerNames))
	for _, headerName := range headerNames {
		headers = append(headers, wireHeader{Name: headerName, Values: append([]string(nil), result.Header.Values(headerName)...)})
	}

	cookies := make([]wireCookie, 0, len(result.Cookies()))
	for _, cookie := range result.Cookies() {
		expires := ""
		if !cookie.Expires.IsZero() {
			expires = cookie.Expires.UTC().Format(time.RFC3339)
		}
		cookies = append(cookies, wireCookie{
			Name: cookie.Name, Value: cookie.Value, Path: cookie.Path, Expires: expires,
			MaxAge: cookie.MaxAge, HTTPOnly: cookie.HttpOnly, Secure: cookie.Secure,
			SameSite: cookieSameSite(cookie.SameSite),
		})
	}

	var body any
	if response.Body.Len() > 0 {
		decoder := json.NewDecoder(bytes.NewReader(response.Body.Bytes()))
		decoder.UseNumber()
		if err := decoder.Decode(&body); err != nil {
			t.Fatalf("decode %s response body: %v", name, err)
		}
	}
	var accessLog map[string]any
	decoder := json.NewDecoder(bytes.NewReader(rawLog))
	decoder.UseNumber()
	if err := decoder.Decode(&accessLog); err != nil {
		t.Fatalf("decode %s access log %q: %v", name, rawLog, err)
	}
	route, _ := accessLog["route"].(string)
	return wireExchange{
		Name: name, Method: method, Route: route, Status: response.Code,
		Headers: headers, Cookies: cookies, Body: body, AccessLog: accessLog,
	}
}

func normalizeWireGolden(golden *wireGolden) {
	for exchangeIndex := range golden.Exchanges {
		exchange := &golden.Exchanges[exchangeIndex]
		for headerIndex := range exchange.Headers {
			header := &exchange.Headers[headerIndex]
			for valueIndex, value := range header.Values {
				switch {
				case strings.EqualFold(header.Name, "X-Request-ID"):
					header.Values[valueIndex] = wireRequestIDPlaceholder
				case strings.EqualFold(header.Name, "Date"):
					header.Values[valueIndex] = wireTimePlaceholder
				case strings.EqualFold(header.Name, "Set-Cookie"):
					header.Values[valueIndex] = normalizeSetCookieExpiry(value)
				}
			}
		}
		for cookieIndex := range exchange.Cookies {
			if exchange.Cookies[cookieIndex].Expires != "" {
				exchange.Cookies[cookieIndex].Expires = wireExpiryPlaceholder
			}
		}
		exchange.Body = normalizeWireValue(exchange.Body)
		exchange.AccessLog = normalizeWireValue(exchange.AccessLog).(map[string]any)
	}
}

func normalizeWireValue(value any) any {
	switch typed := value.(type) {
	case map[string]any:
		for key, nested := range typed {
			switch key {
			case "requestId", "request_id":
				typed[key] = wireRequestIDPlaceholder
			case "time", "createdAt", "created_at", "updatedAt", "updated_at":
				typed[key] = wireTimePlaceholder
			case "expiresAt", "expires_at":
				typed[key] = wireExpiryPlaceholder
			case "duration", "durationMs", "duration_ms", "durationSeconds", "duration_seconds":
				typed[key] = wireDurationPlaceholder
			default:
				typed[key] = normalizeWireValue(nested)
			}
		}
		return typed
	case []any:
		for index := range typed {
			typed[index] = normalizeWireValue(typed[index])
		}
		return typed
	default:
		return value
	}
}

func normalizeSetCookieExpiry(value string) string {
	parts := strings.Split(value, ";")
	for index, part := range parts {
		trimmed := strings.TrimSpace(part)
		if name, _, found := strings.Cut(trimmed, "="); found && strings.EqualFold(name, "Expires") {
			leading := part[:len(part)-len(strings.TrimLeft(part, " \t"))]
			parts[index] = leading + "Expires=" + wireExpiryPlaceholder
		}
	}
	return strings.Join(parts, ";")
}

func cookieSameSite(value http.SameSite) string {
	switch value {
	case http.SameSiteDefaultMode:
		return "Default"
	case http.SameSiteLaxMode:
		return "Lax"
	case http.SameSiteStrictMode:
		return "Strict"
	case http.SameSiteNoneMode:
		return "None"
	default:
		return "Unknown"
	}
}

func assertJSONGolden(t *testing.T, path string, value any) {
	t.Helper()
	got, err := marshalGoldenJSON(value, "  ")
	if err != nil {
		t.Fatal(err)
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, want) {
		t.Fatalf("golden mismatch for %s\n--- got ---\n%s--- want ---\n%s", path, got, want)
	}
}

func marshalGoldenJSON(value any, indent string) ([]byte, error) {
	var encoded bytes.Buffer
	encoder := json.NewEncoder(&encoded)
	encoder.SetEscapeHTML(false)
	if indent != "" {
		encoder.SetIndent("", indent)
	}
	if err := encoder.Encode(value); err != nil {
		return nil, err
	}
	return encoded.Bytes(), nil
}
