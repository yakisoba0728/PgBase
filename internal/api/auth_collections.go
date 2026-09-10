package api

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"time"

	"github.com/google/uuid"
	"github.com/yakisoba0728/PgBase/internal/schema"
	"github.com/yakisoba0728/PgBase/internal/userauth"
)

type AuthCollections interface {
	CreateAuthCollection(context.Context, userauth.CreateAuthCollectionInput) (userauth.AuthCollection, error)
	UpdateAuthCollection(context.Context, uuid.UUID, userauth.UpdateAuthCollectionInput) (userauth.AuthCollection, error)
	DeleteAuthCollection(context.Context, uuid.UUID) error
	ListAuthCollections(context.Context) ([]userauth.AuthCollection, error)
	GetAuthCollection(context.Context, string) (userauth.AuthCollection, error)
	RegisterInCollection(context.Context, uuid.UUID, string, string, string) (userauth.Session, error)
	LoginInCollection(context.Context, uuid.UUID, string, string, string) (userauth.Session, error)
	AuthenticateForCollection(context.Context, uuid.UUID, string) (userauth.Principal, error)
	PublicAuthMethods(context.Context, uuid.UUID) (userauth.PublicAuthMethods, error)
	RefreshSessionForCollection(context.Context, uuid.UUID, string) (userauth.Session, error)
	Logout(context.Context, string) error
	RequestPasswordResetInCollection(context.Context, uuid.UUID, string, string) error
	BeginOAuthInCollection(context.Context, uuid.UUID, string, string, string) (userauth.OAuthAuthorization, error)
	BeginOAuthInCollectionWithProviderRedirect(context.Context, uuid.UUID, string, string, string, string) (userauth.OAuthAuthorization, error)
	RequestEmailOTPInCollection(context.Context, uuid.UUID, string, string) (userauth.EmailOTPRequest, error)
	CompleteEmailOTPInCollection(context.Context, uuid.UUID, string, string) (userauth.Session, error)
	GetProfile(context.Context, uuid.UUID) (map[string]any, error)
	SetProfile(context.Context, uuid.UUID, map[string]any) (map[string]any, error)
	ListAuthRecords(context.Context, uuid.UUID, int) ([]userauth.AuthRecord, error)
	GetAuthRecord(context.Context, uuid.UUID, uuid.UUID) (userauth.AuthRecord, error)
	CreateAuthRecord(context.Context, uuid.UUID, userauth.CreateAuthRecordInput) (userauth.AuthRecord, error)
	UpdateAuthRecord(context.Context, uuid.UUID, uuid.UUID, userauth.UpdateAuthRecordInput) (userauth.AuthRecord, error)
	DeleteAuthRecord(context.Context, uuid.UUID, uuid.UUID) error
	ListAuthAuxiliary(context.Context, uuid.UUID, uuid.UUID, string) ([]userauth.AuthAuxiliaryRecord, error)
	DeleteAuthAuxiliary(context.Context, uuid.UUID, uuid.UUID, string, uuid.UUID) error
	ListAuthorizedAuthRecords(context.Context,uuid.UUID,uuid.UUID,int)([]userauth.AuthRecord,error)
	GetAuthorizedAuthRecord(context.Context,uuid.UUID,uuid.UUID,uuid.UUID)(userauth.AuthRecord,error)
	UpdateAuthorizedAuthRecord(context.Context,uuid.UUID,uuid.UUID,uuid.UUID,userauth.UpdateAuthRecordInput)(userauth.AuthRecord,error)
	DeleteAuthorizedAuthRecord(context.Context,uuid.UUID,uuid.UUID,uuid.UUID)error
}

type authCollectionEndpoints struct{ dependencies Dependencies }

func authCollectionRoutes(dependencies Dependencies) []route {
	if dependencies.AuthCollections == nil {
		return nil
	}
	e := authCollectionEndpoints{dependencies}
	return []route{
		{method: "GET", pattern: "/api/admin/auth-collections/meta/oauth2-providers", access: accessAdminSameOriginRead, response: atomicJSON(maximumCatalogResponseBytes), deadline: deadlineDefault, handler: http.HandlerFunc(e.oauthProviderCatalog)},
		{method: "GET", pattern: "/api/admin/auth-collections", access: accessAdminSameOriginRead, response: atomicJSON(maximumCatalogResponseBytes), deadline: deadlineDefault, handler: http.HandlerFunc(e.adminList)},
		{method: "POST", pattern: "/api/admin/auth-collections", access: accessAdminSession, response: atomicJSON(maximumAdminResponseBytes), deadline: deadlineDefault, maximumRequestBytes: maximumCredentialRequestBytes, handler: http.HandlerFunc(e.adminCreate)},
		{method: "PATCH", pattern: "/api/admin/auth-collections/{id}", access: accessAdminSession, response: atomicJSON(maximumAdminResponseBytes), deadline: deadlineDefault, maximumRequestBytes: maximumCredentialRequestBytes, handler: http.HandlerFunc(e.adminUpdate)},
		{method: "DELETE", pattern: "/api/admin/auth-collections/{id}", access: accessAdminSession, response: atomicJSON(maximumAdminResponseBytes), deadline: deadlineDefault, handler: http.HandlerFunc(e.adminDelete)},
		{method: "GET", pattern: "/api/admin/auth-collections/{id}/records", access: accessAdminSameOriginRead, response: atomicJSON(maximumCatalogResponseBytes), deadline: deadlineDefault, handler: http.HandlerFunc(e.adminRecordList)},
		{method: "POST", pattern: "/api/admin/auth-collections/{id}/records", access: accessAdminSession, response: atomicJSON(maximumAdminResponseBytes), deadline: deadlineDefault, maximumRequestBytes: maximumCredentialRequestBytes, handler: http.HandlerFunc(e.adminRecordCreate)},
		{method: "GET", pattern: "/api/admin/auth-collections/{id}/records/{record}", access: accessAdminSameOriginRead, response: atomicJSON(maximumAdminResponseBytes), deadline: deadlineDefault, handler: http.HandlerFunc(e.adminRecordGet)},
		{method: "PATCH", pattern: "/api/admin/auth-collections/{id}/records/{record}", access: accessAdminSession, response: atomicJSON(maximumAdminResponseBytes), deadline: deadlineDefault, maximumRequestBytes: maximumCredentialRequestBytes, handler: http.HandlerFunc(e.adminRecordUpdate)},
		{method: "DELETE", pattern: "/api/admin/auth-collections/{id}/records/{record}", access: accessAdminSession, response: atomicJSON(maximumAdminResponseBytes), deadline: deadlineDefault, handler: http.HandlerFunc(e.adminRecordDelete)},
		{method: "GET", pattern: "/api/v1/auth/{authCollection}/methods", access: accessPublic, response: atomicJSON(maximumCatalogResponseBytes), deadline: deadlineDefault, handler: http.HandlerFunc(e.methods)},
		{method: "POST", pattern: "/api/v1/auth/{authCollection}/register", access: accessPublic, response: atomicJSON(maximumAdminResponseBytes), deadline: deadlineDefault, maximumRequestBytes: maximumCredentialRequestBytes, handler: e.credentials(true)},
		{method: "POST", pattern: "/api/v1/auth/{authCollection}/login", access: accessPublic, response: atomicJSON(maximumAdminResponseBytes), deadline: deadlineDefault, maximumRequestBytes: maximumCredentialRequestBytes, handler: e.credentials(false)},
		{method: "POST", pattern: "/api/v1/auth/{authCollection}/request-password-reset", access: accessPublic, response: atomicJSON(maximumAdminResponseBytes), deadline: deadlineDefault, maximumRequestBytes: maximumCredentialRequestBytes, handler: http.HandlerFunc(e.reset)},
		{method: "POST", pattern: "/api/v1/auth/{authCollection}/oauth/start", access: accessPublic, response: atomicJSON(maximumAdminResponseBytes), deadline: deadlineDefault, maximumRequestBytes: maximumCredentialRequestBytes, handler: http.HandlerFunc(e.oauthStart)},
		{method: "POST", pattern: "/api/v1/auth/{authCollection}/otp/request", access: accessPublic, response: atomicJSON(maximumAdminResponseBytes), deadline: deadlineDefault, maximumRequestBytes: maximumCredentialRequestBytes, handler: http.HandlerFunc(e.otpRequest)},
		{method: "POST", pattern: "/api/v1/auth/{authCollection}/otp/complete", access: accessPublic, response: atomicJSON(maximumAdminResponseBytes), deadline: deadlineDefault, maximumRequestBytes: maximumCredentialRequestBytes, handler: http.HandlerFunc(e.otpComplete)},
		{method: "GET", pattern: "/api/v1/auth/{authCollection}/session", access: accessUserSession, response: atomicJSON(maximumAdminResponseBytes), deadline: deadlineDefault, handler: http.HandlerFunc(e.session)},
		{method: "POST", pattern: "/api/v1/auth/{authCollection}/refresh", access: accessUserSession, response: atomicJSON(maximumAdminResponseBytes), deadline: deadlineDefault, handler: http.HandlerFunc(e.refresh)},
		{method: "POST", pattern: "/api/v1/auth/{authCollection}/logout", access: accessUserSession, response: atomicJSON(maximumAdminResponseBytes), deadline: deadlineDefault, handler: http.HandlerFunc(e.logout)},
		{method: "GET", pattern: "/api/v1/auth/{authCollection}/profile", access: accessUserSession, response: atomicJSON(maximumAdminResponseBytes), deadline: deadlineDefault, handler: http.HandlerFunc(e.profile)},
		{method: "PATCH", pattern: "/api/v1/auth/{authCollection}/profile", access: accessUserSession, response: atomicJSON(maximumAdminResponseBytes), deadline: deadlineDefault, maximumRequestBytes: maximumCredentialRequestBytes, handler: http.HandlerFunc(e.profile)},
		{method: "GET", pattern: "/api/v1/auth/{authCollection}/aux/{kind}", access: accessUserSession, response: atomicJSON(maximumAdminResponseBytes), deadline: deadlineDefault, handler: http.HandlerFunc(e.auxiliaryList)},
		{method: "DELETE", pattern: "/api/v1/auth/{authCollection}/aux/{kind}/{id}", access: accessUserSession, response: atomicJSON(maximumAdminResponseBytes), deadline: deadlineDefault, handler: http.HandlerFunc(e.auxiliaryDelete)},
		{method:"GET",pattern:"/api/v1/auth/{authCollection}/records",access:accessUserSession,response:atomicJSON(maximumCatalogResponseBytes),deadline:deadlineDefault,handler:http.HandlerFunc(e.authorizedRecordList)},
		{method:"GET",pattern:"/api/v1/auth/{authCollection}/records/{record}",access:accessUserSession,response:atomicJSON(maximumAdminResponseBytes),deadline:deadlineDefault,handler:http.HandlerFunc(e.authorizedRecordGet)},
		{method:"PATCH",pattern:"/api/v1/auth/{authCollection}/records/{record}",access:accessUserSession,response:atomicJSON(maximumAdminResponseBytes),deadline:deadlineDefault,maximumRequestBytes:maximumCredentialRequestBytes,handler:http.HandlerFunc(e.authorizedRecordUpdate)},
		{method:"DELETE",pattern:"/api/v1/auth/{authCollection}/records/{record}",access:accessUserSession,response:atomicJSON(maximumAdminResponseBytes),deadline:deadlineDefault,handler:http.HandlerFunc(e.authorizedRecordDelete)},
	}
}

func (e authCollectionEndpoints) oauthProviderCatalog(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, struct {
		Items []userauth.OAuthProviderCatalog `json:"items"`
	}{Items: userauth.OAuthProviderCatalogs()})
}

func (e authCollectionEndpoints) methods(w http.ResponseWriter, r *http.Request) {
	collection, err := e.resolve(r)
	if err != nil {
		writeUserAuthError(w, r, userauth.ErrInvalidCredentials)
		return
	}
	methods, err := e.dependencies.AuthCollections.PublicAuthMethods(r.Context(), collection.ID)
	if err != nil {
		writeUserAuthError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, methods)
}

func (e authCollectionEndpoints) otpRequest(w http.ResponseWriter, r *http.Request) {
	projection, ok := compileAuthProjection(w, r)
	if !ok {
		return
	}
	collection, err := e.resolve(r)
	var in struct {
		Email string `json:"email"`
	}
	if err != nil || decodeStrictJSON(r, &in) != nil {
		writeUserAuthError(w, r, userauth.ErrValidation)
		return
	}
	address, ok := clientAddress(r, e.dependencies.trustedProxies())
	if !ok {
		writeUserAuthError(w, r, userauth.ErrValidation)
		return
	}
	value, err := e.dependencies.AuthCollections.RequestEmailOTPInCollection(r.Context(), collection.ID, in.Email, address)
	if err != nil {
		writeUserAuthError(w, r, err)
		return
	}
	writeAuthProjectedJSON(w, http.StatusOK, projection, value)
}
func (e authCollectionEndpoints) otpComplete(w http.ResponseWriter, r *http.Request) {
	projection, ok := compileAuthProjection(w, r)
	if !ok {
		return
	}
	collection, err := e.resolve(r)
	var in struct {
		OTPID string `json:"otpId"`
		Code  string `json:"code"`
	}
	if err != nil || decodeStrictJSON(r, &in) != nil {
		writeUserAuthError(w, r, userauth.ErrValidation)
		return
	}
	value, err := e.dependencies.AuthCollections.CompleteEmailOTPInCollection(r.Context(), collection.ID, in.OTPID, in.Code)
	if err != nil {
		writeUserAuthError(w, r, err)
		return
	}
	writeAuthProjectedJSON(w, http.StatusOK, projection, userSessionResponse{Token: value.RawToken, User: publicUser(value.User), ExpiresAt: value.ExpiresAt})
}
func (e authCollectionEndpoints) oauthStart(w http.ResponseWriter, r *http.Request) {
	projection, ok := compileAuthProjection(w, r)
	if !ok {
		return
	}
	collection, err := e.resolve(r)
	var in struct {
		Provider    string `json:"provider"`
		RedirectURI string `json:"redirectUri"`
	}
	if err != nil || decodeStrictJSON(r, &in) != nil {
		writeUserAuthError(w, r, userauth.ErrValidation)
		return
	}
	address, ok := clientAddress(r, e.dependencies.trustedProxies())
	if !ok {
		writeUserAuthError(w, r, userauth.ErrValidation)
		return
	}
	var value userauth.OAuthAuthorization
	if in.Provider == "apple" && e.dependencies.PublicURL != nil {
		bridge := e.dependencies.PublicURL.ResolveReference(&url.URL{Path: "/api/v1/auth/oauth/apple/callback"}).String()
		value, err = e.dependencies.AuthCollections.BeginOAuthInCollectionWithProviderRedirect(r.Context(), collection.ID, in.Provider, in.RedirectURI, bridge, address)
	} else {
		value, err = e.dependencies.AuthCollections.BeginOAuthInCollection(r.Context(), collection.ID, in.Provider, in.RedirectURI, address)
	}
	if err != nil {
		writeUserAuthError(w, r, err)
		return
	}
	writeAuthProjectedJSON(w, http.StatusOK, projection, struct {
		AuthorizationURL    string    `json:"authorizationUrl"`
		ExpiresAt           time.Time `json:"expiresAt"`
		State               string    `json:"state"`
		CodeVerifier        string    `json:"codeVerifier"`
		CodeChallenge       string    `json:"codeChallenge"`
		CodeChallengeMethod string    `json:"codeChallengeMethod"`
	}{value.AuthorizationURL, value.ExpiresAt, value.State, value.CodeVerifier, value.CodeChallenge, value.CodeChallengeMethod})
}

func authCollectionResponse(v userauth.AuthCollection) any {
	return struct {
		ID                  string                       `json:"id"`
		Name                string                       `json:"name"`
		Type                string                       `json:"type"`
		RegistrationEnabled bool                         `json:"registrationEnabled"`
		ProfileSchema       map[string]string            `json:"profileSchema"`
		Fields              []schema.FieldInput          `json:"fields"`
		Rules               userauth.AuthCollectionRules `json:"rules"`
	}{v.ID.String(), v.Name, "auth", v.RegistrationEnabled, v.ProfileSchema, v.Fields, v.Rules}
}
func (e authCollectionEndpoints) adminList(w http.ResponseWriter, r *http.Request) {
	projection, err := authProjection(r)
	if err != nil {
		writeAuthCollectionError(w, r, err)
		return
	}
	values, err := e.dependencies.AuthCollections.ListAuthCollections(r.Context())
	if err != nil {
		writeAuthCollectionError(w, r, err)
		return
	}
	items := make([]any, 0, len(values))
	for _, v := range values {
		items = append(items, authCollectionResponse(v))
	}
	writeAuthProjectedJSON(w, http.StatusOK, projection, struct {
		Items []any `json:"items"`
	}{items})
}
func (e authCollectionEndpoints) adminCreate(w http.ResponseWriter, r *http.Request) {
	projection, err := authProjection(r)
	if err != nil {
		writeAuthCollectionError(w, r, err)
		return
	}
	var in struct {
		ID                  string                       `json:"id"`
		Name                string                       `json:"name"`
		RegistrationEnabled bool                         `json:"registrationEnabled"`
		ProfileSchema       map[string]string            `json:"profileSchema"`
		Fields              []schema.FieldInput          `json:"fields"`
		Rules               userauth.AuthCollectionRules `json:"rules"`
	}
	id := uuid.Nil
	var parseErr error
	if decodeStrictJSON(r, &in) == nil {
		id, parseErr = uuid.Parse(in.ID)
	} else {
		parseErr = userauth.ErrValidation
	}
	if parseErr != nil {
		writeAuthCollectionError(w, r, userauth.ErrValidation)
		return
	}
	v, err := e.dependencies.AuthCollections.CreateAuthCollection(r.Context(), userauth.CreateAuthCollectionInput{ID: id, Name: in.Name, RegistrationEnabled: in.RegistrationEnabled, ProfileSchema: in.ProfileSchema, Fields: in.Fields, Rules: in.Rules})
	if err != nil {
		writeAuthCollectionError(w, r, err)
		return
	}
	writeAuthProjectedJSON(w, http.StatusCreated, projection, authCollectionResponse(v))
}
func (e authCollectionEndpoints) adminUpdate(w http.ResponseWriter, r *http.Request) {
	projection, projectionErr := authProjection(r)
	if projectionErr != nil {
		writeAuthCollectionError(w, r, projectionErr)
		return
	}
	id, err := uuid.Parse(r.PathValue("id"))
	var in struct {
		RegistrationEnabled *bool                        `json:"registrationEnabled"`
		ProfileSchema       *map[string]string           `json:"profileSchema"`
		Fields              []schema.FieldInput          `json:"fields"`
		Rules               userauth.AuthCollectionRules `json:"rules"`
	}
	if err != nil || decodeStrictJSON(r, &in) != nil || in.RegistrationEnabled == nil || (in.ProfileSchema == nil && in.Fields == nil) {
		writeAuthCollectionError(w, r, userauth.ErrValidation)
		return
	}
	legacy := map[string]string(nil)
	if in.ProfileSchema != nil {
		legacy = *in.ProfileSchema
	}
	v, err := e.dependencies.AuthCollections.UpdateAuthCollection(r.Context(), id, userauth.UpdateAuthCollectionInput{RegistrationEnabled: *in.RegistrationEnabled, ProfileSchema: legacy, Fields: in.Fields, Rules: in.Rules})
	if err != nil {
		writeAuthCollectionError(w, r, err)
		return
	}
	writeAuthProjectedJSON(w, http.StatusOK, projection, authCollectionResponse(v))
}
func (e authCollectionEndpoints) adminDelete(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		writeAuthCollectionError(w, r, userauth.ErrValidation)
		return
	}
	if err := e.dependencies.AuthCollections.DeleteAuthCollection(r.Context(), id); err != nil {
		writeAuthCollectionError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (e authCollectionEndpoints) resolve(r *http.Request) (userauth.AuthCollection, error) {
	return e.dependencies.AuthCollections.GetAuthCollection(r.Context(), r.PathValue("authCollection"))
}
func (e authCollectionEndpoints) credentials(register bool) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		projection, ok := compileAuthProjection(w, r)
		if !ok {
			return
		}
		collection, err := e.resolve(r)
		if err != nil {
			writeUserAuthError(w, r, userauth.ErrInvalidCredentials)
			return
		}
		var in struct {
			Email    string `json:"email"`
			Password string `json:"password"`
		}
		if decodeStrictJSON(r, &in) != nil {
			writeUserAuthError(w, r, userauth.ErrValidation)
			return
		}
		address, ok := clientAddress(r, e.dependencies.trustedProxies())
		if !ok {
			writeUserAuthError(w, r, userauth.ErrValidation)
			return
		}
		var session userauth.Session
		if register {
			session, err = e.dependencies.AuthCollections.RegisterInCollection(r.Context(), collection.ID, in.Email, in.Password, address)
		} else {
			session, err = e.dependencies.AuthCollections.LoginInCollection(r.Context(), collection.ID, in.Email, in.Password, address)
		}
		if err != nil {
			writeUserAuthError(w, r, err)
			return
		}
		status := http.StatusOK
		if register {
			status = http.StatusCreated
		}
		writeAuthProjectedJSON(w, status, projection, userSessionResponse{Token: session.RawToken, User: publicUser(session.User), ExpiresAt: session.ExpiresAt})
	})
}
func (e authCollectionEndpoints) reset(w http.ResponseWriter, r *http.Request) {
	collection, err := e.resolve(r)
	var in struct {
		Email string `json:"email"`
	}
	if err != nil || decodeStrictJSON(r, &in) != nil {
		writeUserAuthError(w, r, userauth.ErrValidation)
		return
	}
	address, ok := clientAddress(r, e.dependencies.trustedProxies())
	if !ok {
		writeUserAuthError(w, r, userauth.ErrValidation)
		return
	}
	if err := e.dependencies.AuthCollections.RequestPasswordResetInCollection(r.Context(), collection.ID, in.Email, address); err != nil {
		writeUserAuthError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
func (e authCollectionEndpoints) current(r *http.Request) (authenticatedUser, error) {
	current, ok := r.Context().Value(userPrincipalKey{}).(authenticatedUser)
	if !ok {
		return authenticatedUser{}, userauth.ErrInvalidCredentials
	}
	collection, err := e.resolve(r)
	if err != nil || current.principal.User.AuthCollectionID != collection.ID {
		return authenticatedUser{}, userauth.ErrInvalidCredentials
	}
	return current, nil
}
func (e authCollectionEndpoints) session(w http.ResponseWriter, r *http.Request) {
	projection, ok := compileAuthProjection(w, r)
	if !ok {
		return
	}
	current, err := e.current(r)
	if err != nil {
		writeUserAuthError(w, r, err)
		return
	}
	writeAuthProjectedJSON(w, http.StatusOK, projection, userSessionFromPrincipal(current.principal))
}
func (e authCollectionEndpoints) refresh(w http.ResponseWriter, r *http.Request) {
	projection, ok := compileAuthProjection(w, r)
	if !ok {
		return
	}
	current, err := e.current(r)
	if err != nil {
		writeUserAuthError(w, r, err)
		return
	}
	refreshed, err := e.dependencies.AuthCollections.RefreshSessionForCollection(r.Context(), current.principal.User.AuthCollectionID, current.token)
	if err != nil {
		writeUserAuthError(w, r, err)
		return
	}
	writeAuthProjectedJSON(w, http.StatusOK, projection, userSessionFromResult(refreshed))
}

func (e authCollectionEndpoints) logout(w http.ResponseWriter, r *http.Request) {
	current, err := e.current(r)
	if err != nil {
		writeUserAuthError(w, r, err)
		return
	}
	if err := e.dependencies.AuthCollections.Logout(r.Context(), current.token); err != nil {
		writeUserAuthError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
func (e authCollectionEndpoints) profile(w http.ResponseWriter, r *http.Request) {
	projection, ok := compileAuthProjection(w, r)
	if !ok {
		return
	}
	current, err := e.current(r)
	if err != nil {
		writeUserAuthError(w, r, err)
		return
	}
	var value map[string]any
	if r.Method == http.MethodPatch {
		if decodeStrictJSON(r, &value) != nil {
			writeUserAuthError(w, r, userauth.ErrValidation)
			return
		}
		value, err = e.dependencies.AuthCollections.SetProfile(r.Context(), current.principal.User.ID, value)
	} else {
		value, err = e.dependencies.AuthCollections.GetProfile(r.Context(), current.principal.User.ID)
	}
	if err != nil {
		writeAuthCollectionError(w, r, err)
		return
	}
	writeAuthProjectedJSON(w, http.StatusOK, projection, struct {
		Attributes map[string]any `json:"attributes"`
	}{value})
}
func writeAuthCollectionError(w http.ResponseWriter, r *http.Request, err error) {
	response := APIError{Status: http.StatusServiceUnavailable, Code: "auth_collection_unavailable", Message: "Authentication collection is unavailable"}
	switch {
	case errors.Is(err, userauth.ErrValidation):
		response = APIError{Status: 400, Code: "invalid_request", Message: "Invalid authentication collection request"}
	case errors.Is(err, userauth.ErrNotFound):
		response = APIError{Status: 404, Code: "not_found", Message: "Authentication collection not found"}
	case errors.Is(err, userauth.ErrConflict):
		response = APIError{Status: 409, Code: "conflict", Message: "Authentication collection conflicts with existing data"}
	}
	writeAPIError(w, r, response)
}
