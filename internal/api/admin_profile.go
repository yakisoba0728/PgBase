package api

import (
	"context"
	"errors"
	"net/http"
	"net/netip"

	"github.com/google/uuid"
	"github.com/yakisoba0728/PgBase/internal/adminauth"
	"github.com/yakisoba0728/PgBase/internal/database"
)

type adminSelfAdministration interface {
	Administrator(context.Context, uuid.UUID) (adminauth.Administrator, error)
	UpdateAdministratorCredentials(context.Context, uuid.UUID, string, string, string, string) (adminauth.Administrator, error)
	AdministratorIPPrefixes(context.Context, uuid.UUID) ([]string, error)
	ReplaceAdministratorIPPrefixes(context.Context, uuid.UUID, string, []string) ([]string, error)
}

type adminProfileEndpoints struct {
	service        adminSelfAdministration
	trustedProxies func() []netip.Prefix
}

func adminProfileRoutes(dependencies Dependencies) []route {
	service, ok := dependencies.AdminAuth.(adminSelfAdministration)
	if !ok {
		return nil
	}
	h := adminProfileEndpoints{service: service, trustedProxies: dependencies.trustedProxies}
	return []route{
		{method: http.MethodGet, pattern: "/api/admin/profile", access: accessAdminSameOriginRead, response: atomicJSON(maximumAdminResponseBytes), deadline: deadlineDefault, handler: http.HandlerFunc(h.profile)},
		{method: http.MethodPatch, pattern: "/api/admin/profile", access: accessAdminSession, response: atomicJSON(maximumAdminResponseBytes), deadline: deadlineDefault, maximumRequestBytes: maximumCredentialRequestBytes, handler: http.HandlerFunc(h.profile)},
		{method: http.MethodGet, pattern: "/api/admin/profile/ip-allowlist", access: accessAdminSameOriginRead, response: atomicJSON(maximumAdminResponseBytes), deadline: deadlineDefault, handler: http.HandlerFunc(h.ipAllowlist)},
		{method: http.MethodPatch, pattern: "/api/admin/profile/ip-allowlist", access: accessAdminSession, response: atomicJSON(maximumAdminResponseBytes), deadline: deadlineDefault, handler: http.HandlerFunc(h.ipAllowlist)},
	}
}

func (h adminProfileEndpoints) profile(w http.ResponseWriter, r *http.Request) {
	principal, ok := AdminPrincipalFromContext(r.Context())
	if !ok {
		writeAdminProfileError(w, r, adminauth.ErrInvalidCredentials)
		return
	}
	var value adminauth.Administrator
	var err error
	if r.Method == http.MethodGet {
		value, err = h.service.Administrator(r.Context(), principal.Administrator.ID)
	} else {
		var input struct {
			CurrentPassword string `json:"currentPassword"`
			Email           string `json:"email"`
			NewPassword     string `json:"newPassword"`
		}
		address, valid := clientIP(r, currentTrustedProxies(h.trustedProxies))
		if decodeStrictJSON(r, &input) != nil || !valid {
			writeAdminProfileError(w, r, adminauth.ErrValidation)
			return
		}
		value, err = h.service.UpdateAdministratorCredentials(r.Context(), principal.Administrator.ID, address, input.CurrentPassword, input.Email, input.NewPassword)
		if errors.Is(err, adminauth.ErrInvalidCredentials) {
			// Not 401: the session is still valid, only the re-verification failed.
			writeAPIError(w, r, APIError{Status: http.StatusForbidden, Code: "current_password_invalid", Message: "The current password is missing or incorrect"})
			return
		}
	}
	if err != nil {
		writeAdminProfileError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, administratorResponse{ID: value.ID, Email: value.Email, Active: value.Active, CreatedAt: value.CreatedAt.UTC(), UpdatedAt: value.UpdatedAt.UTC()})
}

func (h adminProfileEndpoints) ipAllowlist(w http.ResponseWriter, r *http.Request) {
	principal, ok := AdminPrincipalFromContext(r.Context())
	if !ok {
		writeAdminProfileError(w, r, adminauth.ErrInvalidCredentials)
		return
	}
	// The same resolution the administrator session middleware checks the list against.
	address, _ := clientIP(r, currentTrustedProxies(h.trustedProxies))
	var prefixes []string
	var err error
	if r.Method == http.MethodGet {
		prefixes, err = h.service.AdministratorIPPrefixes(r.Context(), principal.Administrator.ID)
	} else {
		var input struct {
			Prefixes []string `json:"prefixes"`
		}
		if decodeStrictJSON(r, &input) != nil || input.Prefixes == nil {
			writeAdminProfileError(w, r, adminauth.ErrValidation)
			return
		}
		prefixes, err = h.service.ReplaceAdministratorIPPrefixes(r.Context(), principal.Administrator.ID, address, input.Prefixes)
	}
	if err != nil {
		writeAdminProfileError(w, r, err)
		return
	}
	if prefixes == nil {
		prefixes = []string{}
	}
	writeJSON(w, http.StatusOK, struct {
		Prefixes      []string `json:"prefixes"`
		ClientAddress string   `json:"clientAddress"`
	}{Prefixes: prefixes, ClientAddress: address})
}

func writeAdminProfileError(w http.ResponseWriter, r *http.Request, err error) {
	value := APIError{Status: http.StatusInternalServerError, Code: "internal_error", Message: "Internal server error"}
	switch {
	case errors.Is(err, adminauth.ErrValidation):
		value = APIError{Status: 400, Code: "invalid_profile", Message: "Invalid administrator profile"}
	case errors.Is(err, adminauth.ErrConflict):
		value = APIError{Status: 409, Code: "profile_conflict", Message: "Administrator email is already in use"}
	case errors.Is(err, adminauth.ErrIPRestricted):
		value = APIError{Status: 409, Code: "ip_allowlist_excludes_caller", Message: "The IP allowlist must include the address of this request; use the CLI to override"}
	case errors.Is(err, adminauth.ErrInvalidCredentials):
		value = APIError{Status: 401, Code: "authentication_required", Message: "Administrator authentication is required"}
	case errors.Is(err, adminauth.ErrRateLimited):
		w.Header().Set("Retry-After", authenticationRetryAfter)
		value = APIError{Status: http.StatusTooManyRequests, Code: "rate_limited", Message: "Too many authentication attempts"}
	case database.Classify(err) == database.KindUnavailable:
		value = APIError{Status: 503, Code: "profile_unavailable", Message: "Administrator profile is unavailable"}
	}
	writeAPIError(w, r, value)
}
