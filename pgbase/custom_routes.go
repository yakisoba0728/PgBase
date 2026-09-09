package pgbase

import (
	"context"

	"github.com/webisnoteasy/PgBase/internal/api"
)

// CustomRoute registers an application handler inside PgBase's request
// middleware. Patterns use Go ServeMux syntax, for example /reports/{id}.
// The first segment must be literal; api/admin/health/metrics/auth are reserved.
// Handlers are installed at startup and must be safe for concurrent requests.
// Responses are buffered (256 KiB default, at most 4 MiB plus framing); request
// bodies are capped at 1 MiB by default. Streaming and websocket upgrades are
// not supported by this buffered interface. Handlers must honor cancellation.
type CustomRoute = api.CustomRoute

type CustomRouteAccess = api.CustomRouteAccess

const (
	// RoutePublic does not interpret credentials. Choose it explicitly only
	// for operations that require no authenticated identity.
	RoutePublic = api.CustomRoutePublic
	// RouteUser authenticates an active user session. Handlers can additionally
	// restrict the auth collection using PrincipalFromContext.
	RouteUser = api.CustomRouteUser
	// RouteAdmin authenticates the administrator cookie and applies the same
	// origin and mutation CSRF requirements as built-in administrator routes.
	RouteAdmin = api.CustomRouteAdmin
	// RouteService authenticates a service token; application-specific
	// resource authorization remains the handler's responsibility.
	RouteService = api.CustomRouteService
)

// RequestPrincipal exposes an authenticated identity without credentials.
type RequestPrincipal = api.CustomPrincipal

// PrincipalFromContext returns the identity admitted for this request. A public
// route returns false even when the request contains a session or bearer token.
func PrincipalFromContext(ctx context.Context) (RequestPrincipal, bool) {
	return api.CustomPrincipalFromContext(ctx)
}
