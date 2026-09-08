package api

import (
	"errors"
	"net/http"
	"net/url"
	"strconv"
	"time"

	"github.com/google/uuid"
	"github.com/webisnoteasy/PgBase/internal/userauth"
)

func (e userEndpoints) lifecycleRoutes() []route {
	return []route{
		{method: "GET", pattern: "/api/admin/users", access: accessAdminSameOriginRead, response: atomicJSON(maximumAdminResponseBytes), deadline: deadlineDefault, handler: http.HandlerFunc(e.listUsers)},
		{method: "PATCH", pattern: "/api/admin/users/{id}", access: accessAdminSession, response: atomicJSON(maximumAdminResponseBytes), deadline: deadlineDefault, maximumRequestBytes: maximumCredentialRequestBytes, handler: http.HandlerFunc(e.setUserActive)},
		{method: "POST", pattern: "/api/admin/users/{id}/revoke-sessions", access: accessAdminSession, response: atomicJSON(maximumAdminResponseBytes), deadline: deadlineDefault, handler: http.HandlerFunc(e.revokeUserSessions)},
		{method: "POST", pattern: "/api/v1/auth/change-password", access: accessUserSession, response: atomicJSON(maximumAdminResponseBytes), deadline: deadlineDefault, maximumRequestBytes: maximumCredentialRequestBytes, handler: http.HandlerFunc(e.changePassword)},
	}
}

// The transport enumerates fields rather than serializing a persistence model,
// keeping future credential/session fields outside the administrator response.
type managedUserResponse struct {
	Verified  bool      `json:"verified"`
	ID        string    `json:"id"`
	Email     string    `json:"email"`
	Active    bool      `json:"active"`
	CreatedAt time.Time `json:"createdAt"`
	UpdatedAt time.Time `json:"updatedAt"`
}

func managedUser(user userauth.ManagedUser) managedUserResponse {
	return managedUserResponse{ID: user.ID.String(), Email: user.Email, Verified: user.Verified, Active: user.Active, CreatedAt: user.CreatedAt, UpdatedAt: user.UpdatedAt}
}

func (e userEndpoints) listUsers(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	after, limit, err := userPageParameters(r.URL.RawQuery)
	if err != nil {
		writeUserManagementError(w, r, err)
		return
	}
	page, err := e.dependencies.UserAuth.ListUsers(r.Context(), after, limit)
	if err != nil {
		writeUserManagementError(w, r, err)
		return
	}
	response := struct {
		Items     []managedUserResponse `json:"items"`
		NextAfter string                `json:"nextAfter,omitempty"`
	}{Items: make([]managedUserResponse, 0, len(page.Items))}
	for _, user := range page.Items {
		response.Items = append(response.Items, managedUser(user))
	}
	if page.NextAfter != uuid.Nil {
		response.NextAfter = page.NextAfter.String()
	}
	writeJSON(w, http.StatusOK, response)
}

func userPageParameters(rawQuery string) (uuid.UUID, int, error) {
	values, err := url.ParseQuery(rawQuery)
	if err != nil {
		return uuid.Nil, 0, userauth.ErrValidation
	}
	after, limit := uuid.Nil, 50
	for key, entries := range values {
		if len(entries) != 1 || entries[0] == "" {
			return uuid.Nil, 0, userauth.ErrValidation
		}
		switch key {
		case "after":
			after, err = uuid.Parse(entries[0])
			if err != nil || after == uuid.Nil {
				return uuid.Nil, 0, userauth.ErrValidation
			}
		case "limit":
			limit, err = strconv.Atoi(entries[0])
			if err != nil || limit < 1 || limit > 100 {
				return uuid.Nil, 0, userauth.ErrValidation
			}
		default:
			return uuid.Nil, 0, userauth.ErrValidation
		}
	}
	return after, limit, nil
}

func (e userEndpoints) setUserActive(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	id, err := uuid.Parse(r.PathValue("id"))
	var input struct {
		Active *bool `json:"active"`
	}
	if err != nil || id == uuid.Nil || decodeStrictJSON(r, &input) != nil || input.Active == nil {
		writeUserManagementError(w, r, userauth.ErrValidation)
		return
	}
	user, err := e.dependencies.UserAuth.SetUserActive(r.Context(), id, *input.Active)
	if err != nil {
		writeUserManagementError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, managedUser(user))
}

func (e userEndpoints) revokeUserSessions(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil || id == uuid.Nil {
		writeUserManagementError(w, r, userauth.ErrValidation)
		return
	}
	revoked, err := e.dependencies.UserAuth.RevokeUserSessions(r.Context(), id)
	if err != nil {
		writeUserManagementError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, struct {
		RevokedSessions int64 `json:"revokedSessions"`
	}{revoked})
}

func (e userEndpoints) changePassword(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	current, ok := r.Context().Value(userPrincipalKey{}).(authenticatedUser)
	if !ok {
		writeUserAuthError(w, r, userauth.ErrInvalidCredentials)
		return
	}
	var input struct {
		CurrentPassword string `json:"currentPassword"`
		NewPassword     string `json:"newPassword"`
	}
	if decodeStrictJSON(r, &input) != nil || input.CurrentPassword == "" || input.NewPassword == "" {
		writeUserAuthError(w, r, userauth.ErrValidation)
		return
	}
	if err := e.dependencies.UserAuth.ChangePassword(r.Context(), current.principal.User.ID, input.CurrentPassword, input.NewPassword); err != nil {
		writeUserAuthError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func writeUserManagementError(w http.ResponseWriter, r *http.Request, err error) {
	w.Header().Set("Cache-Control", "no-store")
	response := APIError{Status: http.StatusServiceUnavailable, Code: "user_management_unavailable", Message: "User management is unavailable"}
	switch {
	case errors.Is(err, userauth.ErrValidation):
		response = APIError{Status: http.StatusBadRequest, Code: "invalid_request", Message: "Invalid user management request"}
	case errors.Is(err, userauth.ErrNotFound):
		response = APIError{Status: http.StatusNotFound, Code: "not_found", Message: "User not found"}
	}
	writeAPIError(w, r, response)
}
