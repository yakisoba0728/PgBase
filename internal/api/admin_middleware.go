package api

import (
	"context"
	"crypto/hmac"
	"errors"
	"net/http"
	"net/netip"
	"net/url"
	"strings"
	"time"

	"github.com/webisnoteasy/PgBase/internal/adminauth"
	"github.com/webisnoteasy/PgBase/internal/database"
)

type adminPrincipalKey struct{}

// AdminPrincipal is the authenticated administrator and the raw session bound
// to the current request. RawSession must never be serialized or logged.
type AdminPrincipal struct {
	Administrator adminauth.Administrator
	RawSession    string
	ExpiresAt     time.Time
	// CSRFToken comes from the authentication result. The transport compares
	// it rather than deriving it, so the token secret stays inside adminauth.
	CSRFToken string
}

// AdminPrincipalFromContext returns the authenticated principal installed by
// the administrator-session middleware.
func AdminPrincipalFromContext(ctx context.Context) (AdminPrincipal, bool) {
	principal, ok := ctx.Value(adminPrincipalKey{}).(AdminPrincipal)
	return principal, ok
}

func sameOriginMiddleware(publicURL *url.URL, trustedProxies []netip.Prefix, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !sameOrigin(r, publicURL, trustedProxies) {
			writeAPIError(w, r, APIError{Status: http.StatusForbidden, Code: "origin_rejected", Message: "Request origin is not allowed"})
			return
		}
		next.ServeHTTP(w, r)
	})
}

func adminSessionMiddleware(auth AdminAuth, publicURL *url.URL, trustedProxies []netip.Prefix, next http.Handler) http.Handler {
	handler := csrfMutationMiddleware(next)
	handler = authenticateAdminMiddleware(auth, handler)
	return originMutationMiddleware(publicURL, trustedProxies, handler)
}

// adminSameOriginReadMiddleware admits an authenticated read only from this
// deployment's own origin. A read cannot demand an Origin header the way a
// mutation does — a browser sends none on a same-origin GET, and a page cannot
// add one — so the rule is that a presented origin must be ours. That still
// closes the case this route has to close: a cross-origin fetch always sends
// Origin, so a configured CORS allowance cannot hand another site a report
// describing the operator's own schema, while the admin page and a non-browser
// operator client are unaffected.
func adminSameOriginReadMiddleware(auth AdminAuth, publicURL *url.URL, trustedProxies []netip.Prefix, next http.Handler) http.Handler {
	handler := authenticateAdminMiddleware(auth, next)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if len(r.Header.Values("Origin")) > 0 && !sameOrigin(r, publicURL, trustedProxies) {
			writeAPIError(w, r, APIError{Status: http.StatusForbidden, Code: "origin_rejected", Message: "Request origin is not allowed"})
			return
		}
		handler.ServeHTTP(w, r)
	})
}

func originMutationMiddleware(publicURL *url.URL, trustedProxies []netip.Prefix, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if isAdminMutation(r.Method) && !sameOrigin(r, publicURL, trustedProxies) {
			writeAPIError(w, r, APIError{Status: http.StatusForbidden, Code: "origin_rejected", Message: "Request origin is not allowed"})
			return
		}
		next.ServeHTTP(w, r)
	})
}

func authenticateAdminMiddleware(auth AdminAuth, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		cookie, err := r.Cookie(adminCookieName)
		if err != nil || cookie.Value == "" || auth == nil {
			writeAPIError(w, r, APIError{Status: http.StatusUnauthorized, Code: "authentication_required", Message: "Administrator authentication is required"})
			return
		}
		authenticated, err := auth.Authenticate(r.Context(), cookie.Value)
		if err != nil {
			if errors.Is(err, adminauth.ErrInvalidCredentials) {
				writeAPIError(w, r, APIError{Status: http.StatusUnauthorized, Code: "authentication_required", Message: "Administrator authentication is required"})
				return
			}
			if database.Classify(err) == database.KindUnavailable {
				w.Header().Set("Retry-After", "1")
				writeAPIError(w, r, APIError{Status: http.StatusServiceUnavailable, Code: "authentication_unavailable", Message: "Administrator authentication is unavailable"})
				return
			}
			writeAPIError(w, r, APIError{Status: http.StatusInternalServerError, Code: "internal_error", Message: "Internal server error"})
			return
		}
		principal := AdminPrincipal{
			Administrator: authenticated.Administrator, RawSession: cookie.Value,
			ExpiresAt: authenticated.ExpiresAt, CSRFToken: authenticated.CSRFToken,
		}
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), adminPrincipalKey{}, principal)))
	})
}

func csrfMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		principal, ok := AdminPrincipalFromContext(r.Context())
		provided := r.Header.Get("X-PgBase-CSRF")
		if !ok || provided == "" || principal.CSRFToken == "" ||
			!hmac.Equal([]byte(provided), []byte(principal.CSRFToken)) {
			writeAPIError(w, r, APIError{Status: http.StatusForbidden, Code: "csrf_rejected", Message: "CSRF verification failed"})
			return
		}
		next.ServeHTTP(w, r)
	})
}

func csrfMutationMiddleware(next http.Handler) http.Handler {
	protected := csrfMiddleware(next)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !isAdminMutation(r.Method) {
			next.ServeHTTP(w, r)
			return
		}
		protected.ServeHTTP(w, r)
	})
}

func isAdminMutation(method string) bool {
	switch method {
	case http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete:
		return true
	default:
		return false
	}
}

func sameOrigin(r *http.Request, publicURL *url.URL, trustedProxies []netip.Prefix) bool {
	values := r.Header.Values("Origin")
	if len(values) != 1 {
		return false
	}
	presented, err := url.Parse(values[0])
	if err != nil || presented.User != nil || presented.Opaque != "" || presented.Scheme == "" || presented.Host == "" ||
		presented.Path != "" || presented.RawPath != "" || presented.ForceQuery || presented.RawQuery != "" || presented.Fragment != "" {
		return false
	}
	expectedScheme := requestScheme(r, trustedProxies)
	expectedHost := r.Host
	if publicURL != nil {
		expectedScheme = publicURL.Scheme
		expectedHost = publicURL.Host
	}
	return strings.EqualFold(presented.Scheme, expectedScheme) && strings.EqualFold(presented.Host, expectedHost)
}

func secureAdminCookie(r *http.Request, publicURL *url.URL, trustedProxies []netip.Prefix) bool {
	if publicURL != nil {
		return strings.EqualFold(publicURL.Scheme, "https")
	}
	return requestScheme(r, trustedProxies) == "https"
}

func requestScheme(r *http.Request, trustedProxies []netip.Prefix) string {
	if peer, ok := remoteAddress(r.RemoteAddr); ok && addressTrusted(peer, trustedProxies) {
		values := r.Header.Values("X-Forwarded-Proto")
		if len(values) == 1 {
			value := strings.TrimSpace(values[0])
			if value == "http" || value == "https" {
				return value
			}
		}
	}
	if r.TLS != nil {
		return "https"
	}
	return "http"
}
