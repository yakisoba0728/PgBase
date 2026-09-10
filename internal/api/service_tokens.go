package api

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/google/uuid"
	"github.com/yakisoba0728/PgBase/internal/serviceauth"
)

type ServiceTokens interface {
	Authenticate(context.Context, string) (serviceauth.Principal, error)
	Issue(context.Context, string, time.Duration, []serviceauth.Scope) (serviceauth.IssuedToken, error)
	List(context.Context) ([]serviceauth.Token, error)
	Revoke(context.Context, uuid.UUID) error
}

func serviceTokenRoutes(dependencies Dependencies) []route {
	if dependencies.ServiceTokens == nil {
		return nil
	}
	e := serviceTokenEndpoints{dependencies.ServiceTokens}
	return []route{{method: "GET", pattern: "/api/admin/service-tokens", access: accessAdminSameOriginRead, response: atomicJSON(maximumCatalogResponseBytes), deadline: deadlineDefault, handler: http.HandlerFunc(e.list)}, {method: "POST", pattern: "/api/admin/service-tokens", access: accessAdminSession, response: atomicJSON(maximumAdminResponseBytes), deadline: deadlineDefault, maximumRequestBytes: maximumCredentialRequestBytes, handler: http.HandlerFunc(e.issue)}, {method: "POST", pattern: "/api/admin/service-tokens/{id}/revoke", access: accessAdminSession, response: atomicJSON(maximumAdminResponseBytes), deadline: deadlineDefault, handler: http.HandlerFunc(e.revoke)}}
}

type serviceTokenEndpoints struct{ service ServiceTokens }
type serviceTokenResponse struct {
	ID        string              `json:"id"`
	Name      string              `json:"name"`
	Active    bool                `json:"active"`
	ExpiresAt time.Time           `json:"expiresAt"`
	RevokedAt *time.Time          `json:"revokedAt,omitempty"`
	CreatedAt time.Time           `json:"createdAt"`
	Scopes    []serviceauth.Scope `json:"scopes"`
}

func tokenResponse(v serviceauth.Token) serviceTokenResponse {
	return serviceTokenResponse{v.ID.String(), v.Name, v.Active, v.ExpiresAt, v.RevokedAt, v.CreatedAt, v.Scopes}
}
func (e serviceTokenEndpoints) issue(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Name       string              `json:"name"`
		TTLSeconds int64               `json:"ttlSeconds"`
		Scopes     []serviceauth.Scope `json:"scopes"`
	}
	if decodeStrictJSON(r, &in) != nil {
		writeServiceTokenError(w, r, serviceauth.ErrValidation)
		return
	}
	if in.TTLSeconds < 1 || in.TTLSeconds > 365*24*60*60 {
		writeServiceTokenError(w, r, serviceauth.ErrValidation)
		return
	}
	v, err := e.service.Issue(r.Context(), in.Name, time.Duration(in.TTLSeconds)*time.Second, in.Scopes)
	if err != nil {
		writeServiceTokenError(w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, struct {
		serviceTokenResponse
		Token string `json:"token"`
	}{tokenResponse(v.Token), v.RawToken})
}
func (e serviceTokenEndpoints) list(w http.ResponseWriter, r *http.Request) {
	values, err := e.service.List(r.Context())
	if err != nil {
		writeServiceTokenError(w, r, err)
		return
	}
	items := make([]serviceTokenResponse, len(values))
	for i := range values {
		items[i] = tokenResponse(values[i])
	}
	writeJSON(w, http.StatusOK, struct {
		Items []serviceTokenResponse `json:"items"`
	}{items})
}
func (e serviceTokenEndpoints) revoke(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		writeServiceTokenError(w, r, serviceauth.ErrValidation)
		return
	}
	if err := e.service.Revoke(r.Context(), id); err != nil {
		writeServiceTokenError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
func writeServiceTokenError(w http.ResponseWriter, r *http.Request, err error) {
	response := APIError{Status: 503, Code: "service_tokens_unavailable", Message: "Service tokens are unavailable"}
	if errors.Is(err, serviceauth.ErrValidation) {
		response = APIError{Status: 400, Code: "invalid_request", Message: "Invalid service token request"}
	} else if errors.Is(err, serviceauth.ErrNotFound) {
		response = APIError{Status: 404, Code: "not_found", Message: "Service token not found"}
	}
	writeAPIError(w, r, response)
}
