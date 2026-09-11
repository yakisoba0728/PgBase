//go:build integration

package pgbase

import (
	"crypto/hmac"
	"crypto/sha1"
	"crypto/sha256"
	"encoding/base32"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestPublicTOTPEnrollmentChallengeRecoveryAndReplayHTTP(t *testing.T) {
	app := newPublicUsersApp(t, true)
	session := registerPublicUser(t, app, "totp-http@example.test", publicUserOnePass)
	enrolled := publicUsersRequest(t, app, http.MethodPost, "/api/v1/auth/totp/enroll", map[string]any{"currentPassword": publicUserOnePass}, session.Token, nil, nil)
	assertPublicUsersStatus(t, enrolled, http.StatusOK)
	var enrollment struct {
		Secret     string `json:"secret"`
		OTPAuthURL string `json:"otpAuthUrl"`
	}
	decodePublicUsersJSON(t, enrolled, &enrollment)
	if enrollment.Secret == "" || !strings.HasPrefix(enrollment.OTPAuthURL, "otpauth://totp/") {
		t.Fatalf("enrollment=%+v", enrollment)
	}
	secret, err := base32.StdEncoding.WithPadding(base32.NoPadding).DecodeString(enrollment.Secret)
	if err != nil {
		t.Fatal(err)
	}
	confirmed := publicUsersRequest(t, app, http.MethodPost, "/api/v1/auth/totp/confirm", map[string]any{"code": httpTOTPCode(secret, time.Now().Unix()/30)}, session.Token, nil, nil)
	assertPublicUsersStatus(t, confirmed, http.StatusOK)
	var recovery struct {
		Codes []string `json:"codes"`
	}
	decodePublicUsersJSON(t, confirmed, &recovery)
	if len(recovery.Codes) < 1 {
		t.Fatal("no recovery codes")
	}
	revoked := publicUsersRequest(t, app, http.MethodGet, "/api/v1/auth/session", nil, session.Token, nil, nil)
	assertPublicUsersStatus(t, revoked, http.StatusUnauthorized)
	login := publicUsersRequest(t, app, http.MethodPost, "/api/v1/auth/login", map[string]any{"email": session.User.Email, "password": publicUserOnePass}, "", nil, nil)
	assertPublicUsersStatus(t, login, http.StatusUnauthorized)
	var challenge struct {
		Error struct {
			Code    string              `json:"code"`
			Details map[string][]string `json:"details"`
		} `json:"error"`
	}
	decodePublicUsersJSON(t, login, &challenge)
	tokens := challenge.Error.Details["challengeToken"]
	if challenge.Error.Code != "mfa_required" || len(tokens) != 1 || tokens[0] == "" {
		t.Fatalf("challenge=%+v", challenge)
	}
	completed := publicUsersRequest(t, app, http.MethodPost, "/api/v1/auth/mfa/complete", map[string]any{"challengeToken": tokens[0], "code": recovery.Codes[0]}, "", nil, nil)
	assertPublicUsersStatus(t, completed, http.StatusOK)
	var completedSession publicUserSession
	decodePublicUsersJSON(t, completed, &completedSession)
	if completedSession.Token == "" || completedSession.User.ID != session.User.ID {
		t.Fatalf("completed session=%+v", completedSession)
	}
	replay := publicUsersRequest(t, app, http.MethodPost, "/api/v1/auth/mfa/complete", map[string]any{"challengeToken": tokens[0], "code": recovery.Codes[0]}, "", nil, nil)
	assertPublicUsersStatus(t, replay, http.StatusUnauthorized)

	secondLogin := publicUsersRequest(t, app, http.MethodPost, "/api/v1/auth/login", map[string]any{"email": session.User.Email, "password": publicUserOnePass}, "", nil, nil)
	assertPublicUsersStatus(t, secondLogin, http.StatusUnauthorized)
	var secondChallenge struct {
		Error struct {
			Details map[string][]string `json:"details"`
		} `json:"error"`
	}
	decodePublicUsersJSON(t, secondLogin, &secondChallenge)
	secondTokens := secondChallenge.Error.Details["challengeToken"]
	if len(secondTokens) != 1 || len(recovery.Codes) < 2 {
		t.Fatalf("second challenge=%+v recovery codes=%d", secondChallenge, len(recovery.Codes))
	}
	changed := publicUsersRequest(t, app, http.MethodPost, "/api/v1/auth/change-password", map[string]any{"currentPassword": publicUserOnePass, "newPassword": publicUserTwoPass}, completedSession.Token, nil, nil)
	assertPublicUsersStatus(t, changed, http.StatusNoContent)
	staleChallenge := publicUsersRequest(t, app, http.MethodPost, "/api/v1/auth/mfa/complete", map[string]any{"challengeToken": secondTokens[0], "code": recovery.Codes[1]}, "", nil, nil)
	assertPublicUsersStatus(t, staleChallenge, http.StatusUnauthorized)
}

func TestPublicOAuthPKCEStateExchangeSessionAndReplayHTTP(t *testing.T) {
	var mu sync.Mutex
	var challenge string
	tokenCalls := 0
	providerMux := http.NewServeMux()
	provider := httptest.NewServer(providerMux)
	defer provider.Close()
	providerMux.HandleFunc("/token", func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseForm(); err != nil {
			http.Error(w, "bad", 400)
			return
		}
		sum := sha256.Sum256([]byte(r.Form.Get("code_verifier")))
		computed := base64.RawURLEncoding.EncodeToString(sum[:])
		mu.Lock()
		expected := challenge
		tokenCalls++
		mu.Unlock()
		if r.Form.Get("code") != "local-code" || r.Form.Get("redirect_uri") != "http://localhost/oauth/callback" || computed != expected {
			http.Error(w, "bad", 400)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]string{"access_token": "local-access", "token_type": "Bearer"})
	})
	providerMux.HandleFunc("/userinfo", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer local-access" {
			http.Error(w, "bad", 401)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"sub": "oauth-subject", "email": "oauth-http@example.test", "email_verified": true})
	})
	app := newPublicUsersAppOptions(t, true, func(options *Options) {
		options.OAuthProviders = []OAuthProviderOptions{{Name: "local", AuthorizationURL: provider.URL + "/authorize", TokenURL: provider.URL + "/token", UserInfoURL: provider.URL + "/userinfo", ClientID: "local-client", ClientSecret: "local-secret", Scopes: []string{"openid", "email"}, AllowInsecureLoopback: true}}
		options.OAuthRedirects = []string{"http://localhost/oauth/callback"}
	})
	cookie, csrf := setupPublicUsersAdministrator(t, app)
	realmID := uuid.New()
	created := publicUsersRequest(t, app, http.MethodPost, "/api/admin/auth-collections", map[string]any{"id": realmID.String(), "name": "oauth-customers", "registrationEnabled": true}, "", cookie, map[string]string{"X-PgBase-CSRF": csrf})
	assertPublicUsersStatus(t, created, http.StatusCreated)
	methods := publicUsersRequest(t, app, http.MethodGet, "/api/v1/auth/oauth-customers/methods", nil, "", nil, nil)
	assertPublicUsersStatus(t, methods, http.StatusOK)
	var methodResponse struct {
		OAuth2 struct {
			Enabled   bool `json:"enabled"`
			Providers []struct {
				Name          string `json:"name"`
				StartEndpoint string `json:"startEndpoint"`
			} `json:"providers"`
		} `json:"oauth2"`
	}
	decodePublicUsersJSON(t, methods, &methodResponse)
	if !methodResponse.OAuth2.Enabled || len(methodResponse.OAuth2.Providers) != 1 || methodResponse.OAuth2.Providers[0].Name != "local" {
		t.Fatalf("methods=%s", methods.Body.String())
	}
	start := publicUsersRequest(t, app, http.MethodPost, methodResponse.OAuth2.Providers[0].StartEndpoint, map[string]any{"provider": methodResponse.OAuth2.Providers[0].Name, "redirectUri": "http://localhost/oauth/callback"}, "", nil, nil)
	assertPublicUsersStatus(t, start, http.StatusOK)
	var authorization struct {
		AuthorizationURL    string `json:"authorizationUrl"`
		State               string `json:"state"`
		CodeVerifier        string `json:"codeVerifier"`
		CodeChallenge       string `json:"codeChallenge"`
		CodeChallengeMethod string `json:"codeChallengeMethod"`
	}
	decodePublicUsersJSON(t, start, &authorization)
	parsed, err := url.Parse(authorization.AuthorizationURL)
	if err != nil {
		t.Fatal(err)
	}
	query := parsed.Query()
	state := query.Get("state")
	mu.Lock()
	challenge = query.Get("code_challenge")
	mu.Unlock()
	if state == "" || challenge == "" || query.Get("code_challenge_method") != "S256" || authorization.State != state || authorization.CodeVerifier == "" || authorization.CodeChallenge != challenge || authorization.CodeChallengeMethod != "S256" {
		t.Fatalf("authorization query=%v", query)
	}
	complete := publicUsersRequest(t, app, http.MethodPost, "/api/v1/auth/oauth/complete", map[string]any{"provider": "local", "state": state, "code": "local-code"}, "", nil, nil)
	assertPublicUsersStatus(t, complete, http.StatusOK)
	var session publicUserSession
	decodePublicUsersJSON(t, complete, &session)
	if session.Token == "" || session.User.Email != "oauth-http@example.test" {
		t.Fatalf("OAuth session=%+v", session)
	}
	current := publicUsersRequest(t, app, http.MethodGet, "/api/v1/auth/oauth-customers/session", nil, session.Token, nil, nil)
	assertPublicUsersStatus(t, current, http.StatusOK)
	globalSession := publicUsersRequest(t, app, http.MethodGet, "/api/v1/auth/session", nil, session.Token, nil, nil)
	assertPublicUsersStatus(t, globalSession, http.StatusOK)
	wrongRealm := publicUsersRequest(t, app, http.MethodGet, "/api/v1/auth/users/session", nil, session.Token, nil, nil)
	assertPublicUsersStatus(t, wrongRealm, http.StatusUnauthorized)
	replay := publicUsersRequest(t, app, http.MethodPost, "/api/v1/auth/oauth/complete", map[string]any{"provider": "local", "state": state, "code": "local-code"}, "", nil, nil)
	assertPublicUsersStatus(t, replay, http.StatusUnauthorized)
	mu.Lock()
	calls := tokenCalls
	mu.Unlock()
	if calls != 1 {
		t.Fatalf("provider token exchanges=%d", calls)
	}
}

func httpTOTPCode(secret []byte, counter int64) string {
	var value [8]byte
	binary.BigEndian.PutUint64(value[:], uint64(counter))
	mac := hmac.New(sha1.New, secret)
	_, _ = mac.Write(value[:])
	sum := mac.Sum(nil)
	offset := sum[len(sum)-1] & 15
	code := (uint32(sum[offset])&0x7f)<<24 | uint32(sum[offset+1])<<16 | uint32(sum[offset+2])<<8 | uint32(sum[offset+3])
	return fmt.Sprintf("%06d", code%1_000_000)
}
