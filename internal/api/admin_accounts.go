package api

import (
	"context"
	"errors"
	"net/http"
	"strconv"

	"github.com/google/uuid"
	"github.com/yakisoba0728/PgBase/internal/adminauth"
	"github.com/yakisoba0728/PgBase/internal/database"
)

type administratorAccounts interface {
	ListAdministratorAccounts(context.Context, uuid.UUID, uuid.UUID, int) ([]adminauth.Administrator, uuid.UUID, error)
	CreateAdministratorAccount(context.Context, uuid.UUID, string, string) (adminauth.Administrator, error)
	SetAdministratorActive(context.Context, uuid.UUID, uuid.UUID, bool) (adminauth.Administrator, error)
	SetAdministratorPassword(context.Context, uuid.UUID, uuid.UUID, string) error
	RevokeAdministratorSessions(context.Context, uuid.UUID, uuid.UUID) error
}

func adminAccountRoutes(dependencies Dependencies) []route {
	service, ok := dependencies.AdminAuth.(administratorAccounts)
	if !ok {
		return nil
	}
	h := adminAccountEndpoints{service}
	return []route{
		{method: http.MethodGet, pattern: "/api/admin/administrators", access: accessAdminSameOriginRead, response: atomicJSON(maximumAdminResponseBytes), deadline: deadlineDefault, handler: http.HandlerFunc(h.list)},
		{method: http.MethodPost, pattern: "/api/admin/administrators", access: accessAdminSession, response: atomicJSON(maximumAdminResponseBytes), deadline: deadlineDefault, handler: http.HandlerFunc(h.create)},
		{method: http.MethodPatch, pattern: "/api/admin/administrators/{id}", access: accessAdminSession, response: atomicJSON(maximumAdminResponseBytes), deadline: deadlineDefault, handler: http.HandlerFunc(h.update)},
		{method: http.MethodPost, pattern: "/api/admin/administrators/{id}/password-reset", access: accessAdminSession, response: atomicJSON(maximumAdminResponseBytes), deadline: deadlineDefault, handler: http.HandlerFunc(h.password)},
		{method: http.MethodPost, pattern: "/api/admin/administrators/{id}/revoke-sessions", access: accessAdminSession, response: atomicJSON(maximumAdminResponseBytes), deadline: deadlineDefault, handler: http.HandlerFunc(h.revoke)},
	}
}

type adminAccountEndpoints struct{ service administratorAccounts }

func (h adminAccountEndpoints) list(w http.ResponseWriter, r *http.Request) {
	principal, _ := AdminPrincipalFromContext(r.Context())
	query := r.URL.Query()
	limit, after := 50, uuid.Nil
	var err error
	for key, values := range query {
		if len(values) != 1 || (key != "limit" && key != "after") {
			writeAdminAccountError(w, r, adminauth.ErrValidation)
			return
		}
	}
	if query.Has("limit") {
		limit, err = strconv.Atoi(query.Get("limit"))
	}
	if err == nil && query.Has("after") {
		after, err = uuid.Parse(query.Get("after"))
	}
	if err != nil || limit < 1 || limit > 100 {
		writeAdminAccountError(w, r, adminauth.ErrValidation)
		return
	}
	items, next, err := h.service.ListAdministratorAccounts(r.Context(), principal.Administrator.ID, after, limit)
	if err != nil {
		writeAdminAccountError(w, r, err)
		return
	}
	response := struct {
		Items     []administratorResponse `json:"items"`
		NextAfter string                  `json:"nextAfter,omitempty"`
	}{Items: make([]administratorResponse, 0, len(items))}
	for _, item := range items {
		response.Items = append(response.Items, safeAdministratorAccount(item))
	}
	if next != uuid.Nil {
		response.NextAfter = next.String()
	}
	writeJSON(w, http.StatusOK, response)
}

func (h adminAccountEndpoints) create(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Email    string `json:"email"`
		Password string `json:"password"`
	}
	if decodeStrictJSON(r, &input) != nil {
		writeAdminAccountError(w, r, adminauth.ErrValidation)
		return
	}
	principal, _ := AdminPrincipalFromContext(r.Context())
	value, err := h.service.CreateAdministratorAccount(r.Context(), principal.Administrator.ID, input.Email, input.Password)
	if err != nil {
		writeAdminAccountError(w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, safeAdministratorAccount(value))
}

func (h adminAccountEndpoints) update(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Active *bool `json:"active"`
	}
	if decodeStrictJSON(r, &input) != nil || input.Active == nil {
		writeAdminAccountError(w, r, adminauth.ErrValidation)
		return
	}
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil || id == uuid.Nil {
		writeAdminAccountError(w, r, adminauth.ErrValidation)
		return
	}
	principal, _ := AdminPrincipalFromContext(r.Context())
	value, err := h.service.SetAdministratorActive(r.Context(), principal.Administrator.ID, id, *input.Active)
	if err != nil {
		writeAdminAccountError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, safeAdministratorAccount(value))
}

func (h adminAccountEndpoints) password(w http.ResponseWriter, r *http.Request) {
	var input struct {
		NewPassword string `json:"newPassword"`
	}
	if decodeStrictJSON(r, &input) != nil {
		writeAdminAccountError(w, r, adminauth.ErrValidation)
		return
	}
	h.mutate(w, r, func(actor, target uuid.UUID) error {
		return h.service.SetAdministratorPassword(r.Context(), actor, target, input.NewPassword)
	})
}

func (h adminAccountEndpoints) revoke(w http.ResponseWriter, r *http.Request) {
	var input struct{}
	if decodeStrictJSON(r, &input) != nil {
		writeAdminAccountError(w, r, adminauth.ErrValidation)
		return
	}
	h.mutate(w, r, func(actor, target uuid.UUID) error {
		return h.service.RevokeAdministratorSessions(r.Context(), actor, target)
	})
}

func (h adminAccountEndpoints) mutate(w http.ResponseWriter, r *http.Request, change func(uuid.UUID, uuid.UUID) error) {
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil || id == uuid.Nil {
		writeAdminAccountError(w, r, adminauth.ErrValidation)
		return
	}
	principal, _ := AdminPrincipalFromContext(r.Context())
	if err := change(principal.Administrator.ID, id); err != nil {
		writeAdminAccountError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func safeAdministratorAccount(value adminauth.Administrator) administratorResponse {
	return administratorResponse{ID: value.ID, Email: value.Email, Active: value.Active, CreatedAt: value.CreatedAt.UTC(), UpdatedAt: value.UpdatedAt.UTC()}
}

func writeAdminAccountError(w http.ResponseWriter, r *http.Request, err error) {
	value := APIError{Status: 500, Code: "internal_error", Message: "Internal server error"}
	switch {
	case errors.Is(err, adminauth.ErrValidation):
		value = APIError{Status: 400, Code: "invalid_administrator", Message: "Invalid administrator account request"}
	case errors.Is(err, adminauth.ErrAdministratorNotFound):
		value = APIError{Status: 404, Code: "administrator_not_found", Message: "Administrator account not found"}
	case errors.Is(err, adminauth.ErrConflict):
		value = APIError{Status: 409, Code: "administrator_conflict", Message: "Administrator email is in use or the change would disable the current or last active administrator"}
	case errors.Is(err, adminauth.ErrInvalidCredentials):
		value = APIError{Status: 401, Code: "authentication_required", Message: "Administrator authentication is required"}
	case errors.Is(err, adminauth.ErrRateLimited):
		value = APIError{Status: 429, Code: "rate_limited", Message: "Administrator credential work is busy"}
	case database.Classify(err) == database.KindUnavailable:
		value = APIError{Status: 503, Code: "administrator_unavailable", Message: "Administrator accounts are unavailable"}
	}
	writeAPIError(w, r, value)
}
