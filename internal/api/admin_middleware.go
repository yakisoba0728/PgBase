package api

import (
	"context"
	"crypto/hmac"
	"encoding/base64"
	"errors"
	"net/http"
	"net/netip"
	"net/url"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/yakisoba0728/PgBase/internal/adminauth"
	"github.com/yakisoba0728/PgBase/internal/database"
)

type adminPrincipalKey struct{}

type adminIPAuthorizer interface {
	AuthorizeIP(context.Context, uuid.UUID, string) error
}

const adminSessionTokenBytes = 32

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

func AdminPrincipalFromContext(ctx context.Context) (AdminPrincipal, bool) {
	principal, ok := ctx.Value(adminPrincipalKey{}).(AdminPrincipal)
	return principal, ok
}

// A missing provider means no proxies are trusted. Call it per request so
// published runtime settings are observed without rebuilding the router.
func currentTrustedProxies(provider func() []netip.Prefix) []netip.Prefix {
	if provider == nil {
		return nil
	}
	return provider()
}

func sameOriginMiddleware(publicURL *url.URL, trustedProxies func() []netip.Prefix, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !sameOrigin(r, publicURL, currentTrustedProxies(trustedProxies)) {
			writeAPIError(w, r, APIError{Status: http.StatusForbidden, Code: "origin_rejected", Message: "Request origin is not allowed"})
			return
		}
		next.ServeHTTP(w, r)
	})
}

func adminSessionMiddleware(auth AdminAuth, publicURL *url.URL, trustedProxies func() []netip.Prefix, next http.Handler) http.Handler {
	handler := csrfMutationMiddleware(next)
	handler = authenticateAdminMiddleware(auth, publicURL, trustedProxies, handler)
	return originMutationMiddleware(publicURL, trustedProxies, handler)
}

// adminSameOriginReadMiddleware admits a cookie-authenticated read when Origin
// is absent or names this deployment. A read cannot demand the header the way a
// mutation does: browsers omit it on same-origin GETs, and non-browser operator
// clients commonly omit it too. Cross-origin fetches present Origin and are
// rejected even when global CORS allows that site to read public responses.
func adminSameOriginReadMiddleware(auth AdminAuth, publicURL *url.URL, trustedProxies func() []netip.Prefix, next http.Handler) http.Handler {
	handler := authenticateAdminMiddleware(auth, publicURL, trustedProxies, next)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if len(r.Header.Values("Origin")) > 0 && !sameOrigin(r, publicURL, currentTrustedProxies(trustedProxies)) {
			writeAPIError(w, r, APIError{Status: http.StatusForbidden, Code: "origin_rejected", Message: "Request origin is not allowed"})
			return
		}
		handler.ServeHTTP(w, r)
	})
}

func originMutationMiddleware(publicURL *url.URL, trustedProxies func() []netip.Prefix, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if isAdminMutation(r.Method) && !sameOrigin(r, publicURL, currentTrustedProxies(trustedProxies)) {
			writeAPIError(w, r, APIError{Status: http.StatusForbidden, Code: "origin_rejected", Message: "Request origin is not allowed"})
			return
		}
		next.ServeHTTP(w, r)
	})
}

func authenticateAdminMiddleware(auth AdminAuth, publicURL *url.URL, trustedProxies func() []netip.Prefix, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		cookieName := adminCookieNameForRequest(r, publicURL, currentTrustedProxies(trustedProxies))
		cookie, err := r.Cookie(cookieName)
		if err != nil && cookieName == adminDevelopmentCookie {
			cookie, err = r.Cookie(adminCookieName)
		}
		if err != nil || !validAdminSessionToken(cookie.Value) || auth == nil {
			writeAPIError(w, r, APIError{Status: http.StatusUnauthorized, Code: "authentication_required", Message: "Administrator authentication is required"})
			return
		}
		authenticated, err := auth.Authenticate(r.Context(), cookie.Value)
		if err != nil {
			if errors.Is(err, adminauth.ErrRateLimited) {
				w.Header().Set("Retry-After", authenticationRetryAfter)
				writeAPIError(w, r, APIError{Status: http.StatusTooManyRequests, Code: "rate_limited", Message: "Too many authentication attempts"})
				return
			}
			if errors.Is(err, adminauth.ErrInvalidCredentials) {
				writeAPIError(w, r, APIError{Status: http.StatusUnauthorized, Code: "authentication_required", Message: "Administrator authentication is required"})
				return
			}
			if database.Classify(err) == database.KindUnavailable {
				w.Header().Set("Retry-After", authenticationRetryAfter)
				writeAPIError(w, r, APIError{Status: http.StatusServiceUnavailable, Code: "authentication_unavailable", Message: "Administrator authentication is unavailable"})
				return
			}
			writeAPIError(w, r, APIError{Status: http.StatusInternalServerError, Code: "internal_error", Message: "Internal server error"})
			return
		}
		if authorizer, ok := auth.(adminIPAuthorizer); ok {
			address, valid := clientIP(r, currentTrustedProxies(trustedProxies))
			if !valid || authorizer.AuthorizeIP(r.Context(), authenticated.Administrator.ID, address) != nil {
				writeAPIError(w, r, APIError{Status: http.StatusUnauthorized, Code: "authentication_required", Message: "Administrator authentication is required"})
				return
			}
		}
		principal := AdminPrincipal{
			Administrator: authenticated.Administrator, RawSession: cookie.Value,
			ExpiresAt: authenticated.ExpiresAt, CSRFToken: authenticated.CSRFToken,
		}
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), adminPrincipalKey{}, principal)))
	})
}

func validAdminSessionToken(value string) bool {
	encoding := base64.RawURLEncoding.Strict()
	if len(value) != encoding.EncodedLen(adminSessionTokenBytes) {
		return false
	}
	decoded, err := encoding.DecodeString(value)
	return err == nil && len(decoded) == adminSessionTokenBytes
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

func adminCookieNameForRequest(r *http.Request, publicURL *url.URL, trustedProxies []netip.Prefix) string {
	if secureAdminCookie(r, publicURL, trustedProxies) {
		return adminCookieName
	}
	return adminDevelopmentCookie
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
