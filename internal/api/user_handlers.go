package api

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/webisnoteasy/PgBase/internal/identity"
	"github.com/webisnoteasy/PgBase/internal/recordaccess"
	"github.com/webisnoteasy/PgBase/internal/userauth"
)

type UserAuth interface {
	Register(context.Context, string, string, string) (userauth.Session, error)
	Login(context.Context, string, string, string) (userauth.Session, error)
	Authenticate(context.Context, string) (userauth.Principal, error)
	Logout(context.Context, string) error
}

type RecordAccess interface {
	Get(context.Context, string) (recordaccess.Policy, error)
	Set(context.Context, string, bool) (recordaccess.Policy, error)
}

type userPrincipalKey struct{}
type authenticatedUser struct {
	principal userauth.Principal
	token     string
}

func userSessionMiddleware(auth UserAuth, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		values := r.Header.Values("Authorization")
		if len(values) != 1 || len(values[0]) > 256 {
			writeUserAuthError(w, r, userauth.ErrInvalidCredentials)
			return
		}
		parts := strings.Fields(values[0])
		if len(parts) != 2 || !strings.EqualFold(parts[0], "Bearer") {
			writeUserAuthError(w, r, userauth.ErrInvalidCredentials)
			return
		}
		if auth == nil {
			writeUserAuthError(w, r, errors.New("user authentication unavailable"))
			return
		}
		principal, err := auth.Authenticate(r.Context(), parts[1])
		if err != nil {
			writeUserAuthError(w, r, err)
			return
		}
		if !(identity.Subject{Kind: identity.User, ID: principal.User.ID}).Valid() {
			writeUserAuthError(w, r, userauth.ErrInvalidCredentials)
			return
		}
		ctx := context.WithValue(r.Context(), userPrincipalKey{}, authenticatedUser{principal: principal, token: parts[1]})
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

type userEndpoints struct{ dependencies Dependencies }

func (e userEndpoints) credentials(register bool) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		if register && !e.dependencies.UserRegistrationEnabled {
			writeAPIError(w, r, APIError{Status: http.StatusForbidden, Code: "registration_disabled", Message: "User registration is disabled"})
			return
		}
		var input struct {
			Email    string `json:"email"`
			Password string `json:"password"`
		}
		if err := decodeStrictJSON(r, &input); err != nil {
			writeUserAuthError(w, r, userauth.ErrValidation)
			return
		}
		address, ok := clientAddress(r, e.dependencies.TrustedProxies)
		if !ok {
			writeUserAuthError(w, r, userauth.ErrValidation)
			return
		}
		auth := e.dependencies.UserAuth
		if auth == nil {
			writeUserAuthError(w, r, errors.New("user authentication unavailable"))
			return
		}
		var session userauth.Session
		var err error
		status := http.StatusOK
		if register {
			session, err = auth.Register(r.Context(), input.Email, input.Password, address)
			status = http.StatusCreated
		} else {
			session, err = auth.Login(r.Context(), input.Email, input.Password, address)
		}
		if err != nil {
			writeUserAuthError(w, r, err)
			return
		}
		writeJSON(w, status, userSessionResponse{Token: session.RawToken, User: publicUser(session.User), ExpiresAt: session.ExpiresAt})
	})
}

type userResponse struct {
	ID        string    `json:"id"`
	Email     string    `json:"email"`
	CreatedAt time.Time `json:"createdAt"`
}

func publicUser(user userauth.User) userResponse {
	return userResponse{ID: user.ID.String(), Email: user.Email, CreatedAt: user.CreatedAt}
}

type userSessionResponse struct {
	Token     string       `json:"token,omitempty"`
	User      userResponse `json:"user"`
	ExpiresAt time.Time    `json:"expiresAt"`
}

func (e userEndpoints) session(w http.ResponseWriter, r *http.Request) {
	current, ok := r.Context().Value(userPrincipalKey{}).(authenticatedUser)
	if !ok {
		writeUserAuthError(w, r, userauth.ErrInvalidCredentials)
		return
	}
	writeJSON(w, http.StatusOK, userSessionResponse{User: publicUser(current.principal.User), ExpiresAt: current.principal.ExpiresAt})
}

func (e userEndpoints) logout(w http.ResponseWriter, r *http.Request) {
	current, ok := r.Context().Value(userPrincipalKey{}).(authenticatedUser)
	if !ok {
		writeUserAuthError(w, r, userauth.ErrInvalidCredentials)
		return
	}
	if err := e.dependencies.UserAuth.Logout(r.Context(), current.token); err != nil {
		writeUserAuthError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (e userEndpoints) access(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	service := e.dependencies.RecordAccess
	if service == nil {
		writeRecordError(w, r, errors.New("record access service unavailable"))
		return
	}
	var policy recordaccess.Policy
	var err error
	if r.Method == http.MethodPut {
		var input struct {
			Enabled *bool `json:"enabled"`
		}
		if decodeStrictJSON(r, &input) != nil || input.Enabled == nil {
			writeInvalidRecordRequest(w, r)
			return
		}
		policy, err = service.Set(r.Context(), r.PathValue("collection"), *input.Enabled)
	} else {
		policy, err = service.Get(r.Context(), r.PathValue("collection"))
	}
	if err != nil {
		writeRecordError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, struct {
		Collection string `json:"collection"`
		Enabled    bool   `json:"enabled"`
	}{policy.Collection, policy.Enabled})
}

func writeUserAuthError(w http.ResponseWriter, r *http.Request, err error) {
	w.Header().Set("Cache-Control", "no-store")
	response := APIError{Status: http.StatusServiceUnavailable, Code: "authentication_unavailable", Message: "Authentication is unavailable"}
	switch {
	case errors.Is(err, userauth.ErrValidation):
		response = APIError{Status: http.StatusBadRequest, Code: "invalid_request", Message: "Invalid authentication request"}
	case errors.Is(err, userauth.ErrInvalidCredentials):
		w.Header().Set("WWW-Authenticate", "Bearer")
		response = APIError{Status: http.StatusUnauthorized, Code: "invalid_credentials", Message: "Invalid credentials"}
	case errors.Is(err, userauth.ErrConflict):
		response = APIError{Status: http.StatusConflict, Code: "user_conflict", Message: "User cannot be registered"}
	case errors.Is(err, userauth.ErrRateLimited):
		w.Header().Set("Retry-After", "60")
		response = APIError{Status: http.StatusTooManyRequests, Code: "rate_limited", Message: "Try again later"}
	}
	writeAPIError(w, r, response)
}
