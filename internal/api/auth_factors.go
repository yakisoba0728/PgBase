package api

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"time"

	"github.com/google/uuid"
	"github.com/yakisoba0728/PgBase/internal/userauth"
)

type AuthFactors interface {
	BeginTOTPEnrollment(context.Context, uuid.UUID, string) (userauth.TOTPEnrollment, error)
	ConfirmTOTPEnrollment(context.Context, uuid.UUID, string) (userauth.RecoveryCodes, error)
	DisableTOTP(context.Context, uuid.UUID, string, string) error
	CompleteMFA(context.Context, string, string) (userauth.Session, error)
	BeginOAuth(context.Context, string, string, string) (userauth.OAuthAuthorization, error)
	BeginOAuthWithProviderRedirect(context.Context, string, string, string, string) (userauth.OAuthAuthorization, error)
	BeginOAuthLink(context.Context, uuid.UUID, string, string, string) (userauth.OAuthAuthorization, error)
	BeginOAuthLinkWithProviderRedirect(context.Context, uuid.UUID, string, string, string, string) (userauth.OAuthAuthorization, error)
	CompleteOAuth(context.Context, string, string, string) (userauth.Session, error)
	AcceptOAuthCallback(context.Context, string, string, string, string, string) (string, error)
	RequestEmailOTP(context.Context, string, string) (userauth.EmailOTPRequest, error)
	CompleteEmailOTP(context.Context, string, string) (userauth.Session, error)
	GetOAuthAvatar(context.Context, uuid.UUID) (userauth.OAuthAvatar, error)
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
		{"otp/request", accessPublic}, {"otp/complete", accessPublic},
	}
	routes := make([]route, 0, len(actions))
	for _, action := range actions {
		routes = append(routes, route{method: "POST", pattern: "/api/v1/auth/" + action.name, access: action.access, response: atomicJSON(maximumAdminResponseBytes), deadline: deadlineDefault, maximumRequestBytes: maximumCredentialRequestBytes, handler: e.factorAction(action.name)})
	}
	routes = append(routes, route{method: http.MethodGet, pattern: "/api/v1/auth/avatar", access: accessUserSession, response: passthrough(), deadline: deadlineDefault, handler: http.HandlerFunc(e.oauthAvatar)})
	routes = append(routes, route{method: http.MethodPost, pattern: "/api/v1/auth/oauth/apple/callback", access: accessPublic, response: passthrough(), deadline: deadlineDefault, maximumRequestBytes: maximumCredentialRequestBytes, handler: http.HandlerFunc(e.appleOAuthCallback)})
	return routes
}

func (e userEndpoints) appleOAuthCallback(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	if r.Header.Get("Content-Type") != "application/x-www-form-urlencoded" || r.ParseForm() != nil || len(r.PostForm) == 0 {
		http.Error(w, "invalid OAuth callback", http.StatusBadRequest)
		return
	}
	allowed := map[string]bool{"code": true, "state": true, "error": true, "error_description": true, "user": true}
	for key, values := range r.PostForm {
		if !allowed[key] || len(values) != 1 {
			http.Error(w, "invalid OAuth callback", http.StatusBadRequest)
			return
		}
	}
	destination, err := e.dependencies.AuthFactors.AcceptOAuthCallback(r.Context(), "apple", r.PostForm.Get("state"), r.PostForm.Get("code"), r.PostForm.Get("error"), r.PostForm.Get("error_description"))
	if err != nil {
		http.Error(w, "invalid OAuth callback", http.StatusBadRequest)
		return
	}
	http.Redirect(w, r, destination, http.StatusSeeOther)
}

func (e userEndpoints) oauthAvatar(w http.ResponseWriter, r *http.Request) {
	current, _ := r.Context().Value(userPrincipalKey{}).(authenticatedUser)
	avatar, err := e.dependencies.AuthFactors.GetOAuthAvatar(r.Context(), current.principal.User.ID)
	if err != nil {
		writeUserAuthError(w, r, err)
		return
	}
	w.Header().Set("Content-Type", avatar.MediaType)
	w.Header().Set("Cache-Control", "private, max-age=300")
	w.Header().Set("ETag", `"`+fmt.Sprintf("%x", avatar.SHA256)+`"`)
	w.Header().Set("Content-Length", strconv.Itoa(len(avatar.Content)))
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(avatar.Content)
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
			Email           string `json:"email"`
			OTPID           string `json:"otpId"`
		}
		if decodeStrictJSON(r, &input) != nil {
			writeUserAuthError(w, r, userauth.ErrValidation)
			return
		}
		current, _ := r.Context().Value(userPrincipalKey{}).(authenticatedUser)
		if current.principal.Impersonated && (action == "totp/enroll" || action == "totp/confirm" || action == "totp/disable" || action == "oauth/link") {
			writeUserAuthError(w, r, userauth.ErrInvalidCredentials)
			return
		}
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
		case "otp/request":
			address, ok := clientAddress(r, e.dependencies.trustedProxies())
			if !ok {
				err = userauth.ErrValidation
				break
			}
			var value userauth.EmailOTPRequest
			value, err = service.RequestEmailOTP(r.Context(), input.Email, address)
			result = value
		case "otp/complete":
			var value userauth.Session
			value, err = service.CompleteEmailOTP(r.Context(), input.OTPID, input.Code)
			result = userSessionResponse{Token: value.RawToken, User: publicUser(value.User), ExpiresAt: value.ExpiresAt}
		case "oauth/start", "oauth/link":
			var value userauth.OAuthAuthorization
			if action == "oauth/start" {
				address, ok := clientAddress(r, e.dependencies.trustedProxies())
				if !ok {
					writeUserAuthError(w, r, userauth.ErrValidation)
					return
				}
				if input.Provider == "apple" && e.dependencies.PublicURL != nil {
					bridge := e.dependencies.PublicURL.ResolveReference(&url.URL{Path: "/api/v1/auth/oauth/apple/callback"}).String()
					value, err = service.BeginOAuthWithProviderRedirect(r.Context(), input.Provider, input.RedirectURI, bridge, address)
				} else {
					value, err = service.BeginOAuth(r.Context(), input.Provider, input.RedirectURI, address)
				}
			} else {
				if input.Provider == "apple" && e.dependencies.PublicURL != nil {
					bridge := e.dependencies.PublicURL.ResolveReference(&url.URL{Path: "/api/v1/auth/oauth/apple/callback"}).String()
					value, err = service.BeginOAuthLinkWithProviderRedirect(r.Context(), current.principal.User.ID, input.CurrentPassword, input.Provider, input.RedirectURI, bridge)
				} else {
					value, err = service.BeginOAuthLink(r.Context(), current.principal.User.ID, input.CurrentPassword, input.Provider, input.RedirectURI)
				}
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
