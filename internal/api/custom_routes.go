package api

import (
	"context"
	"fmt"
	"net/http"
	"path"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/webisnoteasy/PgBase/internal/identity"
)

// CustomRoute is a trusted Go extension installed when the application starts.
// Access must be explicit: its zero value never exposes a handler publicly.
type CustomRoute struct {
	Method               string
	Pattern              string
	Access               CustomRouteAccess
	Handler              http.Handler
	MaximumRequestBytes  int64
	MaximumResponseBytes int
}

type CustomRouteAccess string

const (
	CustomRoutePublic   CustomRouteAccess = "public"
	CustomRouteUser     CustomRouteAccess = "user"
	CustomRouteAdmin    CustomRouteAccess = "administrator"
	CustomRouteService  CustomRouteAccess = "service"
	MaximumCustomRoutes                   = 64
)

// CustomPrincipal is a credential-free view of the admitted request identity.
// Public routes have no principal, even if the caller presents credentials.
type CustomPrincipal struct {
	Kind                         string    `json:"kind"`
	ID                           string    `json:"id"`
	AuthCollectionID             string    `json:"authCollectionId,omitempty"`
	Verified                     bool      `json:"verified,omitempty"`
	ExpiresAt                    time.Time `json:"expiresAt,omitempty"`
	Impersonated                 bool      `json:"impersonated,omitempty"`
	ImpersonatingAdministratorID string    `json:"impersonatingAdministratorId,omitempty"`
}

func CustomPrincipalFromContext(ctx context.Context) (CustomPrincipal, bool) {
	if admin, ok := AdminPrincipalFromContext(ctx); ok {
		return CustomPrincipal{Kind: string(identity.Administrator), ID: admin.Administrator.ID.String(), ExpiresAt: admin.ExpiresAt}, true
	}
	if user, ok := ctx.Value(userPrincipalKey{}).(authenticatedUser); ok {
		principal := CustomPrincipal{Kind: string(identity.User), ID: user.principal.User.ID.String(),
			AuthCollectionID: user.principal.User.AuthCollectionID.String(), Verified: user.principal.User.Verified,
			ExpiresAt: user.principal.ExpiresAt, Impersonated: user.principal.Impersonated}
		if user.principal.Impersonated {
			principal.ImpersonatingAdministratorID = user.principal.ImpersonatingAdministratorID.String()
		}
		return principal, true
	}
	if subject, ok := ctx.Value(publicRecordPrincipalKey{}).(identity.Subject); ok && subject.Kind == identity.Service {
		return CustomPrincipal{Kind: string(identity.Service), ID: subject.ID.String()}, true
	}
	return CustomPrincipal{}, false
}

// ExtensionRequestContext returns the already-admitted domain subject and the
// normalized request identity for in-process extension adapters. It never
// returns bearer, cookie, CSRF, or impersonation credentials. The request ID
// marker prevents a plain context.Background from being treated as an admitted
// anonymous HTTP request.
func ExtensionRequestContext(ctx context.Context) (identity.Subject, string, bool) {
	id := requestID(ctx)
	if id == "" {
		return identity.Subject{}, "", false
	}
	if admin, ok := AdminPrincipalFromContext(ctx); ok && admin.Administrator.ID != uuid.Nil {
		return identity.Subject{Kind: identity.Administrator, ID: admin.Administrator.ID}, id, true
	}
	if user, ok := ctx.Value(userPrincipalKey{}).(authenticatedUser); ok && user.principal.User.ID != uuid.Nil {
		return identity.Subject{Kind: identity.User, ID: user.principal.User.ID}, id, true
	}
	if subject, ok := ctx.Value(publicRecordPrincipalKey{}).(identity.Subject); ok && subject.Valid() {
		return subject, id, true
	}
	return identity.Subject{Kind: identity.Anonymous}, id, true
}

// ValidateCustomRoutes is also called before opening the database so malformed
// patterns, conflicts and invalid limits fail without starting an application.
func ValidateCustomRoutes(custom []CustomRoute) error {
	if len(custom) > MaximumCustomRoutes {
		return fmt.Errorf("CustomRoutes: at most %d routes are supported", MaximumCustomRoutes)
	}
	mux := http.NewServeMux()
	for _, item := range custom {
		if err := validateCustomRoute(item); err != nil {
			return fmt.Errorf("CustomRoutes %q: %w", item.Method+" "+item.Pattern, err)
		}
		if err := registerRoute(mux, item.Method+" "+item.Pattern, item.Handler); err != nil {
			return fmt.Errorf("CustomRoutes: %w", err)
		}
	}
	return nil
}

func validateCustomRoute(item CustomRoute) error {
	switch item.Method {
	case http.MethodGet, http.MethodHead, http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete:
	default:
		return fmt.Errorf("method must be GET, HEAD, POST, PUT, PATCH or DELETE")
	}
	if item.Handler == nil {
		return fmt.Errorf("handler is required")
	}
	switch item.Access {
	case CustomRoutePublic, CustomRouteUser, CustomRouteAdmin, CustomRouteService:
	default:
		return fmt.Errorf("access must be public, user, administrator or service")
	}
	if len(item.Pattern) > 256 || !utf8.ValidString(item.Pattern) || !strings.HasPrefix(item.Pattern, "/") ||
		strings.ContainsAny(item.Pattern, " ?#%\\\t\r\n") || strings.Contains(item.Pattern, "//") ||
		path.Clean(item.Pattern) != strings.TrimSuffix(item.Pattern, "/") {
		return fmt.Errorf("pattern must be a canonical absolute path of at most 256 bytes")
	}
	for _, character := range item.Pattern {
		if unicode.IsControl(character) || unicode.IsSpace(character) {
			return fmt.Errorf("pattern cannot contain control characters or whitespace")
		}
	}
	first := strings.Split(strings.TrimPrefix(item.Pattern, "/"), "/")[0]
	if first == "" || strings.ContainsAny(first, "{}") {
		return fmt.Errorf("pattern must start with a literal application namespace")
	}
	switch first {
	case "api", "admin", "health", "metrics", "auth":
		return fmt.Errorf("namespace %q is reserved for PgBase", first)
	}
	if item.MaximumRequestBytes < 0 || item.MaximumRequestBytes > maxJSONBodyBytes {
		return fmt.Errorf("maximum request bytes must be between 0 and %d", maxJSONBodyBytes)
	}
	if item.MaximumResponseBytes < 0 || item.MaximumResponseBytes > maximumCustomRouteResponseBytes {
		return fmt.Errorf("maximum response bytes must be between 0 and %d", maximumCustomRouteResponseBytes)
	}
	return nil
}

func (d Dependencies) customRoutes() ([]route, error) {
	if err := ValidateCustomRoutes(d.CustomRoutes); err != nil {
		return nil, err
	}
	result := make([]route, 0, len(d.CustomRoutes))
	for _, item := range d.CustomRoutes {
		policy := accessPublic
		switch item.Access {
		case CustomRouteUser:
			if d.UserAuth == nil {
				return nil, fmt.Errorf("CustomRoutes: user authentication is unavailable")
			}
			policy = accessUserSession
		case CustomRouteAdmin:
			policy = accessAdminSession
			if item.Method == http.MethodGet || item.Method == http.MethodHead {
				policy = accessAdminSameOriginRead
			}
		case CustomRouteService:
			if d.ServiceTokens == nil {
				return nil, fmt.Errorf("CustomRoutes: service token authentication is unavailable")
			}
			policy = accessServiceSession
		}
		responseBytes := item.MaximumResponseBytes
		if responseBytes == 0 {
			responseBytes = maximumAdminResponseBytes
		}
		result = append(result, route{method: item.Method, pattern: item.Pattern, access: policy,
			response: atomicJSON(responseBytes), deadline: deadlineDefault,
			maximumRequestBytes: item.MaximumRequestBytes, handler: item.Handler,
			noStore: item.Access != CustomRoutePublic})
	}
	return result, nil
}

func noStoreCustomHandler(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		next.ServeHTTP(w, r)
		w.Header().Set("Cache-Control", "no-store")
	})
}

// ServeMux reports both malformed patterns and overlapping patterns by panic.
// Turn those configuration mistakes into construction errors, never a crash.
func registerRoute(mux *http.ServeMux, pattern string, handler http.Handler) (err error) {
	defer func() {
		if recovered := recover(); recovered != nil {
			err = fmt.Errorf("invalid or conflicting route %q: %v", pattern, recovered)
		}
	}()
	mux.Handle(pattern, handler)
	return nil
}
