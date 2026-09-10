package api

import (
	"context"
	"errors"
	"net/http"

	"github.com/google/uuid"
	"github.com/yakisoba0728/PgBase/internal/adminauth"
	"github.com/yakisoba0728/PgBase/internal/database"
)

type adminSelfAdministration interface {
	Administrator(context.Context, uuid.UUID) (adminauth.Administrator, error)
	UpdateAdministratorEmail(context.Context, uuid.UUID, string) (adminauth.Administrator, error)
	AdministratorIPPrefixes(context.Context, uuid.UUID) ([]string, error)
	ReplaceAdministratorIPPrefixes(context.Context, uuid.UUID, []string) ([]string, error)
}

type adminProfileEndpoints struct{ service adminSelfAdministration }

func adminProfileRoutes(dependencies Dependencies) []route {
	service, ok := dependencies.AdminAuth.(adminSelfAdministration)
	if !ok {
		return nil
	}
	h := adminProfileEndpoints{service: service}
	return []route{
		{method: http.MethodGet, pattern: "/api/admin/profile", access: accessAdminSameOriginRead, response: atomicJSON(maximumAdminResponseBytes), deadline: deadlineDefault, handler: http.HandlerFunc(h.profile)},
		{method: http.MethodPatch, pattern: "/api/admin/profile", access: accessAdminSession, response: atomicJSON(maximumAdminResponseBytes), deadline: deadlineDefault, handler: http.HandlerFunc(h.profile)},
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
			Email string `json:"email"`
		}
		if decodeStrictJSON(r, &input) != nil {
			writeAdminProfileError(w, r, adminauth.ErrValidation)
			return
		}
		value, err = h.service.UpdateAdministratorEmail(r.Context(), principal.Administrator.ID, input.Email)
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
		prefixes, err = h.service.ReplaceAdministratorIPPrefixes(r.Context(), principal.Administrator.ID, input.Prefixes)
	}
	if err != nil {
		writeAdminProfileError(w, r, err)
		return
	}
	if prefixes == nil {
		prefixes = []string{}
	}
	writeJSON(w, http.StatusOK, struct {
		Prefixes []string `json:"prefixes"`
	}{Prefixes: prefixes})
}

func writeAdminProfileError(w http.ResponseWriter, r *http.Request, err error) {
	value := APIError{Status: http.StatusInternalServerError, Code: "internal_error", Message: "Internal server error"}
	switch {
	case errors.Is(err, adminauth.ErrValidation):
		value = APIError{Status: 400, Code: "invalid_profile", Message: "Invalid administrator profile"}
	case errors.Is(err, adminauth.ErrConflict):
		value = APIError{Status: 409, Code: "profile_conflict", Message: "Administrator email is already in use"}
	case errors.Is(err, adminauth.ErrInvalidCredentials):
		value = APIError{Status: 401, Code: "authentication_required", Message: "Administrator authentication is required"}
	case database.Classify(err) == database.KindUnavailable:
		value = APIError{Status: 503, Code: "profile_unavailable", Message: "Administrator profile is unavailable"}
	}
	writeAPIError(w, r, value)
}
