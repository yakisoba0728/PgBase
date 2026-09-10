package api

import (
	"context"
	"errors"
	"net/http"

	"github.com/google/uuid"
	"github.com/yakisoba0728/PgBase/internal/mailer"
	"github.com/yakisoba0728/PgBase/internal/userauth"
)

type AuthAdministration interface {
	GetAuthAdministration(context.Context, uuid.UUID) (userauth.AuthAdministration, error)
	SetAuthPolicy(context.Context, uuid.UUID, userauth.AuthPolicy) (userauth.AuthPolicy, error)
	PutOAuthProvider(context.Context, uuid.UUID, userauth.OAuthProviderConfig) (userauth.OAuthProviderConfig, error)
	DeleteOAuthProvider(context.Context, uuid.UUID, string) error
	PutMailTemplate(context.Context, uuid.UUID, userauth.MailTemplate) (userauth.MailTemplate, error)
	PreviewMailTemplate(context.Context, userauth.MailTemplate, string) (mailer.Message, error)
	PutSMTPConfig(context.Context, uuid.UUID, userauth.SMTPAdminConfig) (userauth.SMTPAdminConfig, error)
	TestSMTP(context.Context, uuid.UUID, string) error
	SetUserVerified(context.Context, uuid.UUID, bool) (userauth.ManagedUser, error)
	AdminRequestVerification(context.Context, uuid.UUID) error
	AdminRequestPasswordReset(context.Context, uuid.UUID) error
}

type authAdministrationEndpoints struct{ service AuthAdministration }

func authAdministrationRoutes(dependencies Dependencies) []route {
	if dependencies.AuthAdministration == nil {
		return nil
	}
	e := authAdministrationEndpoints{dependencies.AuthAdministration}
	read := func(pattern string, handler http.HandlerFunc) route {
		return route{method: http.MethodGet, pattern: pattern, access: accessAdminSameOriginRead, response: atomicJSON(maximumAdminResponseBytes), deadline: deadlineDefault, handler: handler}
	}
	write := func(method, pattern string, handler http.HandlerFunc) route {
		return route{method: method, pattern: pattern, access: accessAdminSession, response: atomicJSON(maximumAdminResponseBytes), deadline: deadlineDefault, maximumRequestBytes: maximumCredentialRequestBytes, handler: handler}
	}
	return []route{
		read("/api/admin/auth-collections/{id}/administration", e.get),
		write(http.MethodPut, "/api/admin/auth-collections/{id}/policy", e.policy),
		write(http.MethodPut, "/api/admin/auth-collections/{id}/oauth-providers/{provider}", e.provider),
		write(http.MethodDelete, "/api/admin/auth-collections/{id}/oauth-providers/{provider}", e.provider),
		write(http.MethodPut, "/api/admin/auth-collections/{id}/mail-templates/{purpose}", e.template),
		write(http.MethodPost, "/api/admin/auth-collections/{id}/mail-templates/{purpose}/preview", e.templatePreview),
		write(http.MethodPut, "/api/admin/auth-collections/{id}/smtp", e.smtp),
		write(http.MethodPost, "/api/admin/auth-collections/{id}/smtp/test", e.smtpTest),
		write(http.MethodPut, "/api/admin/users/{id}/verification", e.userVerification),
		write(http.MethodPost, "/api/admin/users/{id}/send-verification", e.sendVerification),
		write(http.MethodPost, "/api/admin/users/{id}/send-password-reset", e.sendReset),
	}
}

func collectionID(r *http.Request) (uuid.UUID, error) {
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil || id == uuid.Nil {
		return uuid.Nil, userauth.ErrValidation
	}
	return id, nil
}
func (e authAdministrationEndpoints) get(w http.ResponseWriter, r *http.Request) {
	id, err := collectionID(r)
	if err == nil {
		var v userauth.AuthAdministration
		v, err = e.service.GetAuthAdministration(r.Context(), id)
		if err == nil {
			w.Header().Set("Cache-Control", "no-store")
			writeJSON(w, http.StatusOK, v)
			return
		}
	}
	writeAuthAdministrationError(w, r, err)
}
func (e authAdministrationEndpoints) policy(w http.ResponseWriter, r *http.Request) {
	id, err := collectionID(r)
	var in struct {
		PasswordEnabled      *bool `json:"passwordEnabled"`
		MinimumPasswordBytes *int  `json:"minimumPasswordBytes"`
		MFAEnabled           *bool `json:"mfaEnabled"`
		OTPEnabled           *bool `json:"otpEnabled"`
		OTPDurationSeconds   *int  `json:"otpDurationSeconds"`
		OTPMaxAttempts       *int  `json:"otpMaxAttempts"`
	}
	if err != nil || decodeStrictJSON(r, &in) != nil || in.PasswordEnabled == nil || in.MinimumPasswordBytes == nil || in.MFAEnabled == nil {
		writeAuthAdministrationError(w, r, userauth.ErrValidation)
		return
	}
	policy := userauth.AuthPolicy{PasswordEnabled: *in.PasswordEnabled, MinimumPasswordBytes: *in.MinimumPasswordBytes, MFAEnabled: *in.MFAEnabled}
	if in.OTPEnabled == nil || in.OTPDurationSeconds == nil || in.OTPMaxAttempts == nil {
		current, getErr := e.service.GetAuthAdministration(r.Context(), id)
		if getErr != nil {
			writeAuthAdministrationError(w, r, getErr)
			return
		}
		policy.OTPEnabled = current.Policy.OTPEnabled
		policy.OTPDurationSeconds = current.Policy.OTPDurationSeconds
		policy.OTPMaxAttempts = current.Policy.OTPMaxAttempts
	} else {
		policy.OTPEnabled = *in.OTPEnabled
		policy.OTPDurationSeconds = *in.OTPDurationSeconds
		policy.OTPMaxAttempts = *in.OTPMaxAttempts
	}
	v, err := e.service.SetAuthPolicy(r.Context(), id, policy)
	if err != nil {
		writeAuthAdministrationError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, v)
}
func (e authAdministrationEndpoints) provider(w http.ResponseWriter, r *http.Request) {
	id, err := collectionID(r)
	name := r.PathValue("provider")
	if r.Method == http.MethodDelete {
		if err == nil {
			err = e.service.DeleteOAuthProvider(r.Context(), id, name)
		}
		if err != nil {
			writeAuthAdministrationError(w, r, err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
		return
	}
	var in struct {
		Enabled               bool     `json:"enabled"`
		AuthorizationURL      string   `json:"authorizationUrl"`
		TokenURL              string   `json:"tokenUrl"`
		UserInfoURL           string   `json:"userInfoUrl"`
		ClientID              string   `json:"clientId"`
		ClientSecret          string   `json:"clientSecret"`
		Scopes                []string `json:"scopes"`
		RedirectURIs          []string `json:"redirectUris"`
		SubjectField          string   `json:"subjectField"`
		EmailField            string   `json:"emailField"`
		EmailVerifiedField    string   `json:"emailVerifiedField"`
		AvatarField           string   `json:"avatarField"`
		AllowInsecureLoopback bool     `json:"allowInsecureLoopback"`
	}
	if err != nil || decodeStrictJSON(r, &in) != nil {
		writeAuthAdministrationError(w, r, userauth.ErrValidation)
		return
	}
	v, err := e.service.PutOAuthProvider(r.Context(), id, userauth.OAuthProviderConfig{Name: name, Enabled: in.Enabled, AuthorizationURL: in.AuthorizationURL, TokenURL: in.TokenURL, UserInfoURL: in.UserInfoURL, ClientID: in.ClientID, ClientSecret: in.ClientSecret, Scopes: in.Scopes, RedirectURIs: in.RedirectURIs, SubjectField: in.SubjectField, EmailField: in.EmailField, EmailVerifiedField: in.EmailVerifiedField, AvatarField: in.AvatarField, AllowInsecureLoopback: in.AllowInsecureLoopback})
	if err != nil {
		writeAuthAdministrationError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, v)
}
func (e authAdministrationEndpoints) template(w http.ResponseWriter, r *http.Request) {
	id, err := collectionID(r)
	var in struct {
		Subject  string `json:"subject"`
		Body     string `json:"body"`
		HTMLBody string `json:"htmlBody"`
	}
	if err != nil || decodeStrictJSON(r, &in) != nil {
		writeAuthAdministrationError(w, r, userauth.ErrValidation)
		return
	}
	v, err := e.service.PutMailTemplate(r.Context(), id, userauth.MailTemplate{Purpose: r.PathValue("purpose"), Subject: in.Subject, Body: in.Body, HTMLBody: in.HTMLBody})
	if err != nil {
		writeAuthAdministrationError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, v)
}

func (e authAdministrationEndpoints) templatePreview(w http.ResponseWriter, r *http.Request) {
	_, err := collectionID(r)
	var in struct {
		Subject   string `json:"subject"`
		Body      string `json:"body"`
		HTMLBody  string `json:"htmlBody"`
		ActionURL string `json:"actionUrl"`
	}
	if err != nil || decodeStrictJSON(r, &in) != nil {
		writeAuthAdministrationError(w, r, userauth.ErrValidation)
		return
	}
	v, err := e.service.PreviewMailTemplate(r.Context(), userauth.MailTemplate{Purpose: r.PathValue("purpose"), Subject: in.Subject, Body: in.Body, HTMLBody: in.HTMLBody}, in.ActionURL)
	if err != nil {
		writeAuthAdministrationError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, v)
}
func (e authAdministrationEndpoints) smtp(w http.ResponseWriter, r *http.Request) {
	id, err := collectionID(r)
	var in struct {
		Address               string `json:"address"`
		From                  string `json:"from"`
		Username              string `json:"username"`
		Password              string `json:"password"`
		ServerName            string `json:"serverName"`
		ImplicitTLS           bool   `json:"implicitTls"`
		AllowInsecureLoopback bool   `json:"allowInsecureLoopback"`
	}
	if err != nil || decodeStrictJSON(r, &in) != nil {
		writeAuthAdministrationError(w, r, userauth.ErrValidation)
		return
	}
	v, err := e.service.PutSMTPConfig(r.Context(), id, userauth.SMTPAdminConfig{Address: in.Address, From: in.From, Username: in.Username, Password: in.Password, ServerName: in.ServerName, ImplicitTLS: in.ImplicitTLS, AllowInsecureLoopback: in.AllowInsecureLoopback})
	if err != nil {
		writeAuthAdministrationError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, v)
}
func (e authAdministrationEndpoints) smtpTest(w http.ResponseWriter, r *http.Request) {
	id, err := collectionID(r)
	var in struct {
		Recipient string `json:"recipient"`
	}
	if err != nil || decodeStrictJSON(r, &in) != nil {
		writeAuthAdministrationError(w, r, userauth.ErrValidation)
		return
	}
	if err = e.service.TestSMTP(r.Context(), id, in.Recipient); err != nil {
		writeAuthAdministrationError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
func userAdminID(r *http.Request) (uuid.UUID, error) {
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil || id == uuid.Nil {
		return uuid.Nil, userauth.ErrValidation
	}
	return id, nil
}
func (e authAdministrationEndpoints) userVerification(w http.ResponseWriter, r *http.Request) {
	id, err := userAdminID(r)
	var in struct {
		Verified *bool `json:"verified"`
	}
	if err != nil || decodeStrictJSON(r, &in) != nil || in.Verified == nil {
		writeAuthAdministrationError(w, r, userauth.ErrValidation)
		return
	}
	u, err := e.service.SetUserVerified(r.Context(), id, *in.Verified)
	if err != nil {
		writeAuthAdministrationError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, managedUser(u))
}
func (e authAdministrationEndpoints) sendVerification(w http.ResponseWriter, r *http.Request) {
	e.userAction(w, r, e.service.AdminRequestVerification)
}
func (e authAdministrationEndpoints) sendReset(w http.ResponseWriter, r *http.Request) {
	e.userAction(w, r, e.service.AdminRequestPasswordReset)
}
func (e authAdministrationEndpoints) userAction(w http.ResponseWriter, r *http.Request, action func(context.Context, uuid.UUID) error) {
	id, err := userAdminID(r)
	if err == nil {
		err = action(r.Context(), id)
	}
	if err != nil {
		writeAuthAdministrationError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
func writeAuthAdministrationError(w http.ResponseWriter, r *http.Request, err error) {
	w.Header().Set("Cache-Control", "no-store")
	response := APIError{Status: http.StatusServiceUnavailable, Code: "auth_administration_unavailable", Message: "Authentication administration is unavailable"}
	if errors.Is(err, userauth.ErrValidation) {
		response = APIError{Status: http.StatusBadRequest, Code: "invalid_request", Message: "Invalid authentication administration request"}
	} else if errors.Is(err, userauth.ErrNotFound) {
		response = APIError{Status: http.StatusNotFound, Code: "not_found", Message: "Authentication resource not found"}
	} else if errors.Is(err, userauth.ErrConflict) {
		response = APIError{Status: http.StatusConflict, Code: "conflict", Message: "Authentication change conflicts with current state"}
	}
	writeAPIError(w, r, response)
}
