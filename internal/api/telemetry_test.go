package api

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/webisnoteasy/PgBase/internal/adminauth"
	"github.com/webisnoteasy/PgBase/internal/observability"
)

var testMetricsToken = []byte("metrics-access-token")

type captureRecorder struct {
	events []observability.Event
}

func (r *captureRecorder) Record(_ context.Context, event observability.Event) {
	r.events = append(r.events, event)
}

func TestAdminAuthOutcomeClassificationIsBounded(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want observability.Outcome
	}{
		{name: "success", want: observability.OutcomeSuccess},
		{name: "validation", err: fmt.Errorf("sensitive@example.test: %w", adminauth.ErrValidation), want: observability.OutcomeValidation},
		{name: "invalid credentials", err: fmt.Errorf("secret-password: %w", adminauth.ErrInvalidCredentials), want: observability.OutcomeInvalidCredentials},
		{name: "setup unavailable", err: fmt.Errorf("setup-token: %w", adminauth.ErrSetupUnavailable), want: observability.OutcomeSetupUnavailable},
		{name: "rate limited", err: fmt.Errorf("192.0.2.44: %w", adminauth.ErrRateLimited), want: observability.OutcomeRateLimited},
		{name: "auth conflict", err: fmt.Errorf("duplicate email: %w", adminauth.ErrConflict), want: observability.OutcomeConflict},
		{name: "conflict", err: &pgconn.PgError{Code: "23505", Message: "duplicate sensitive email"}, want: observability.OutcomeConflict},
		{name: "unavailable", err: &pgconn.PgError{Code: "40001", Message: "database-url-secret"}, want: observability.OutcomeUnavailable},
		{name: "canceled", err: fmt.Errorf("cancel sensitive operation: %w", context.Canceled), want: observability.OutcomeCanceled},
		{name: "failure", err: errors.New("unbounded internal secret"), want: observability.OutcomeFailure},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := adminAuthOutcome(tt.err); got != tt.want {
				t.Fatalf("adminAuthOutcome() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestAdminEndpointsRecordBoundedAuthenticationEvents(t *testing.T) {
	tests := []struct {
		name          string
		endpoint      func(adminEndpoints, http.ResponseWriter, *http.Request)
		request       func() *http.Request
		auth          *fakeAdminAuth
		wantOperation observability.Operation
		wantOutcome   observability.Outcome
		wantStatus    int
	}{
		{
			name: "setup success",
			endpoint: func(endpoint adminEndpoints, response http.ResponseWriter, request *http.Request) {
				endpoint.setup(response, request)
			},
			request: func() *http.Request {
				request := httptest.NewRequest(http.MethodPost, "/api/admin/setup", strings.NewReader(`{"setupToken":"secret-setup-token","email":"sensitive@example.test","password":"secret-password"}`))
				request.Header.Set("Content-Type", "application/json")
				return request
			},
			auth:          &fakeAdminAuth{setupSession: validSession()},
			wantOperation: observability.OperationAdminSetup,
			wantOutcome:   observability.OutcomeSuccess,
			wantStatus:    http.StatusCreated,
		},
		{
			name: "login invalid credentials",
			endpoint: func(endpoint adminEndpoints, response http.ResponseWriter, request *http.Request) {
				endpoint.login(response, request)
			},
			request: func() *http.Request {
				request := httptest.NewRequest(http.MethodPost, "/api/admin/auth/login", strings.NewReader(`{"email":"sensitive@example.test","password":"secret-password"}`))
				request.Header.Set("Content-Type", "application/json")
				return request
			},
			auth:          &fakeAdminAuth{loginErr: fmt.Errorf("sensitive@example.test: %w", adminauth.ErrInvalidCredentials)},
			wantOperation: observability.OperationAdminLogin,
			wantOutcome:   observability.OutcomeInvalidCredentials,
			wantStatus:    http.StatusUnauthorized,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			recorder := &captureRecorder{}
			endpoint := adminEndpoints{auth: tt.auth, recorder: recorder}
			response := httptest.NewRecorder()
			tt.endpoint(endpoint, response, tt.request())
			if response.Code != tt.wantStatus {
				t.Fatalf("status = %d, want %d; body = %s", response.Code, tt.wantStatus, response.Body.String())
			}
			if len(recorder.events) != 1 {
				t.Fatalf("events = %d, want 1", len(recorder.events))
			}
			event := recorder.events[0]
			if event.Component() != observability.ComponentAdminAuth || event.Operation() != tt.wantOperation || event.Outcome() != tt.wantOutcome {
				t.Fatalf("event = component:%v operation:%v outcome:%v", event.Component(), event.Operation(), event.Outcome())
			}
			if event.Duration() < 0 {
				t.Fatalf("duration = %v", event.Duration())
			}
			encoded := fmt.Sprintf("%#v", event)
			for _, secret := range []string{"sensitive@example.test", "secret-password", "secret-setup-token", "192.0.2.1"} {
				if strings.Contains(encoded, secret) {
					t.Fatalf("event leaked %q: %s", secret, encoded)
				}
			}
		})
	}
}

func TestMetricsEndpointIsOptIn(t *testing.T) {
	tests := []struct {
		name         string
		metrics      *observability.Metrics
		metricsToken []byte
	}{
		{name: "no metrics or token"},
		{name: "metrics without token", metrics: observability.NewMetrics(nil)},
		{name: "token without metrics", metricsToken: testMetricsToken},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			handler := mustRouter(t, Dependencies{Metrics: tt.metrics,
				MetricsExposition: metricsExposition(tt.metrics, tt.metricsToken)})
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/metrics", nil))
			if response.Code != http.StatusNotFound {
				t.Fatalf("status = %d, want %d; body = %s", response.Code, http.StatusNotFound, response.Body.String())
			}
			if tt.metrics != nil {
				metricsRequest := httptest.NewRequest(http.MethodGet, "/metrics", nil)
				metricsRequest.Header.Set("Authorization", "Bearer inspection-token")
				metricsResponse := httptest.NewRecorder()
				tt.metrics.Handler([]byte("inspection-token")).ServeHTTP(metricsResponse, metricsRequest)
				if strings.Contains(metricsResponse.Body.String(), "pgbase_http_requests_total") {
					t.Fatalf("disabled metrics combination recorded HTTP traffic: %s", metricsResponse.Body.String())
				}
			}
		})
	}
}

func TestMetricsEndpointRequiresExactlyOneCorrectBearerCredential(t *testing.T) {
	metrics := observability.NewMetrics(nil)
	handler := mustRouter(t, Dependencies{Metrics: metrics,
		MetricsExposition: metricsExposition(metrics, testMetricsToken)})
	tests := []struct {
		name           string
		authorizations []string
		wantStatus     int
	}{
		{name: "missing", wantStatus: http.StatusUnauthorized},
		{name: "wrong", authorizations: []string{"Bearer wrong-token"}, wantStatus: http.StatusUnauthorized},
		{name: "duplicate", authorizations: []string{"Bearer metrics-access-token", "Bearer metrics-access-token"}, wantStatus: http.StatusUnauthorized},
		{name: "correct", authorizations: []string{"Bearer metrics-access-token"}, wantStatus: http.StatusOK},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			request := httptest.NewRequest(http.MethodGet, "/metrics", nil)
			for _, authorization := range tt.authorizations {
				request.Header.Add("Authorization", authorization)
			}
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, request)
			if response.Code != tt.wantStatus {
				t.Fatalf("status = %d, want %d; body = %s", response.Code, tt.wantStatus, response.Body.String())
			}
			if tt.wantStatus == http.StatusOK && !strings.Contains(response.Body.String(), "# HELP pgbase_http_requests_total") {
				t.Fatalf("metrics scrape missing HTTP collector: %s", response.Body.String())
			}
			if strings.Contains(response.Body.String(), string(testMetricsToken)) {
				t.Fatalf("metrics response leaked bearer token: %s", response.Body.String())
			}
		})
	}
}

func TestHTTPMetricsUseRouteTemplatesWithoutRawRequestData(t *testing.T) {
	metrics := observability.NewMetrics(nil)
	handler := mustRouter(t, Dependencies{Metrics: metrics,
		MetricsExposition: metricsExposition(metrics, testMetricsToken)})
	const (
		collectionSentinel = "raw_collection_sentinel"
		idSentinel         = "raw_id_sentinel"
		querySentinel      = "raw_query_sentinel"
	)
	request := httptest.NewRequest(http.MethodGet,
		"/api/v1/collections/"+collectionSentinel+"/records/"+idSentinel+"?credential="+querySentinel, nil)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusUnauthorized {
		t.Fatalf("record status = %d, want %d; body = %s", response.Code, http.StatusUnauthorized, response.Body.String())
	}

	body := scrapeMetrics(t, handler)
	if !strings.Contains(body, `route="/api/v1/collections/{collection}/records/{id}"`) {
		t.Fatalf("metrics missing record route template: %s", body)
	}
	for _, sentinel := range []string{collectionSentinel, idSentinel, querySentinel, "credential="} {
		if strings.Contains(body, sentinel) {
			t.Fatalf("metrics leaked raw request data %q: %s", sentinel, body)
		}
	}
}

func TestHTTPMetricsObserveRecoveredPanicAndBalanceInFlight(t *testing.T) {
	metrics := observability.NewMetrics(nil)
	handler := mustRouter(t, Dependencies{
		Metrics:           metrics,
		MetricsExposition: metricsExposition(metrics, testMetricsToken),
		AdminUI: http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusAccepted)
			_, _ = w.Write([]byte("partial-sensitive-response"))
			panic("panic-sensitive-message")
		}),
	})
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/admin/panic", nil))
	if response.Code != http.StatusInternalServerError {
		t.Fatalf("panic status = %d, want %d; body = %s", response.Code, http.StatusInternalServerError, response.Body.String())
	}
	if strings.Contains(response.Body.String(), "partial-sensitive-response") || strings.Contains(response.Body.String(), "panic-sensitive-message") {
		t.Fatalf("panic response leaked partial content: %s", response.Body.String())
	}

	body := scrapeMetrics(t, handler)
	if !strings.Contains(body, `pgbase_http_requests_total{method="GET",route="/admin/",status="5xx"} 1`) {
		t.Fatalf("metrics did not observe recovered 500: %s", body)
	}
	if !strings.Contains(body, `pgbase_http_requests_in_flight{method="GET",route="/admin/"} 0`) {
		t.Fatalf("panic left in-flight gauge unbalanced: %s", body)
	}
	if strings.Contains(body, "partial-sensitive-response") || strings.Contains(body, "panic-sensitive-message") {
		t.Fatalf("metrics leaked panic content: %s", body)
	}
}

func scrapeMetrics(t *testing.T, handler http.Handler) string {
	t.Helper()
	request := httptest.NewRequest(http.MethodGet, "/metrics", nil)
	request.Header.Set("Authorization", "Bearer "+string(testMetricsToken))
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("metrics status = %d, want %d; body = %s", response.Code, http.StatusOK, response.Body.String())
	}
	return response.Body.String()
}

// metricsExposition mirrors what composition does: build the authenticated
// exposition handler outside the router so the bearer secret never becomes a
// router dependency.
func metricsExposition(metrics *observability.Metrics, token []byte) http.Handler {
	if metrics == nil || len(token) == 0 {
		return nil
	}
	return metrics.Handler(token)
}
