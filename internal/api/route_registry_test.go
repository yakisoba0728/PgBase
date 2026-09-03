package api

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/webisnoteasy/PgBase/internal/observability"
	"github.com/webisnoteasy/PgBase/internal/schema"
)

// registeredPatterns lists the registry in a stable order.
func registeredPatterns(routes []route) []string {
	patterns := make([]string, 0, len(routes))
	for _, declared := range routes {
		patterns = append(patterns, declared.muxPattern())
	}
	sort.Strings(patterns)
	return patterns
}

func declaredTestRoutes(t *testing.T) []route {
	t.Helper()
	return declareRoutes(completeDependencies(Dependencies{
		Metrics:           nil,
		MetricsExposition: nil,
		AdminUI:           http.NewServeMux(),
	}), false)
}

// The registry is the authoritative description of the HTTP surface, so the
// public route set is pinned here rather than inferred from the shape of the
// registration calls. Adding, removing or renaming a public route has to be a
// deliberate edit to this list.
func TestRouteRegistryDeclaresExactlyThePublicSurface(t *testing.T) {
	want := []string{
		"/admin",
		"/admin/",
		"DELETE /api/admin/session",
		"DELETE /api/v1/collections/{collection}/records/{id}",
		"GET /api/admin/collections",
		"GET /api/admin/schema-operations",
		"GET /api/admin/schema-operations/{id}",
		"GET /api/admin/session",
		"GET /api/admin/status",
		"GET /api/admin/system",
		"GET /api/admin/system/health",
		"GET /api/v1/collections/{collection}/records",
		"GET /api/v1/collections/{collection}/records/{id}",
		"GET /health/live",
		"GET /health/ready",
		"PATCH /api/v1/collections/{collection}/records/{id}",
		"POST /api/admin/auth/login",
		"POST /api/admin/auth/logout",
		"POST /api/admin/collections/apply",
		"POST /api/admin/collections/plan",
		"POST /api/admin/login",
		"POST /api/admin/setup",
		"POST /api/v1/collections/{collection}/records",
	}
	got := registeredPatterns(declaredTestRoutes(t))
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("route registry = %#v,\nwant %#v", got, want)
	}
}

func TestRouteRegistryIsCompleteUniqueAndWithinTheMetricBudget(t *testing.T) {
	routes := declaredTestRoutes(t)
	if err := validateRoutes(routes); err != nil {
		t.Fatalf("declared registry is invalid: %v", err)
	}
	methodAgnostic := 0
	for _, declared := range routes {
		if declared.method == "" {
			methodAgnostic++
		}
	}
	if len(routes) > maximumRegisteredRoutes || methodAgnostic > maximumMethodAgnosticRoutes {
		t.Fatalf("route budget exceeded: routes=%d/%d method-agnostic=%d/%d; update the telemetry "+
			"design and series budget before expanding",
			len(routes), maximumRegisteredRoutes, methodAgnostic, maximumMethodAgnosticRoutes)
	}
}

// Every route carries exactly one access policy, and the policy each route
// carries is pinned. A route silently losing its admission is the failure this
// catches.
func TestEveryRouteCarriesOneCheckedAccessPolicy(t *testing.T) {
	want := map[string]accessPolicy{
		"GET /health/live":                                     accessPublic,
		"GET /health/ready":                                    accessPublic,
		"GET /api/admin/status":                                accessPublic,
		"POST /api/admin/setup":                                accessSameOrigin,
		"POST /api/admin/auth/login":                           accessSameOrigin,
		"POST /api/admin/login":                                accessSameOrigin,
		"GET /api/admin/session":                               accessAdminSameOriginRead,
		"POST /api/admin/auth/logout":                          accessAdminSession,
		"DELETE /api/admin/session":                            accessAdminSession,
		"POST /api/admin/collections/plan":                     accessAdminSession,
		"POST /api/admin/collections/apply":                    accessAdminSession,
		"GET /api/admin/schema-operations":                     accessAdminSameOriginRead,
		"GET /api/admin/schema-operations/{id}":                accessAdminSameOriginRead,
		"GET /api/admin/collections":                           accessAdminSameOriginRead,
		"GET /api/admin/system":                                accessAdminSameOriginRead,
		"GET /api/admin/system/health":                         accessAdminSameOriginRead,
		"GET /api/v1/collections/{collection}/records":         accessAdminSameOriginRead,
		"POST /api/v1/collections/{collection}/records":        accessAdminSession,
		"GET /api/v1/collections/{collection}/records/{id}":    accessAdminSameOriginRead,
		"PATCH /api/v1/collections/{collection}/records/{id}":  accessAdminSession,
		"DELETE /api/v1/collections/{collection}/records/{id}": accessAdminSession,
		"/admin":  accessEmbeddedUI,
		"/admin/": accessEmbeddedUI,
	}
	for _, declared := range declaredTestRoutes(t) {
		key := declared.muxPattern()
		expected, known := want[key]
		if !known {
			t.Fatalf("route %q has no pinned access policy", key)
		}
		if declared.access != expected {
			t.Fatalf("route %q access = %s, want %s", key, declared.access, expected)
		}
		delete(want, key)
	}
	if len(want) != 0 {
		missing := make([]string, 0, len(want))
		for key := range want {
			missing = append(missing, key)
		}
		sort.Strings(missing)
		t.Fatalf("pinned access policies with no declared route: %v", missing)
	}
}

// A buffered response holds the whole body in memory before the first byte
// reaches the client, so the cap is what keeps peak memory independent of
// catalog and page size. Schema apply is the only long-deadline route.
func TestBufferedRoutesNameACapAndOnlySchemaApplyRunsLong(t *testing.T) {
	for _, declared := range declaredTestRoutes(t) {
		key := declared.muxPattern()
		if declared.response.atomic && declared.response.maximumBytes <= 0 {
			t.Fatalf("route %q buffers its response without a cap", key)
		}
		if !declared.response.atomic && declared.response.maximumBytes != 0 {
			t.Fatalf("route %q streams but names a cap", key)
		}
		wantDeadline := deadlineDefault
		if key == "POST /api/admin/collections/apply" {
			wantDeadline = deadlineSchemaApply
		}
		if declared.deadline != wantDeadline {
			t.Fatalf("route %q deadline = %d, want %d", key, declared.deadline, wantDeadline)
		}
	}
}

func TestOnlyCredentialEntryRoutesUseTheSmallerRequestBodyCap(t *testing.T) {
	want := map[string]bool{
		"POST /api/admin/setup":      true,
		"POST /api/admin/auth/login": true,
		"POST /api/admin/login":      true,
	}
	for _, declared := range declaredTestRoutes(t) {
		key := declared.muxPattern()
		if want[key] {
			if declared.maximumRequestBytes != maximumCredentialRequestBytes {
				t.Fatalf("route %q request cap = %d, want %d", key, declared.maximumRequestBytes, maximumCredentialRequestBytes)
			}
			delete(want, key)
			continue
		}
		if declared.maximumRequestBytes != 0 {
			t.Fatalf("route %q unexpectedly narrows the global request cap to %d", key, declared.maximumRequestBytes)
		}
	}
	if len(want) != 0 {
		t.Fatalf("credential routes without a pinned request cap: %v", want)
	}
}

// The catalog and record page caps are the transport side of budgets the
// kernels already enforce. Now that the cap is enforced at runtime the two
// numbers cannot simply be equal: a kernel measures the value it produced and
// the transport writes it through json.Encoder, so the cap is the kernel bound
// plus one fixed framing allowance. Both halves are pinned, because dropping
// the allowance would reject a maximal legal response and inflating it would
// give the memory bound away.
func TestBufferedCapsMatchTheKernelBudgetsTheyMirror(t *testing.T) {
	caps := map[string]int{}
	for _, declared := range declaredTestRoutes(t) {
		caps[declared.muxPattern()] = declared.response.maximumBytes
	}
	if responseFraming != 4<<10 {
		t.Fatalf("response framing allowance = %d, want 4 KiB", responseFraming)
	}
	if got := caps["GET /api/admin/collections"]; got != 16<<20+responseFraming {
		t.Fatalf("catalog cap = %d, want the canonical 16 MiB snapshot bound plus framing", got)
	}
	for _, pattern := range []string{
		"GET /api/v1/collections/{collection}/records",
		"GET /api/v1/collections/{collection}/records/{id}",
	} {
		if got := caps[pattern]; got != 4<<20+responseFraming {
			t.Fatalf("%s cap = %d, want the 4 MiB record page budget plus framing", pattern, got)
		}
	}
	if got := caps["GET /api/admin/schema-operations"]; got != 256<<10+responseFraming {
		t.Fatalf("operation history cap = %d, want the 256 KiB ledger page bound plus framing", got)
	}
}

// The history route rejects a limit before the kernel would, so the two bounds
// have to be the same set. If the route's number drifted upward the kernel
// would answer the rejection instead, in the wrong error shape; downward and a
// legal page would become unreachable.
func TestOperationHistoryLimitMatchesTheKernelBound(t *testing.T) {
	administrator := validSession().Administrator.ID
	if _, err := schema.NewOperationQuery(administrator, maximumOperationHistoryLimit, ""); err != nil {
		t.Fatalf("kernel rejected the largest limit the route accepts: %v", err)
	}
	if _, err := schema.NewOperationQuery(administrator, maximumOperationHistoryLimit+1, ""); err == nil {
		t.Fatal("kernel accepted a limit above the one the route accepts")
	}
}

// Buffering a stream would defeat both routes that declare passthrough: the
// Prometheus exposition is not a JSON envelope and the embedded UI serves
// prebuilt assets of its own sizes. The registry is where that decision lives,
// so it is pinned here rather than inferred from either handler.
func TestOnlyStreamingRoutesDeclarePassthrough(t *testing.T) {
	streaming := map[string]bool{"GET /metrics": true, "/admin": true, "/admin/": true}
	routes := declareRoutes(completeDependencies(Dependencies{
		Metrics:           observability.NewMetrics(nil),
		MetricsExposition: http.NewServeMux(),
		AdminUI:           http.NewServeMux(),
	}), true)
	seen := 0
	for _, declared := range routes {
		key := declared.muxPattern()
		if streaming[key] {
			seen++
			if declared.response.atomic {
				t.Fatalf("route %q buffers a streaming response", key)
			}
			continue
		}
		if !declared.response.atomic {
			t.Fatalf("route %q streams but is not one of the two streaming routes", key)
		}
	}
	if seen != len(streaming) {
		t.Fatalf("declared %d of %d streaming routes", seen, len(streaming))
	}
}

func TestValidateRoutesRejectsIncompleteDuplicateAndUncappedEntries(t *testing.T) {
	handler := http.NewServeMux()
	complete := route{method: "GET", pattern: "/a", access: accessPublic,
		response: atomicJSON(1 << 10), deadline: deadlineDefault, handler: handler}
	for _, testCase := range []struct {
		name   string
		routes []route
		want   error
	}{
		{
			name:   "duplicate pattern",
			routes: []route{complete, complete},
			want:   errRouteDuplicated,
		},
		{
			name: "buffered without a cap",
			routes: []route{{method: "GET", pattern: "/a", access: accessPublic,
				response: responseMode{atomic: true}, deadline: deadlineDefault, handler: handler}},
			want: errRouteUncappedBytes,
		},
		{
			name: "streaming with a cap",
			routes: []route{{method: "GET", pattern: "/a", access: accessPublic,
				response: responseMode{maximumBytes: 1}, deadline: deadlineDefault, handler: handler}},
			want: errRouteIncomplete,
		},
		{
			name: "request cap exceeds the global cap",
			routes: []route{{method: "POST", pattern: "/a", access: accessPublic,
				response: atomicJSON(1 << 10), deadline: deadlineDefault,
				maximumRequestBytes: maxJSONBodyBytes + 1, handler: handler}},
			want: errRouteRequestCap,
		},
		{
			name: "missing access policy",
			routes: []route{{method: "GET", pattern: "/a",
				response: atomicJSON(1 << 10), deadline: deadlineDefault, handler: handler}},
			want: errRouteIncomplete,
		},
		{
			name: "missing deadline policy",
			routes: []route{{method: "GET", pattern: "/a", access: accessPublic,
				response: atomicJSON(1 << 10), handler: handler}},
			want: errRouteIncomplete,
		},
		{
			name: "missing handler",
			routes: []route{{method: "GET", pattern: "/a", access: accessPublic,
				response: atomicJSON(1 << 10), deadline: deadlineDefault}},
			want: errRouteIncomplete,
		},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			err := validateRoutes(testCase.routes)
			if !errors.Is(err, testCase.want) {
				t.Fatalf("validateRoutes() = %v, want %v", err, testCase.want)
			}
		})
	}
}

func TestValidateRoutesRejectsExceedingTheMetricSeriesBudget(t *testing.T) {
	handler := http.NewServeMux()
	routes := make([]route, 0, maximumRegisteredRoutes+1)
	for index := 0; index <= maximumRegisteredRoutes; index++ {
		routes = append(routes, route{
			method: "GET", pattern: "/route/" + strings.Repeat("a", index+1),
			access: accessPublic, response: atomicJSON(1 << 10),
			deadline: deadlineDefault, handler: handler,
		})
	}
	if err := validateRoutes(routes); !errors.Is(err, errRouteCardinality) {
		t.Fatalf("validateRoutes(oversized) = %v, want %v", err, errRouteCardinality)
	}

	methodAgnostic := make([]route, 0, maximumMethodAgnosticRoutes+1)
	for index := 0; index <= maximumMethodAgnosticRoutes; index++ {
		methodAgnostic = append(methodAgnostic, route{
			pattern: "/any/" + strings.Repeat("b", index+1),
			access:  accessPublic, response: atomicJSON(1 << 10),
			deadline: deadlineDefault, handler: handler,
		})
	}
	if err := validateRoutes(methodAgnostic); !errors.Is(err, errRouteCardinality) {
		t.Fatalf("validateRoutes(method-agnostic) = %v, want %v", err, errRouteCardinality)
	}
}

// The schema apply deadline has to reach two places: the handler context, so
// it can travel into SchemaKernel and the pgx transaction, and the real
// connection's write deadline, so a DDL that commits at the edge of its window
// can still deliver its response. A wrapper always sits between the two, so
// this also pins that a wrapping writer stays unwrappable.
func TestSchemaApplyDeadlineReachesTheHandlerAndTheConnection(t *testing.T) {
	const timeout = 90 * time.Second
	var (
		observedDeadline time.Time
		observedOK       bool
	)
	probe := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		observedDeadline, observedOK = r.Context().Deadline()
		w.WriteHeader(http.StatusNoContent)
	})
	connection := &deadlineWriter{ResponseWriter: httptest.NewRecorder()}

	before := time.Now()
	applyDeadline(deadlineSchemaApply, timeout, probe).
		ServeHTTP(&bufferedResponseWriter{destination: connection, header: make(http.Header)},
			httptest.NewRequest(http.MethodPost, "/api/admin/collections/apply", nil))
	after := time.Now()

	if !observedOK {
		t.Fatal("schema apply handler ran with no context deadline")
	}
	if observedDeadline.Before(before.Add(timeout)) || observedDeadline.After(after.Add(timeout)) {
		t.Fatalf("handler deadline = %s, want about %s from now", observedDeadline, timeout)
	}
	if want := observedDeadline.Add(responseMargin); !connection.writeDeadline.Equal(want) {
		t.Fatalf("connection write deadline = %s, want the handler deadline plus the %s response margin (%s)",
			connection.writeDeadline, responseMargin, want)
	}
}

// A route without the schema-apply policy must not acquire a deadline, and
// must not touch the connection's write deadline either.
func TestDefaultDeadlinePolicyLeavesTheRequestAndConnectionAlone(t *testing.T) {
	var observedOK bool
	probe := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, observedOK = r.Context().Deadline()
		w.WriteHeader(http.StatusNoContent)
	})
	connection := &deadlineWriter{ResponseWriter: httptest.NewRecorder()}
	applyDeadline(deadlineDefault, 90*time.Second, probe).
		ServeHTTP(connection, httptest.NewRequest(http.MethodGet, "/api/admin/collections", nil))
	if observedOK {
		t.Fatal("default-deadline route received a context deadline")
	}
	if !connection.writeDeadline.IsZero() {
		t.Fatalf("default-deadline route set a write deadline of %s", connection.writeDeadline)
	}
}

// Fail closed: if the write deadline cannot be set, the response could be cut
// off mid-body later, and for a committed DDL that is worse than refusing the
// request. It must end before any header, body or domain call.
func TestSchemaApplyRefusesWhenTheWriteDeadlineCannotBeSet(t *testing.T) {
	called := false
	probe := http.HandlerFunc(func(http.ResponseWriter, *http.Request) { called = true })
	response := httptest.NewRecorder()
	applyDeadline(deadlineSchemaApply, 90*time.Second, probe).
		ServeHTTP(response, httptest.NewRequest(http.MethodPost, "/api/admin/collections/apply", nil))
	if called {
		t.Fatal("schema apply reached the handler without a write deadline")
	}
	if response.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want %d", response.Code, http.StatusServiceUnavailable)
	}
	if got := response.Header().Get("Retry-After"); got != "1" {
		t.Fatalf("Retry-After = %q, want %q", got, "1")
	}
	assertErrorCode(t, response.Body.Bytes(), "schema_apply_unavailable")
}
