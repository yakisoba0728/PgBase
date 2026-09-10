//go:build integration

package userauth

import (
	"context"
	"encoding/base32"
	"errors"
	"net/url"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/yakisoba0728/PgBase/internal/adminauth"
	"github.com/yakisoba0728/PgBase/internal/identity"
	"github.com/yakisoba0728/PgBase/internal/schema"
)

func setAuthRuleForTest(t *testing.T, service *Service, collectionID uuid.UUID, rule string) {
	t.Helper()
	if _, err := service.pool.Exec(context.Background(), `update pgbase_meta.auth_collections set auth_rule=$2,updated_at=CURRENT_TIMESTAMP where id=$1`, collectionID, rule); err != nil {
		t.Fatal(err)
	}
}

func TestAuthRuleGuardsPasswordWithCurrentCustomRecord(t *testing.T) {
	service := NewService(newUserAuthPool(t), adminauth.NewArgon2idPasswords(), []byte("0123456789abcdef0123456789abcdef"), time.Hour)
	ctx := context.Background()
	realmID := uuid.New()
	_, err := service.CreateAuthCollection(ctx, CreateAuthCollectionInput{
		ID: realmID, Name: "auth_rule_" + realmID.String()[:8], RegistrationEnabled: true,
		Fields: []schema.FieldInput{{Name: "score", Type: schema.TypeInteger}},
		Rules:  AuthCollectionRules{Create: "true"},
	})
	if err != nil {
		t.Fatal(err)
	}
	registered, err := service.RegisterInCollection(ctx, realmID, "rule@example.test", "correct horse battery staple", "register")
	if err != nil {
		t.Fatal(err)
	}
	setAuthRuleForTest(t, service, realmID, "active = true && score >= 2")
	if _, err := service.SetProfile(ctx, registered.User.ID, map[string]any{"score": float64(1)}); err != nil {
		t.Fatal(err)
	}
	if _, err := service.LoginInCollection(ctx, realmID, registered.User.Email, "correct horse battery staple", "login-deny"); !errors.Is(err, ErrInvalidCredentials) {
		t.Fatalf("password authRule refusal=%v", err)
	}
	if _, err := service.SetProfile(ctx, registered.User.ID, map[string]any{"score": float64(2)}); err != nil {
		t.Fatal(err)
	}
	allowed := false
	err = service.pool.InTx(ctx, pgx.TxOptions{}, func(tx pgx.Tx) error {
		var evaluateErr error
		allowed, evaluateErr = EvaluateAuthRecordRuleInTx(ctx, tx, realmID, "active = true && score >= 2", AuthRuleContext{Principal: identity.Subject{Kind: identity.User, ID: registered.User.ID}, Current: AuthRecord{ID: registered.User.ID}})
		return evaluateErr
	})
	if err != nil || !allowed {
		t.Fatalf("direct authRule evaluation allowed=%v err=%v", allowed, err)
	}
	if _, err := service.LoginInCollection(ctx, realmID, registered.User.Email, "correct horse battery staple", "login-allow"); err != nil {
		t.Fatalf("password authRule allow=%v", err)
	}
}

func TestAuthRuleGuardsOAuthWithoutConsumingState(t *testing.T) {
	service := NewService(newUserAuthPool(t), adminauth.NewArgon2idPasswords(), []byte("0123456789abcdef0123456789abcdef"), time.Hour)
	ctx := context.Background()
	provider := realmOAuthProvider{identity: OAuthIdentity{Subject: "auth-rule-subject", Email: "oauth-rule@example.test", EmailVerified: true}}
	redirect := "https://app.example/oauth/callback"
	if err := service.ConfigureOAuth(map[string]OAuthProvider{"local": provider}, []string{redirect}, time.Minute); err != nil {
		t.Fatal(err)
	}
	service.ConfigureOAuthRegistration(true)
	authorization, err := service.BeginOAuth(ctx, "local", redirect, "oauth-rule")
	if err != nil {
		t.Fatal(err)
	}
	parsed, _ := url.Parse(authorization.AuthorizationURL)
	state := parsed.Query().Get("state")
	setAuthRuleForTest(t, service, DefaultCollectionID, "false")
	if _, err := service.CompleteOAuth(ctx, "local", state, "code"); !errors.Is(err, ErrInvalidCredentials) {
		t.Fatalf("OAuth authRule refusal=%v", err)
	}
	var consumed bool
	if err := service.pool.QueryRow(ctx, `select consumed_at is not null from pgbase_meta.user_oauth_states where state_hash=$1`, service.oauthStateHash("local", state)).Scan(&consumed); err != nil || consumed {
		t.Fatalf("denied OAuth state consumed=%v err=%v", consumed, err)
	}
	var users int
	if err := service.pool.QueryRow(ctx, `select count(*) from pgbase_meta.users where email=$1`, provider.identity.Email).Scan(&users); err != nil || users != 0 {
		t.Fatalf("denied OAuth registration persisted users=%d err=%v", users, err)
	}
	setAuthRuleForTest(t, service, DefaultCollectionID, "true")
	if _, err := service.CompleteOAuth(ctx, "local", state, "code"); err != nil {
		t.Fatalf("OAuth retry after rule allow=%v", err)
	}
}

func TestAuthRuleGuardsOTPAndRefreshWithoutConsumingCredential(t *testing.T) {
	service, sender := newEmailActionService(t)
	ctx := context.Background()
	registered, err := service.Register(ctx, "otp-rule@example.test", "correct horse battery staple", "register")
	if err != nil {
		t.Fatal(err)
	}
	policy, err := service.authPolicy(ctx, DefaultCollectionID)
	if err != nil {
		t.Fatal(err)
	}
	policy.OTPEnabled = true
	if _, err := service.SetAuthPolicy(ctx, DefaultCollectionID, policy); err != nil {
		t.Fatal(err)
	}
	request, err := service.RequestEmailOTP(ctx, registered.User.Email, "otp-rule")
	if err != nil {
		t.Fatal(err)
	}
	code := otpFromMessage(t, sender)
	setAuthRuleForTest(t, service, DefaultCollectionID, "false")
	if _, err := service.CompleteEmailOTP(ctx, request.OTPID, code); !errors.Is(err, ErrInvalidCredentials) {
		t.Fatalf("OTP authRule refusal=%v", err)
	}
	var consumed bool
	if err := service.pool.QueryRow(ctx, `select consumed_at is not null from pgbase_meta.user_email_otps where id=$1`, request.OTPID).Scan(&consumed); err != nil || consumed {
		t.Fatalf("denied OTP consumed=%v err=%v", consumed, err)
	}
	if _, err := service.RefreshSessionForCollection(ctx, DefaultCollectionID, registered.RawToken); !errors.Is(err, ErrInvalidCredentials) {
		t.Fatalf("refresh authRule refusal=%v", err)
	}
	if _, err := service.Authenticate(ctx, registered.RawToken); err != nil {
		t.Fatalf("denied refresh invalidated old token: %v", err)
	}
	setAuthRuleForTest(t, service, DefaultCollectionID, "true")
	if _, err := service.CompleteEmailOTP(ctx, request.OTPID, code); err != nil {
		t.Fatalf("OTP retry after rule allow=%v", err)
	}
	if _, err := service.RefreshSessionForCollection(ctx, DefaultCollectionID, registered.RawToken); err != nil {
		t.Fatalf("refresh after rule allow=%v", err)
	}
}

func TestAuthRuleGuardsMFAWithoutConsumingChallengeOrRecoveryCode(t *testing.T) {
	service := NewService(newUserAuthPool(t), adminauth.NewArgon2idPasswords(), []byte("0123456789abcdef0123456789abcdef"), time.Hour)
	ctx := context.Background()
	registered, err := service.Register(ctx, "mfa-rule@example.test", "correct horse battery staple", "register")
	if err != nil {
		t.Fatal(err)
	}
	enrollment, err := service.BeginTOTPEnrollment(ctx, registered.User.ID, "correct horse battery staple")
	if err != nil {
		t.Fatal(err)
	}
	secret, _ := base32.StdEncoding.WithPadding(base32.NoPadding).DecodeString(enrollment.Secret)
	now, _ := service.databaseNow(ctx)
	recovery, err := service.ConfirmTOTPEnrollment(ctx, registered.User.ID, totpCode(secret, now.Unix()/30))
	if err != nil {
		t.Fatal(err)
	}
	_, err = service.Login(ctx, registered.User.Email, "correct horse battery staple", "mfa-rule")
	var required *MFARequiredError
	if !errors.As(err, &required) {
		t.Fatalf("MFA challenge=%v", err)
	}
	setAuthRuleForTest(t, service, DefaultCollectionID, "false")
	if _, err := service.CompleteMFA(ctx, required.ChallengeToken, recovery.Codes[0]); !errors.Is(err, ErrInvalidCredentials) {
		t.Fatalf("MFA authRule refusal=%v", err)
	}
	setAuthRuleForTest(t, service, DefaultCollectionID, "true")
	if _, err := service.CompleteMFA(ctx, required.ChallengeToken, recovery.Codes[0]); err != nil {
		t.Fatalf("MFA retry after rule allow=%v", err)
	}
}

func TestAuxiliaryOAuthDeletionCannotRaceAwayLastSignInMethod(t *testing.T) {
	service := NewService(newUserAuthPool(t), adminauth.NewArgon2idPasswords(), []byte("0123456789abcdef0123456789abcdef"), time.Hour)
	ctx := context.Background()
	registered, err := service.Register(ctx, "unlink-rule@example.test", "correct horse battery staple", "register")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.pool.Exec(ctx, `update pgbase_meta.users set password_enabled=false where id=$1`, registered.User.ID); err != nil {
		t.Fatal(err)
	}
	ids := []uuid.UUID{uuid.New(), uuid.New()}
	for i, id := range ids {
		if _, err := service.pool.Exec(ctx, `insert into pgbase_meta.user_oauth_identities(id,auth_collection_id,provider,subject,user_id,created_at) values($1,$2,$3,$4,$5,CURRENT_TIMESTAMP)`, id, DefaultCollectionID, "provider"+string(rune('a'+i)), uuid.NewString(), registered.User.ID); err != nil {
			t.Fatal(err)
		}
	}
	var wg sync.WaitGroup
	results := make(chan error, 2)
	for _, id := range ids {
		wg.Add(1)
		go func(identityID uuid.UUID) {
			defer wg.Done()
			results <- service.DeleteAuthAuxiliary(ctx, DefaultCollectionID, registered.User.ID, "externalAuths", identityID)
		}(id)
	}
	wg.Wait()
	close(results)
	var succeeded, refused int
	for err := range results {
		switch {
		case err == nil:
			succeeded++
		case errors.Is(err, ErrValidation):
			refused++
		default:
			t.Fatalf("concurrent auxiliary unlink=%v", err)
		}
	}
	if succeeded != 1 || refused != 1 {
		t.Fatalf("concurrent auxiliary unlink success/refused=%d/%d", succeeded, refused)
	}
	var remaining int
	if err := service.pool.QueryRow(ctx, `select count(*) from pgbase_meta.user_oauth_identities where user_id=$1`, registered.User.ID).Scan(&remaining); err != nil || remaining != 1 {
		t.Fatalf("remaining identities=%d err=%v", remaining, err)
	}
	policy, err := service.authPolicy(ctx, DefaultCollectionID)
	if err != nil {
		t.Fatal(err)
	}
	policy.OTPEnabled = true
	if _, err := service.SetAuthPolicy(ctx, DefaultCollectionID, policy); err != nil {
		t.Fatal(err)
	}
	var lastID uuid.UUID
	if err := service.pool.QueryRow(ctx, `select id from pgbase_meta.user_oauth_identities where user_id=$1`, registered.User.ID).Scan(&lastID); err != nil {
		t.Fatal(err)
	}
	if err := service.DeleteAuthAuxiliary(ctx, DefaultCollectionID, registered.User.ID, "externalAuths", lastID); err != nil {
		t.Fatalf("OTP alternative did not permit last OAuth unlink: %v", err)
	}
}
