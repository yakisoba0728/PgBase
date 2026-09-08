package api

import (
	"context"
	"net/http"
	"time"

	"github.com/google/uuid"
	"github.com/webisnoteasy/PgBase/internal/userauth"
)

type AuthFactors interface {
	BeginTOTPEnrollment(context.Context, uuid.UUID, string) (userauth.TOTPEnrollment, error)
	ConfirmTOTPEnrollment(context.Context, uuid.UUID, string) (userauth.RecoveryCodes, error)
	DisableTOTP(context.Context, uuid.UUID, string, string) error
	CompleteMFA(context.Context, string, string) (userauth.Session, error)
	BeginOAuth(context.Context, string, string, string) (userauth.OAuthAuthorization, error)
	BeginOAuthLink(context.Context, uuid.UUID, string, string, string) (userauth.OAuthAuthorization, error)
	CompleteOAuth(context.Context, string, string, string) (userauth.Session, error)
}

func (e userEndpoints) factorRoutes() []route {
	if e.dependencies.AuthFactors == nil {
		return nil
	}
	actions := []struct {
		name   string
		access accessPolicy
	}{
		{"totp/enroll", accessUserSession}, {"totp/confirm", accessUserSession}, {"totp/disable", accessUserSession},
		{"mfa/complete", accessPublic}, {"oauth/start", accessPublic}, {"oauth/link", accessUserSession}, {"oauth/complete", accessPublic},
	}
	routes := make([]route, 0, len(actions))
	for _, action := range actions {
		routes = append(routes, route{method: "POST", pattern: "/api/v1/auth/" + action.name, access: action.access, response: atomicJSON(maximumAdminResponseBytes), deadline: deadlineDefault, maximumRequestBytes: maximumCredentialRequestBytes, handler: e.factorAction(action.name)})
	}
	return routes
}

func (e userEndpoints) factorAction(action string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		var input struct {
			CurrentPassword string `json:"currentPassword"`
			Code            string `json:"code"`
			ChallengeToken  string `json:"challengeToken"`
			Provider        string `json:"provider"`
			RedirectURI     string `json:"redirectUri"`
			State           string `json:"state"`
		}
		if decodeStrictJSON(r, &input) != nil {
			writeUserAuthError(w, r, userauth.ErrValidation)
			return
		}
		current, _ := r.Context().Value(userPrincipalKey{}).(authenticatedUser)
		service := e.dependencies.AuthFactors
		var result any
		var err error
		switch action {
		case "totp/enroll":
			var value userauth.TOTPEnrollment
			value, err = service.BeginTOTPEnrollment(r.Context(), current.principal.User.ID, input.CurrentPassword)
			result = struct {
				Secret     string `json:"secret"`
				OTPAuthURL string `json:"otpAuthUrl"`
			}{value.Secret, value.OTPAuthURL}
		case "totp/confirm":
			var value userauth.RecoveryCodes
			value, err = service.ConfirmTOTPEnrollment(r.Context(), current.principal.User.ID, input.Code)
			result = struct {
				Codes []string `json:"codes"`
			}{value.Codes}
		case "totp/disable":
			err = service.DisableTOTP(r.Context(), current.principal.User.ID, input.CurrentPassword, input.Code)
		case "mfa/complete", "oauth/complete":
			var value userauth.Session
			if action == "mfa/complete" {
				value, err = service.CompleteMFA(r.Context(), input.ChallengeToken, input.Code)
			} else {
				value, err = service.CompleteOAuth(r.Context(), input.Provider, input.State, input.Code)
			}
			result = userSessionResponse{Token: value.RawToken, User: publicUser(value.User), ExpiresAt: value.ExpiresAt}
		case "oauth/start", "oauth/link":
			var value userauth.OAuthAuthorization
			if action == "oauth/start" {
				address, ok := clientAddress(r, e.dependencies.TrustedProxies)
				if !ok {
					writeUserAuthError(w, r, userauth.ErrValidation)
					return
				}
				value, err = service.BeginOAuth(r.Context(), input.Provider, input.RedirectURI, address)
			} else {
				value, err = service.BeginOAuthLink(r.Context(), current.principal.User.ID, input.CurrentPassword, input.Provider, input.RedirectURI)
			}
			result = struct {
				AuthorizationURL string    `json:"authorizationUrl"`
				ExpiresAt        time.Time `json:"expiresAt"`
			}{value.AuthorizationURL, value.ExpiresAt}
		}
		if err != nil {
			writeUserAuthError(w, r, err)
			return
		}
		if result == nil {
			w.WriteHeader(http.StatusNoContent)
		} else {
			writeJSON(w, http.StatusOK, result)
		}
	})
}
