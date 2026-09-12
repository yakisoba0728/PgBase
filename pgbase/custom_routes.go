package pgbase

import (
	"context"
	"io/fs"
	"net/http"
	"path"
	"strings"

	"github.com/yakisoba0728/PgBase/internal/api"
)

// RouteMiddleware decorates a trusted Go route handler. Middleware order is
// outermost first, matching conventional net/http composition.
type RouteMiddleware func(http.Handler) http.Handler

// RouteGroup builds related custom routes with a shared prefix and middleware.
// The zero value is a root group. Routes returns a defensive copy suitable for
// Options.CustomRoutes.
type RouteGroup struct {
	prefix     string
	middleware []RouteMiddleware
	routes     *[]CustomRoute
}

// NewRouteGroup creates an independent route builder.
func NewRouteGroup(prefix string, middleware ...RouteMiddleware) *RouteGroup {
	routes := []CustomRoute{}
	return &RouteGroup{prefix: cleanRoutePrefix(prefix), middleware: append([]RouteMiddleware(nil), middleware...), routes: &routes}
}

// Group returns a child that appends routes to the same builder.
func (g *RouteGroup) Group(prefix string, middleware ...RouteMiddleware) *RouteGroup {
	g.ensureRoutes()
	child := &RouteGroup{prefix: joinRoutePattern(g.prefix, prefix), routes: g.routes}
	child.middleware = append(append([]RouteMiddleware(nil), g.middleware...), middleware...)
	return child
}

// Use adds middleware for routes registered after this call.
func (g *RouteGroup) Use(middleware ...RouteMiddleware) {
	g.middleware = append(g.middleware, middleware...)
}

// Handle registers a buffered route.
func (g *RouteGroup) Handle(method, pattern string, access CustomRouteAccess, handler http.Handler) {
	g.append(CustomRoute{Method: method, Pattern: joinRoutePattern(g.prefix, pattern), Access: access, Handler: handler})
}

// Stream registers an SSE, download, or protocol-upgrade route whose writes
// are sent directly after PgBase admission succeeds.
func (g *RouteGroup) Stream(method, pattern string, access CustomRouteAccess, handler http.Handler) {
	g.append(CustomRoute{Method: method, Pattern: joinRoutePattern(g.prefix, pattern), Access: access, Handler: handler, StreamResponse: true})
}

// Static serves a filesystem below pattern using a streaming public route.
func (g *RouteGroup) Static(pattern string, access CustomRouteAccess, filesystem fs.FS) {
	full := strings.TrimSuffix(joinRoutePattern(g.prefix, pattern), "/") + "/"
	handler := http.StripPrefix(strings.TrimSuffix(full, "/"), http.FileServerFS(filesystem))
	g.append(CustomRoute{Method: http.MethodGet, Pattern: full + "{path...}", Access: access, Handler: handler, StreamResponse: true})
}

func (g *RouteGroup) Routes() []CustomRoute {
	if g == nil || g.routes == nil {
		return nil
	}
	return append([]CustomRoute(nil), (*g.routes)...)
}

func (g *RouteGroup) append(route CustomRoute) {
	g.ensureRoutes()
	handler := route.Handler
	for index := len(g.middleware) - 1; index >= 0; index-- {
		handler = g.middleware[index](handler)
	}
	route.Handler = handler
	*g.routes = append(*g.routes, route)
}

func (g *RouteGroup) ensureRoutes() {
	if g.routes == nil {
		routes := []CustomRoute{}
		g.routes = &routes
	}
}

func cleanRoutePrefix(prefix string) string {
	if prefix == "" || prefix == "/" {
		return ""
	}
	return "/" + strings.Trim(path.Clean("/"+prefix), "/")
}

func joinRoutePattern(prefix, pattern string) string {
	if pattern == "" || pattern == "/" {
		if prefix == "" {
			return "/"
		}
		return prefix
	}
	return strings.TrimSuffix(prefix, "/") + "/" + strings.TrimPrefix(pattern, "/")
}

// CustomRoute registers an application handler inside PgBase's request
// middleware. Patterns use Go ServeMux syntax, for example /reports/{id}.
// The first segment must be literal; api/admin/health/metrics/auth are reserved.
// Handlers are installed at startup and must be safe for concurrent requests.
// Responses are buffered (256 KiB default, at most 4 MiB plus framing) unless
// StreamResponse is selected. Request bodies are capped at 1 MiB by default.
// Handlers must honor cancellation.
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
