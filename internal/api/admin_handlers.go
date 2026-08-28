package api

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net/http"
	"net/netip"
	"net/url"
	"time"

	"github.com/google/uuid"
	"github.com/webisnoteasy/PgBase/internal/adminauth"
)

const adminCookieName = "pgbase_admin"

// AdminAuth supplies all administrator security state transitions used by HTTP.
type AdminAuth interface {
	Setup(context.Context, string, string, string, string) (adminauth.Session, error)
	Login(context.Context, string, string, string) (adminauth.Session, error)
	Authenticate(context.Context, string) (adminauth.Administrator, time.Time, error)
	Logout(context.Context, string) error
}

type adminEndpoints struct {
	auth           AdminAuth
	tokenSecret    []byte
	publicURL      *url.URL
	trustedProxies []netip.Prefix
}

type administratorResponse struct {
	ID        uuid.UUID `json:"id"`
	Email     string    `json:"email"`
	Active    bool      `json:"active"`
	CreatedAt time.Time `json:"createdAt"`
	UpdatedAt time.Time `json:"updatedAt"`
}

type adminSessionResponse struct {
	Administrator administratorResponse `json:"administrator"`
	CSRFToken     string                `json:"csrfToken"`
	ExpiresAt     time.Time             `json:"expiresAt"`
}

func newAdminEndpoints(dependencies Dependencies) adminEndpoints {
	return adminEndpoints{
		auth:           dependencies.AdminAuth,
		tokenSecret:    append([]byte(nil), dependencies.TokenSecret...),
		publicURL:      dependencies.PublicURL,
		trustedProxies: append([]netip.Prefix(nil), dependencies.TrustedProxies...),
	}
}

func (e adminEndpoints) setup(w http.ResponseWriter, r *http.Request) {
	var input struct {
		SetupToken string `json:"setupToken"`
		Email      string `json:"email"`
		Password   string `json:"password"`
	}
	if err := decodeStrictJSON(r, &input); err != nil {
		writeAPIError(w, r, APIError{Status: http.StatusBadRequest, Code: "invalid_request", Message: "Invalid request"})
		return
	}
	if e.auth == nil {
		writeAdminAuthError(w, r, errors.New("administrator authentication is unavailable"))
		return
	}
	session, err := e.auth.Setup(r.Context(), input.SetupToken, input.Email, input.Password, clientAddress(r, e.trustedProxies))
	if err != nil {
		writeAdminAuthError(w, r, err)
		return
	}
	e.writeIssuedSession(w, r, http.StatusCreated, session)
}

func (e adminEndpoints) login(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Email    string `json:"email"`
		Password string `json:"password"`
	}
	if err := decodeStrictJSON(r, &input); err != nil {
		writeAPIError(w, r, APIError{Status: http.StatusBadRequest, Code: "invalid_request", Message: "Invalid request"})
		return
	}
	if e.auth == nil {
		writeAdminAuthError(w, r, errors.New("administrator authentication is unavailable"))
		return
	}
	session, err := e.auth.Login(r.Context(), input.Email, input.Password, clientAddress(r, e.trustedProxies))
	if err != nil {
		writeAdminAuthError(w, r, err)
		return
	}
	e.writeIssuedSession(w, r, http.StatusOK, session)
}

func (e adminEndpoints) currentSession(w http.ResponseWriter, r *http.Request) {
	principal, ok := AdminPrincipalFromContext(r.Context())
	if !ok {
		writeAPIError(w, r, APIError{Status: http.StatusUnauthorized, Code: "authentication_required", Message: "Administrator authentication is required"})
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, sessionResponse(principal.Administrator, principal.ExpiresAt, e.tokenSecret, principal.RawSession))
}

func (e adminEndpoints) logout(w http.ResponseWriter, r *http.Request) {
	principal, ok := AdminPrincipalFromContext(r.Context())
	if !ok {
		writeAPIError(w, r, APIError{Status: http.StatusUnauthorized, Code: "authentication_required", Message: "Administrator authentication is required"})
		return
	}
	if err := e.auth.Logout(r.Context(), principal.RawSession); err != nil {
		writeAdminAuthError(w, r, err)
		return
	}
	http.SetCookie(w, &http.Cookie{
		Name:     adminCookieName,
		Value:    "",
		Path:     "/",
		Expires:  time.Unix(1, 0).UTC(),
		MaxAge:   -1,
		HttpOnly: true,
		Secure:   secureAdminCookie(r, e.publicURL, e.trustedProxies),
		SameSite: http.SameSiteLaxMode,
	})
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusNoContent)
}

func (e adminEndpoints) writeIssuedSession(w http.ResponseWriter, r *http.Request, status int, session adminauth.Session) {
	http.SetCookie(w, &http.Cookie{
		Name:     adminCookieName,
		Value:    session.RawToken,
		Path:     "/",
		Expires:  session.ExpiresAt.UTC(),
		HttpOnly: true,
		Secure:   secureAdminCookie(r, e.publicURL, e.trustedProxies),
		SameSite: http.SameSiteLaxMode,
	})
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, status, sessionResponse(session.Administrator, session.ExpiresAt, e.tokenSecret, session.RawToken))
}

func sessionResponse(administrator adminauth.Administrator, expiresAt time.Time, tokenSecret []byte, rawSession string) adminSessionResponse {
	return adminSessionResponse{
		Administrator: administratorResponse{
			ID:        administrator.ID,
			Email:     administrator.Email,
			Active:    administrator.Active,
			CreatedAt: administrator.CreatedAt.UTC(),
			UpdatedAt: administrator.UpdatedAt.UTC(),
		},
		CSRFToken: adminauth.DeriveCSRFToken(tokenSecret, rawSession),
		ExpiresAt: expiresAt.UTC(),
	}
}

func decodeStrictJSON(r *http.Request, destination any) error {
	mediaType, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || mediaType != "application/json" {
		return errors.New("request content type is not application/json")
	}
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(destination); err != nil {
		return err
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		if err == nil {
			return errors.New("request body contains trailing JSON")
		}
		return err
	}
	return nil
}

func writeAdminAuthError(w http.ResponseWriter, r *http.Request, err error) {
	apiError := APIError{Status: http.StatusInternalServerError, Code: "internal_error", Message: "Internal server error"}
	switch {
	case errors.Is(err, adminauth.ErrValidation):
		apiError = APIError{Status: http.StatusBadRequest, Code: "validation_failed", Message: "Authentication input is invalid"}
	case errors.Is(err, adminauth.ErrInvalidCredentials):
		apiError = APIError{Status: http.StatusUnauthorized, Code: "invalid_credentials", Message: "Invalid administrator credentials"}
	case errors.Is(err, adminauth.ErrSetupUnavailable):
		apiError = APIError{Status: http.StatusConflict, Code: "setup_unavailable", Message: "Administrator setup is unavailable"}
	case errors.Is(err, adminauth.ErrRateLimited):
		apiError = APIError{Status: http.StatusTooManyRequests, Code: "rate_limited", Message: "Too many authentication attempts"}
	}
	writeAPIError(w, r, apiError)
}
