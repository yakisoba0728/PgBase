package api

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"net/netip"
	"net/url"
	"time"

	"github.com/google/uuid"
	"github.com/yakisoba0728/PgBase/internal/adminauth"
	"github.com/yakisoba0728/PgBase/internal/database"
	"github.com/yakisoba0728/PgBase/internal/jsonvalue"
	"github.com/yakisoba0728/PgBase/internal/observability"
)

const (
	adminCookieName          = "__Host-pgbase_admin"
	adminDevelopmentCookie   = "pgbase_admin"
	authenticationRetryAfter = "1"
)

type AdminAuth interface {
	SetupRequired(context.Context) (bool, error)
	Setup(context.Context, string, string, string, string) (adminauth.Session, error)
	Login(context.Context, string, string, string) (adminauth.Session, error)
	Authenticate(context.Context, string) (adminauth.Principal, error)
	Logout(context.Context, string) error
}

// adminOTPLogin is optional so existing AdminAuth implementations retain their
// established password-only contract. The production service implements it.
type adminOTPLogin interface {
	LoginWithOTP(context.Context, string, string, string, string) (adminauth.Session, error)
}

func (e adminEndpoints) status(w http.ResponseWriter, r *http.Request) {
	if e.auth == nil {
		writeAdminAuthError(w, r, errors.New("administrator authentication is unavailable"))
		return
	}
	var setupRequired bool
	var err error
	if e.statusCache == nil {
		setupRequired, err = e.auth.SetupRequired(r.Context())
	} else {
		setupRequired, err = e.statusCache.get(r.Context(), e.auth.SetupRequired)
	}
	if err != nil {
		writeAdminAuthError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, struct {
		SetupRequired bool `json:"setupRequired"`
	}{SetupRequired: setupRequired})
}

type adminEndpoints struct {
	auth           AdminAuth
	publicURL      *url.URL
	trustedProxies func() []netip.Prefix
	sqlConsole     bool
	recorder       observability.Recorder
	statusCache    *setupStatusCache
}

type administratorResponse struct {
	ID        uuid.UUID `json:"id"`
	Email     string    `json:"email"`
	Active    bool      `json:"active"`
	CreatedAt time.Time `json:"createdAt"`
	UpdatedAt time.Time `json:"updatedAt"`
}

type adminSessionResponse struct {
	Administrator     administratorResponse `json:"administrator"`
	CSRFToken         string                `json:"csrfToken"`
	ExpiresAt         time.Time             `json:"expiresAt"`
	SQLConsoleEnabled bool                  `json:"sqlConsoleEnabled"`
}

func newAdminEndpoints(dependencies Dependencies) adminEndpoints {
	var recorder observability.Recorder
	if dependencies.Metrics != nil {
		recorder = dependencies.Metrics
	}
	return adminEndpoints{
		auth:           dependencies.AdminAuth,
		publicURL:      dependencies.PublicURL,
		trustedProxies: dependencies.trustedProxies,
		sqlConsole:     dependencies.SQLConsole != nil,
		recorder:       recorder,
		statusCache:    newSetupStatusCache(),
	}
}

func (e adminEndpoints) setup(w http.ResponseWriter, r *http.Request) {
	started := time.Now()
	outcome := observability.OutcomeFailure
	defer func() {
		e.recordOperation(r.Context(), observability.OperationAdminSetup, outcome, time.Since(started))
	}()
	var input struct {
		SetupToken string `json:"setupToken"`
		Email      string `json:"email"`
		Password   string `json:"password"`
	}
	if err := decodeStrictJSON(r, &input); err != nil {
		outcome = observability.OutcomeValidation
		writeAPIError(w, r, APIError{Status: http.StatusBadRequest, Code: "invalid_request", Message: "Invalid request"})
		return
	}
	if e.auth == nil {
		outcome = observability.OutcomeUnavailable
		writeAdminAuthError(w, r, errors.New("administrator authentication is unavailable"))
		return
	}
	subject, ok := clientAddress(r, currentTrustedProxies(e.trustedProxies))
	if !ok {
		outcome = observability.OutcomeValidation
		writeAPIError(w, r, APIError{Status: http.StatusBadRequest, Code: "invalid_request", Message: "Invalid request"})
		return
	}
	session, err := e.auth.Setup(r.Context(), input.SetupToken, input.Email, input.Password, subject)
	if err != nil {
		outcome = adminAuthOutcome(err)
		writeAdminAuthError(w, r, err)
		return
	}
	e.statusCache.invalidate()
	outcome = observability.OutcomeSuccess
	e.writeIssuedSession(w, r, http.StatusCreated, session)
}

func (e adminEndpoints) login(w http.ResponseWriter, r *http.Request) {
	started := time.Now()
	outcome := observability.OutcomeFailure
	defer func() {
		e.recordOperation(r.Context(), observability.OperationAdminLogin, outcome, time.Since(started))
	}()
	var input struct {
		Email    string `json:"email"`
		Password string `json:"password"`
		OTPCode  string `json:"otpCode"`
	}
	if err := decodeStrictJSON(r, &input); err != nil {
		outcome = observability.OutcomeValidation
		writeAPIError(w, r, APIError{Status: http.StatusBadRequest, Code: "invalid_request", Message: "Invalid request"})
		return
	}
	if e.auth == nil {
		outcome = observability.OutcomeUnavailable
		writeAdminAuthError(w, r, errors.New("administrator authentication is unavailable"))
		return
	}
	subject, ok := clientIP(r, currentTrustedProxies(e.trustedProxies))
	if !ok {
		outcome = observability.OutcomeValidation
		writeAPIError(w, r, APIError{Status: http.StatusBadRequest, Code: "invalid_request", Message: "Invalid request"})
		return
	}
	var session adminauth.Session
	var err error
	if otpAuth, ok := e.auth.(adminOTPLogin); ok {
		session, err = otpAuth.LoginWithOTP(r.Context(), input.Email, input.Password, subject, input.OTPCode)
	} else {
		session, err = e.auth.Login(r.Context(), input.Email, input.Password, subject)
	}
	if err != nil {
		outcome = adminAuthOutcome(err)
		writeAdminAuthError(w, r, err)
		return
	}
	outcome = observability.OutcomeSuccess
	e.writeIssuedSession(w, r, http.StatusOK, session)
}

func (e adminEndpoints) recordOperation(ctx context.Context, operation observability.Operation, outcome observability.Outcome, duration time.Duration) {
	if e.recorder == nil {
		return
	}
	e.recorder.Record(ctx, observability.OperationEvent(operation, outcome, duration))
}

func adminAuthOutcome(err error) observability.Outcome {
	switch {
	case err == nil:
		return observability.OutcomeSuccess
	case errors.Is(err, adminauth.ErrValidation):
		return observability.OutcomeValidation
	case errors.Is(err, adminauth.ErrInvalidCredentials):
		return observability.OutcomeInvalidCredentials
	case errors.Is(err, adminauth.ErrSetupUnavailable):
		return observability.OutcomeSetupUnavailable
	case errors.Is(err, adminauth.ErrRateLimited):
		return observability.OutcomeRateLimited
	case errors.Is(err, adminauth.ErrConflict):
		return observability.OutcomeConflict
	}
	switch database.Classify(err) {
	case database.KindValidation:
		return observability.OutcomeValidation
	case database.KindConflict:
		return observability.OutcomeConflict
	case database.KindUnavailable:
		return observability.OutcomeUnavailable
	case database.KindCanceled:
		return observability.OutcomeCanceled
	default:
		return observability.OutcomeFailure
	}
}

func (e adminEndpoints) currentSession(w http.ResponseWriter, r *http.Request) {
	principal, ok := AdminPrincipalFromContext(r.Context())
	if !ok {
		writeAPIError(w, r, APIError{Status: http.StatusUnauthorized, Code: "authentication_required", Message: "Administrator authentication is required"})
		return
	}
	writeJSON(w, http.StatusOK, sessionResponse(principal.Administrator, principal.ExpiresAt, principal.CSRFToken, e.sqlConsole))
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
		Name:     adminCookieNameForRequest(r, e.publicURL, currentTrustedProxies(e.trustedProxies)),
		Value:    "",
		Path:     "/",
		Expires:  time.Unix(1, 0).UTC(),
		MaxAge:   -1,
		HttpOnly: true,
		Secure:   secureAdminCookie(r, e.publicURL, currentTrustedProxies(e.trustedProxies)),
		SameSite: http.SameSiteLaxMode,
	})
	w.WriteHeader(http.StatusNoContent)
}

func (e adminEndpoints) writeIssuedSession(w http.ResponseWriter, r *http.Request, status int, session adminauth.Session) {
	http.SetCookie(w, &http.Cookie{
		Name:     adminCookieNameForRequest(r, e.publicURL, currentTrustedProxies(e.trustedProxies)),
		Value:    session.RawToken,
		Path:     "/",
		Expires:  session.ExpiresAt.UTC(),
		HttpOnly: true,
		Secure:   secureAdminCookie(r, e.publicURL, currentTrustedProxies(e.trustedProxies)),
		SameSite: http.SameSiteLaxMode,
	})
	writeJSON(w, status, sessionResponse(session.Administrator, session.ExpiresAt, session.CSRFToken, e.sqlConsole))
}

func sessionResponse(administrator adminauth.Administrator, expiresAt time.Time, csrfToken string, sqlConsole bool) adminSessionResponse {
	return adminSessionResponse{
		Administrator: administratorResponse{
			ID:        administrator.ID,
			Email:     administrator.Email,
			Active:    administrator.Active,
			CreatedAt: administrator.CreatedAt.UTC(),
			UpdatedAt: administrator.UpdatedAt.UTC(),
		},
		CSRFToken:         csrfToken,
		ExpiresAt:         expiresAt.UTC(),
		SQLConsoleEnabled: sqlConsole,
	}
}

func decodeStrictJSON(r *http.Request, destination any) error {
	mediaType, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || mediaType != "application/json" {
		return errors.New("request content type is not application/json")
	}
	raw, err := io.ReadAll(r.Body)
	if err != nil {
		return err
	}
	if err := jsonvalue.ValidateUnicode(raw); err != nil {
		return err
	}
	if err := rejectDuplicateJSONKeys(raw); err != nil {
		return err
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
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

// rejectDuplicateJSONKeys walks the token stream and rejects an object that
// declares the same key twice, so a request cannot rely on last-key-wins
// parsing to smuggle contradictory values.
func rejectDuplicateJSONKeys(raw []byte) error {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	type frame struct {
		object    bool
		keys      map[string]struct{}
		expectKey bool
	}
	var stack []*frame
	for {
		token, err := decoder.Token()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return err
		}
		switch token := token.(type) {
		case json.Delim:
			switch token {
			case '{':
				stack = append(stack, &frame{object: true, keys: map[string]struct{}{}, expectKey: true})
			case '[':
				stack = append(stack, &frame{})
			default:
				if len(stack) > 0 {
					stack = stack[:len(stack)-1]
				}
				// A closed composite was a value in the parent object, so the
				// parent expects its next key again.
				if len(stack) > 0 && stack[len(stack)-1].object && !stack[len(stack)-1].expectKey {
					stack[len(stack)-1].expectKey = true
				}
			}
		case string:
			if len(stack) == 0 {
				continue
			}
			top := stack[len(stack)-1]
			if !top.object {
				continue
			}
			if top.expectKey {
				if _, duplicate := top.keys[token]; duplicate {
					return fmt.Errorf("request body contains duplicate JSON key %q", token)
				}
				top.keys[token] = struct{}{}
				top.expectKey = false
			} else {
				top.expectKey = true
			}
		default:
			if len(stack) > 0 && stack[len(stack)-1].object && !stack[len(stack)-1].expectKey {
				stack[len(stack)-1].expectKey = true
			}
		}
	}
}

func writeAdminAuthError(w http.ResponseWriter, r *http.Request, err error) {
	apiError := APIError{Status: http.StatusInternalServerError, Code: "internal_error", Message: "Internal server error"}
	switch {
	case errors.Is(err, adminauth.ErrValidation):
		apiError = APIError{Status: http.StatusBadRequest, Code: "validation_failed", Message: "Authentication input is invalid"}
	case errors.Is(err, adminauth.ErrInvalidCredentials):
		apiError = APIError{Status: http.StatusUnauthorized, Code: "invalid_credentials", Message: "Invalid administrator credentials"}
	case errors.Is(err, adminauth.ErrOTPRequired):
		apiError = APIError{Status: http.StatusUnauthorized, Code: "otp_required", Message: "Administrator OTP is required"}
	case errors.Is(err, adminauth.ErrSetupUnavailable):
		apiError = APIError{Status: http.StatusConflict, Code: "setup_unavailable", Message: "Administrator setup is unavailable"}
	case errors.Is(err, adminauth.ErrRateLimited):
		w.Header().Set("Retry-After", authenticationRetryAfter)
		apiError = APIError{Status: http.StatusTooManyRequests, Code: "rate_limited", Message: "Too many authentication attempts"}
	case database.Classify(err) == database.KindUnavailable:
		w.Header().Set("Retry-After", authenticationRetryAfter)
		apiError = APIError{Status: http.StatusServiceUnavailable, Code: "authentication_unavailable", Message: "Administrator authentication is unavailable"}
	}
	writeAPIError(w, r, apiError)
}
