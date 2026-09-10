package api

import (
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/yakisoba0728/PgBase/internal/backup"
)

// accessPolicy names the admission a route requires. Exactly one applies to
// each route, so a reviewer can read admission off the registry instead of
// reconstructing it from a nested middleware call.
type accessPolicy uint8

const (
	accessPublic accessPolicy = iota + 1
	accessSameOrigin
	accessAdminSession
	accessAdminSameOriginRead
	accessMetricsToken
	accessEmbeddedUI
	accessUserSession
	accessServiceSession
	accessAnonymous
)

// responseMode separates a response the router buffers whole from one it
// streams. Buffering bounds peak memory per request, so every buffered route
// must name the bound it accepts; a passthrough route must not name one.
type responseMode struct {
	atomic       bool
	maximumBytes int
}

func atomicJSON(maximumBytes int) responseMode {
	return responseMode{atomic: true, maximumBytes: maximumBytes}
}

func passthrough() responseMode { return responseMode{} }

// deadlinePolicy names which server deadline a route runs under. Long-running
// operations carry their exception explicitly rather than widening timeouts
// for every route.
type deadlinePolicy uint8

const (
	deadlineDefault deadlinePolicy = iota + 1
	deadlineSchemaApply
	deadlineBackupTransfer
)

type route struct {
	method              string
	pattern             string
	access              accessPolicy
	response            responseMode
	deadline            deadlinePolicy
	maximumRequestBytes int64
	streamedUpload      bool
	schemaDocument      bool
	recordDocument      bool
	noStore             bool
	handler             http.Handler
}

// A route with no method is matched for every method, which the registry
// counts separately because each one costs a metric series per method the
// internet sends.
func (r route) muxPattern() string {
	if r.method == "" {
		return r.pattern
	}
	return r.method + " " + r.pattern
}

func (r route) String() string { return r.muxPattern() }

var (
	errRouteIncomplete    = errors.New("route registry entry is incomplete")
	errRouteDuplicated    = errors.New("route registry declares one pattern twice")
	errRouteUncappedBytes = errors.New("route registry declares a buffered response without a byte cap")
	errRouteRequestCap    = errors.New("route registry declares an invalid request body cap")
	errRouteCardinality   = errors.New("route registry exceeds the HTTP metric series budget")
)

// The metric series budget the operational telemetry design was sized for. A
// method-agnostic route costs a series for every method the internet sends, so
// those are budgeted separately and much more tightly.
const (
	maximumRegisteredRoutes     = 150
	maximumMethodAgnosticRoutes = 3
)

func validateRoutes(routes []route) error {
	seen := make(map[string]struct{}, len(routes))
	methodAgnostic := 0
	for _, declared := range routes {
		if declared.pattern == "" || declared.handler == nil ||
			declared.access == 0 || declared.deadline == 0 {
			return fmt.Errorf("%w: %q", errRouteIncomplete, declared)
		}
		if declared.response.atomic && declared.response.maximumBytes <= 0 {
			return fmt.Errorf("%w: %q", errRouteUncappedBytes, declared)
		}
		if !declared.response.atomic && declared.response.maximumBytes != 0 {
			return fmt.Errorf("%w: %q", errRouteIncomplete, declared)
		}
		if declared.maximumRequestBytes < 0 || declared.maximumRequestBytes > backup.MaximumArchiveBytes {
			return fmt.Errorf("%w: %q", errRouteRequestCap, declared)
		}
		if declared.maximumRequestBytes > maxJSONBodyBytes && !declared.streamedUpload && !declared.schemaDocument && !declared.recordDocument {
			return fmt.Errorf("%w: %q", errRouteRequestCap, declared)
		}
		if declared.recordDocument && (declared.schemaDocument || declared.streamedUpload || declared.maximumRequestBytes != maximumRecordRequestBytes || !isRecordWriteRoute(declared)) {
			return fmt.Errorf("%w: %q", errRouteRequestCap, declared)
		}
		if declared.schemaDocument && (declared.streamedUpload || declared.method != "POST" ||
			declared.access != accessAdminSession || declared.maximumRequestBytes != maximumCatalogResponseBytes ||
			(declared.pattern != "/api/admin/schema/import/preview" && declared.pattern != "/api/admin/schema/import/apply")) {
			return fmt.Errorf("%w: %q", errRouteRequestCap, declared)
		}
		if declared.streamedUpload && (declared.maximumRequestBytes == 0 || declared.method != "POST") {
			return fmt.Errorf("%w: %q", errRouteRequestCap, declared)
		}
		if declared.method == "" {
			methodAgnostic++
		}
		key := declared.muxPattern()
		if _, duplicate := seen[key]; duplicate {
			return fmt.Errorf("%w: %q", errRouteDuplicated, key)
		}
		seen[key] = struct{}{}
	}
	if len(routes) > maximumRegisteredRoutes || methodAgnostic > maximumMethodAgnosticRoutes {
		return fmt.Errorf("%w: routes=%d/%d method-agnostic=%d/%d",
			errRouteCardinality, len(routes), maximumRegisteredRoutes,
			methodAgnostic, maximumMethodAgnosticRoutes)
	}
	return nil
}

func (p accessPolicy) String() string {
	switch p {
	case accessPublic:
		return "public"
	case accessSameOrigin:
		return "same-origin"
	case accessAdminSession:
		return "admin-session"
	case accessAdminSameOriginRead:
		return "admin-same-origin-read"
	case accessMetricsToken:
		return "metrics-token"
	case accessEmbeddedUI:
		return "embedded-ui"
	case accessServiceSession:
		return "service-session"
	case accessAnonymous:
		return "anonymous"
	case accessUserSession:
		return "user-session"
	}
	return "unknown"
}

// admit wraps a handler in the middleware its declared access policy requires.
// Keeping this in one place is what makes the policy column authoritative:
// a route cannot silently acquire a different admission by being registered
// through a different call shape.
func (d Dependencies) admit(policy accessPolicy, handler http.Handler) http.Handler {
	switch policy {
	case accessServiceSession:
		return serviceSessionMiddleware(d.ServiceTokens, handler)
	case accessAnonymous:
		return anonymousSessionMiddleware(handler)
	case accessUserSession:
		return userSessionMiddleware(d.UserAuth, handler)
	case accessSameOrigin:
		return sameOriginMiddleware(d.PublicURL, d.TrustedProxies, handler)
	case accessAdminSession:
		return adminSessionMiddleware(d.AdminAuth, d.PublicURL, d.TrustedProxies, handler)
	case accessAdminSameOriginRead:
		return adminSameOriginReadMiddleware(d.AdminAuth, d.PublicURL, d.TrustedProxies, handler)
	}
	return handler
}

// requiredModules reports the dependencies the declared routes need. Handlers
// used to answer a request with an internal error when a module was missing;
// a missing module is a wiring mistake, so the router refuses to be built.
func (d Dependencies) requiredModules() error {
	missing := make([]string, 0, 4)
	if d.Readiness == nil {
		missing = append(missing, "Readiness")
	}
	if d.AdminAuth == nil {
		missing = append(missing, "AdminAuth")
	}
	if d.Schema == nil {
		missing = append(missing, "Schema")
	}
	if d.Records == nil {
		missing = append(missing, "Records")
	}
	if d.System == nil {
		missing = append(missing, "System")
	}
	if d.SchemaHealth == nil {
		missing = append(missing, "SchemaHealth")
	}
	if (d.UserAuth == nil) != (d.RecordAccess == nil) {
		missing = append(missing, "UserAuth and RecordAccess must be configured together")
	}
	if d.SchemaApplyTimeout <= 0 {
		missing = append(missing, "SchemaApplyTimeout")
	}
	if len(missing) == 0 {
		return nil
	}
	return fmt.Errorf("construct API router: missing %s", strings.Join(missing, ", "))
}

// responseFraming is the fixed allowance a buffered response gets on top of
// the kernel budget it mirrors. A kernel measures the value it produced; the
// transport writes that value through json.Encoder, which appends a newline
// and can frame a wrapper slightly differently. Without the allowance the cap
// would reject a response the kernel considers legal, which is the one failure
// enforcement must not introduce.
const responseFraming = 4 << 10

// Response caps. Each names the largest body the router is willing to hold in
// memory for that class of route, so an admitted request cannot make peak
// buffer usage a function of catalog or page size.
const (
	// Health and admin envelopes are small fixed shapes; the cap only has to
	// be generous enough that a redacted error can never be truncated.
	maximumHealthResponseBytes = 64 << 10
	maximumAdminResponseBytes  = 256 << 10
	// The catalog reuses the canonical snapshot bound the schema package
	// already enforces, so the router never admits a catalog it could not
	// have produced.
	maximumCatalogResponseBytes = 16<<20 + responseFraming
	// Record pages reuse the read budget the query package enforces, so a
	// page that fit the kernel's contract cannot fail at the transport.
	maximumRecordPageResponseBytes = 48<<20 + 2*responseFraming
	maximumBatchResponseBytes      = 48<<20 + responseFraming + 32<<10
	// Canonical records are at most 8 MiB; JSON escaping can expand a byte sixfold.
	maximumRecordRequestBytes       = 48<<20 + responseFraming
	maximumCustomRouteResponseBytes = 4<<20 + responseFraming
	// Schema operation history reuses the 256 KiB serialized page bound the
	// ledger enforces when it decides how many receipts fit one page, so a
	// page the ledger was willing to build is a page the router can deliver.
	maximumOperationPageResponseBytes = 256<<10 + responseFraming
	// A finding serializes to a severity, a stable code and a fixed operator
	// message and never to an identifier, so none reaches 512 bytes. The cap
	// below therefore bounds the findings at well under 16 KiB, and the rest
	// of this budget covers the probe list and timestamps.
	maximumSchemaHealthResponseBytes = 32<<10 + responseFraming
)

// MaximumSchemaFindings bounds how many findings one diagnostic report may
// carry over HTTP. Findings are already deduplicated by code and name no
// collection, field or row, so a run cannot produce more than the number of
// codes this binary defines; the cap is what keeps the payload bounded if that
// code set later outgrows the budget above. A report that hits it says so
// through its finding count, and `pgbase doctor` still prints every finding.
const MaximumSchemaFindings = 32

func isRecordWriteRoute(declared route) bool {
	var prefix string
	switch declared.access {
	case accessAdminSession:
		prefix = "/api/v1/collections/"
	case accessUserSession:
		prefix = "/api/v1/user/collections/"
	case accessAnonymous:
		prefix = "/api/v1/public/collections/"
	case accessServiceSession:
		prefix = "/api/v1/service/collections/"
	default:
		return false
	}
	return declared.method == "POST" && declared.pattern == prefix+"{collection}/records" ||
		declared.method == "PATCH" && declared.pattern == prefix+"{collection}/records/{id}"
}
