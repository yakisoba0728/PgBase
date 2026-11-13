//go:build integration

package userauth

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/yakisoba0728/PgBase/internal/identity"
	"github.com/yakisoba0728/PgBase/internal/schema"
	"testing"
	"time"
)

func TestAuthClosureSystemAttributesAndRelationPrecision(t *testing.T) {
	ctx := context.Background()
	pool := newUserAuthPool(t)
	s := NewService(pool, stubPasswords{}, []byte("0123456789abcdef0123456789abcdef"), time.Hour)
	session, err := s.Register(ctx, "closure@example.test", "correct horse battery staple", "closure")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = pool.Exec(ctx, `insert into pgbase_meta.user_auth_profiles(user_id,attributes,updated_at) values($1,'{"verified":true,"email":"spoof@example.test","n":9007199254740993}',CURRENT_TIMESTAMP) on conflict(user_id) do update set attributes=excluded.attributes`, session.User.ID); err != nil {
		t.Fatal(err)
	}
	allowed, err := EvaluateAuthRecordRuleInTx(ctx, pool, DefaultCollectionID, "@request.auth.verified = true", AuthRuleContext{Principal: identity.Subject{Kind: identity.User, ID: session.User.ID}, Current: AuthRecord{ID: session.User.ID}})
	if err != nil || allowed {
		t.Errorf("profile spoof accepted=%v err=%v", allowed, err)
	}
	target := uuid.New()
	if _, err = pool.Exec(ctx, `insert into pgbase_meta.users(id,auth_collection_id,email,password_hash,active,created_at,updated_at) values($1,$2,'target@example.test','hash',true,CURRENT_TIMESTAMP,CURRENT_TIMESTAMP)`, target, DefaultCollectionID); err != nil {
		t.Fatal(err)
	}
	var name string
	if err = pool.QueryRow(ctx, `select name from pgbase_meta.auth_collections where id=$1`, DefaultCollectionID).Scan(&name); err != nil {
		t.Fatal(err)
	}
	fields, _ := json.Marshal([]schema.FieldInput{{Name: "parent", Type: schema.TypeRelation, Relation: &schema.RelationInput{TargetCollection: name, Cardinality: schema.RelationSingle, OnDelete: schema.RelationSetNull}}})
	if _, err = pool.Exec(ctx, `update pgbase_meta.auth_collections set fields_json=$2 where id=$1`, DefaultCollectionID, fields); err != nil {
		t.Fatal(err)
	}
	if _, err = pool.Exec(ctx, `update pgbase_meta.user_auth_profiles set attributes=attributes||jsonb_build_object('parent',$2::text) where user_id=$1`, session.User.ID, target.String()); err != nil {
		t.Fatal(err)
	}
	if err = s.DeleteAuthRecord(ctx, DefaultCollectionID, target); err != nil {
		t.Fatal(err)
	}
	var number string
	if err = pool.QueryRow(ctx, `select attributes->>'n' from pgbase_meta.user_auth_profiles where user_id=$1`, session.User.ID).Scan(&number); err != nil {
		t.Fatal(err)
	}
	if number != "9007199254740993" {
		t.Errorf("unrelated number corrupted: %s", number)
	}
}

func TestAuthClosureMFAConditionsAndCredentialInvalidation(t *testing.T) {
	ctx := context.Background()
	pool := newUserAuthPool(t)
	s := NewService(pool, stubPasswords{}, []byte("0123456789abcdef0123456789abcdef"), time.Hour)
	session, err := s.Register(ctx, "closure-mfa@example.test", "correct horse battery staple", "closure-mfa")
	if err != nil {
		t.Fatal(err)
	}
	secret := []byte("01234567890123456789")
	encrypted, err := s.encrypt("totp-secret", secret)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = pool.Exec(ctx, `insert into pgbase_meta.user_totp_factors(user_id,secret_ciphertext,active,created_at,updated_at) values($1,$2,true,CURRENT_TIMESTAMP,CURRENT_TIMESTAMP)`, session.User.ID, encrypted); err != nil {
		t.Fatal(err)
	}
	for _, pair := range [][2]string{{`{"tier":1}`, `{"field":"tier","equals":1.0}`}, {`{"tier":"a"}`, `{"field":"tier","equals":"\u0061"}`}} {
		if _, err = pool.Exec(ctx, `insert into pgbase_meta.user_auth_profiles(user_id,attributes,updated_at) values($1,$2,CURRENT_TIMESTAMP) on conflict(user_id) do update set attributes=excluded.attributes`, session.User.ID, pair[0]); err != nil {
			t.Fatal(err)
		}
		if _, err = pool.Exec(ctx, `update pgbase_meta.auth_collection_policies set mfa_enabled=true,mfa_rule=$2 where auth_collection_id=$1`, DefaultCollectionID, pair[1]); err != nil {
			t.Fatal(err)
		}
		err = pool.InTx(ctx, pgx.TxOptions{}, func(tx pgx.Tx) error {
			required, e := s.mfaRequiredTx(ctx, tx, session.User)
			if e == nil && !required {
				t.Errorf("equivalent MFA condition skipped: %s", pair[1])
			}
			return e
		})
		if err != nil {
			t.Fatal(err)
		}
		_, err = s.Login(ctx, session.User.Email, "correct horse battery staple", "condition-login")
		var required *MFARequiredError
		if !errors.As(err, &required) {
			t.Fatalf("login skipped semantic MFA condition: %v", err)
		}
	}
	var challenge *MFARequiredError
	if err = pool.InTx(ctx, pgx.TxOptions{}, func(tx pgx.Tx) error {
		var e error
		challenge, e = s.insertMFAChallenge(ctx, tx, session.User.ID, time.Now())
		return e
	}); err != nil {
		t.Fatal(err)
	}
	if _, err = pool.Exec(ctx, `update pgbase_meta.user_auth_profiles set attributes='{}' where user_id=$1`, session.User.ID); err != nil {
		t.Fatal(err)
	}
	password := "replacement password"
	if _, err = s.UpdateAuthRecord(ctx, DefaultCollectionID, session.User.ID, UpdateAuthRecordInput{Password: &password}); err != nil {
		t.Fatal(err)
	}
	var count int
	if err = pool.QueryRow(ctx, `select count(*) from pgbase_meta.user_mfa_challenges where token_hash=$1`, s.mfaChallengeHash(challenge.ChallengeToken)).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Error("credential replacement retained MFA challenge")
	}
	now, err := s.databaseNow(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.CompleteMFA(ctx, challenge.ChallengeToken, totpCode(secret, now.Unix()/30)); !errors.Is(err, ErrInvalidCredentials) {
		t.Fatalf("old password proof issued session: %v", err)
	}
	if err = pool.QueryRow(ctx, `select count(*) from pgbase_meta.user_sessions where user_id=$1 and revoked_at is null`, session.User.ID).Scan(&count); err != nil || count != 0 {
		t.Fatalf("post-change sessions=%d err=%v", count, err)
	}
	// Policy tightening must invalidate the same outstanding password proof.
	if err = pool.InTx(ctx, pgx.TxOptions{}, func(tx pgx.Tx) error {
		var e error
		challenge, e = s.insertMFAChallenge(ctx, tx, session.User.ID, time.Now())
		return e
	}); err != nil {
		t.Fatal(err)
	}
	administration, err := s.GetAuthAdministration(ctx, DefaultCollectionID)
	if err != nil {
		t.Fatal(err)
	}
	next := administration.Policy
	next.MinimumPasswordBytes++
	next.MFARule = ""
	if _, err = s.SetAuthPolicy(ctx, DefaultCollectionID, next); err != nil {
		t.Fatal(err)
	}
	if err = pool.QueryRow(ctx, `select count(*) from pgbase_meta.user_mfa_challenges where token_hash=$1`, s.mfaChallengeHash(challenge.ChallengeToken)).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Error("policy tightening retained MFA challenge")
	}
	// Unknown challenges must not allocate persistent rate-limit state.
	var before, after int
	if err = pool.QueryRow(ctx, `select count(*) from pgbase_meta.user_auth_rate_limits`).Scan(&before); err != nil {
		t.Fatal(err)
	}
	if _, err = s.CompleteMFA(ctx, "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA", "000000"); !errors.Is(err, ErrInvalidCredentials) {
		t.Fatal(err)
	}
	if err = pool.QueryRow(ctx, `select count(*) from pgbase_meta.user_auth_rate_limits`).Scan(&after); err != nil {
		t.Fatal(err)
	}
	if after != before {
		t.Error("unknown challenge allocated failure bucket")
	}
}

func TestAuthClosureOAuthFallbackAndProfileVisibility(t *testing.T) {
	ctx := context.Background()
	pool := newUserAuthPool(t)
	s := NewService(pool, stubPasswords{}, []byte("0123456789abcdef0123456789abcdef"), time.Hour)
	session, err := s.Register(ctx, "closure-oauth@example.test", "correct horse battery staple", "closure-oauth")
	if err != nil {
		t.Fatal(err)
	}
	if err = s.ConfigureOAuth(map[string]OAuthProvider{"local": realmOAuthProvider{}}, []string{"https://app.example/callback"}, time.Minute); err != nil {
		t.Fatal(err)
	}
	config := OAuthProviderConfig{Name: "local", Enabled: false, AuthorizationURL: "https://id.example/auth", TokenURL: "https://id.example/token", UserInfoURL: "https://id.example/info", ClientID: "client", RedirectURIs: []string{"https://app.example/callback"}, SubjectField: "sub", EmailField: "email", EmailVerifiedField: "email_verified"}
	if _, err = s.PutOAuthProvider(ctx, DefaultCollectionID, config); err != nil {
		t.Fatal(err)
	}
	if _, _, err = s.configuredOAuthProvider(ctx, DefaultCollectionID, "local"); !errors.Is(err, ErrValidation) {
		t.Errorf("disabled override=%v", err)
	}
	if _, err = pool.Exec(ctx, `insert into pgbase_meta.user_oauth_identities(id,auth_collection_id,user_id,provider,subject,created_at) values($1,$2,$3,'local','closure',CURRENT_TIMESTAMP)`, uuid.New(), DefaultCollectionID, session.User.ID); err != nil {
		t.Fatal(err)
	}
	if _, err = pool.Exec(ctx, `update pgbase_meta.auth_collection_policies set password_enabled=false,otp_enabled=false where auth_collection_id=$1`, DefaultCollectionID); err != nil {
		t.Fatal(err)
	}
	if err = s.UnlinkOAuthIdentity(ctx, session.User.ID, "local"); !errors.Is(err, ErrValidation) {
		t.Errorf("removed last usable method: %v", err)
	}
	fields, _ := json.Marshal([]schema.FieldInput{{Name: "secret", Type: schema.TypeText, Hidden: true}, {Name: "visible", Type: schema.TypeText}})
	if _, err = pool.Exec(ctx, `update pgbase_meta.auth_collections set fields_json=$2,update_rule='true' where id=$1`, DefaultCollectionID, fields); err != nil {
		t.Fatal(err)
	}
	if _, err = pool.Exec(ctx, `update pgbase_meta.user_auth_profiles set attributes='{"secret":"hidden","visible":"shown"}' where user_id=$1`, session.User.ID); err != nil {
		t.Fatal(err)
	}
	profile, err := s.GetProfile(ctx, session.User.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := profile["secret"]; ok {
		t.Error("GetProfile leaked hidden field")
	}
	profile, err = s.SetProfile(ctx, session.User.ID, map[string]any{"visible": "edited"})
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := profile["secret"]; ok {
		t.Error("SetProfile leaked hidden field")
	}
}

type closurePausingPasswords struct {
	entered, release chan struct{}
	hash             bool
}

func (p closurePausingPasswords) Hash(string) (string, error) {
	if p.hash {
		close(p.entered)
		<-p.release
	}
	return "hash", nil
}
func (p closurePausingPasswords) Verify(string, string) error {
	if !p.hash {
		close(p.entered)
		<-p.release
	}
	return nil
}

func TestAuthClosurePasswordPolicyRecheckedAfterKDF(t *testing.T) {
	for _, operation := range []string{"register", "login", "change"} {
		t.Run(operation, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			pool := newUserAuthPool(t)
			s := NewService(pool, stubPasswords{}, []byte("0123456789abcdef0123456789abcdef"), time.Hour)
			session, err := s.Register(ctx, "policy-closure@example.test", "correct horse battery staple", "closure-policy")
			if err != nil {
				t.Fatal(err)
			}
			paused := closurePausingPasswords{make(chan struct{}), make(chan struct{}), operation == "register"}
			s.passwords = paused
			result := make(chan error, 1)
			go func() {
				var err error
				switch operation {
				case "register":
					_, err = s.Register(ctx, "new-policy@example.test", "correct horse battery staple", "closure-policy-2")
				case "login":
					_, err = s.Login(ctx, session.User.Email, "correct horse battery staple", "closure-policy-2")
				case "change":
					err = s.ChangePassword(ctx, session.User.ID, "correct horse battery staple", "replacement password")
				}
				result <- err
			}()
			select {
			case <-paused.entered:
			case <-ctx.Done():
				t.Fatal(ctx.Err())
			}
			policy, err := s.GetAuthAdministration(ctx, DefaultCollectionID)
			if err != nil {
				t.Fatal(err)
			}
			next := policy.Policy
			next.MinimumPasswordBytes = 40
			if _, err = s.SetAuthPolicy(ctx, DefaultCollectionID, next); err != nil {
				t.Fatal(err)
			}
			close(paused.release)
			if err = <-result; err == nil {
				t.Error("stale password policy accepted after KDF")
			}
		})
	}
}

func TestAuthClosureMFAAccountBudgetSurvivesNewChallenges(t *testing.T) {
	ctx := context.Background()
	pool := newUserAuthPool(t)
	s := NewService(pool, stubPasswords{}, []byte("0123456789abcdef0123456789abcdef"), time.Hour)
	session, err := s.Register(ctx, "mfa-budget@example.test", "correct horse battery staple", "mfa-budget")
	if err != nil {
		t.Fatal(err)
	}
	encrypted, err := s.encrypt("totp-secret", []byte("01234567890123456789"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err = pool.Exec(ctx, `insert into pgbase_meta.user_totp_factors(user_id,secret_ciphertext,active,created_at,updated_at) values($1,$2,true,CURRENT_TIMESTAMP,CURRENT_TIMESTAMP)`, session.User.ID, encrypted); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < mfaFailureLimit; i++ {
		var challenge *MFARequiredError
		if err = pool.InTx(ctx, pgx.TxOptions{}, func(tx pgx.Tx) error {
			var e error
			challenge, e = s.insertMFAChallenge(ctx, tx, session.User.ID, time.Now())
			return e
		}); err != nil {
			t.Fatal(err)
		}
		_, err = s.CompleteMFA(ctx, challenge.ChallengeToken, "invalid")
		if err == nil {
			t.Fatal("wrong factor accepted")
		}
	}
	var challenge *MFARequiredError
	if err = pool.InTx(ctx, pgx.TxOptions{}, func(tx pgx.Tx) error {
		var e error
		challenge, e = s.insertMFAChallenge(ctx, tx, session.User.ID, time.Now())
		return e
	}); err != nil {
		t.Fatal(err)
	}
	if _, err = s.CompleteMFA(ctx, challenge.ChallengeToken, "invalid"); !errors.Is(err, ErrRateLimited) {
		t.Errorf("new challenge reset account budget: %v", err)
	}
	if err = s.DisableTOTP(ctx, session.User.ID, "correct horse battery staple", "invalid"); !errors.Is(err, ErrRateLimited) {
		t.Errorf("disable bypassed shared factor budget: %v", err)
	}
}

type closureCountingPasswords struct{ calls int }

func (p *closureCountingPasswords) Hash(string) (string, error) { p.calls++; return "hash", nil }
func (p *closureCountingPasswords) Verify(string, string) error { return nil }

func TestAuthClosureDeniedUpdateSkipsKDFAndAllowedUpdateUsesAdmission(t *testing.T) {
	ctx := context.Background()
	pool := newUserAuthPool(t)
	passwords := &closureCountingPasswords{}
	s := NewService(pool, passwords, []byte("0123456789abcdef0123456789abcdef"), time.Hour)
	session, err := s.Register(ctx, "kdf-closure@example.test", "correct horse battery staple", "kdf-closure")
	if err != nil {
		t.Fatal(err)
	}
	for _, rule := range []any{nil, "false"} {
		if _, err = pool.Exec(ctx, `update pgbase_meta.auth_collections set update_rule=$2,manage_rule='true' where id=$1`, DefaultCollectionID, rule); err != nil {
			t.Fatal(err)
		}
		passwords.calls = 0
		password := "replacement password"
		if _, err = s.UpdateAuthorizedAuthRecord(ctx, DefaultCollectionID, uuid.Nil, session.User.ID, UpdateAuthRecordInput{Password: &password}); !errors.Is(err, ErrInvalidCredentials) {
			t.Fatal(err)
		}
		if passwords.calls != 0 {
			t.Errorf("denied update performed %d hashes", passwords.calls)
		}
	}
	if _, err = pool.Exec(ctx, `update pgbase_meta.auth_collections set update_rule='true' where id=$1`, DefaultCollectionID); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < cap(s.passwordSlots); i++ {
		s.passwordSlots <- struct{}{}
	}
	password := "replacement password"
	passwords.calls = 0
	_, err = s.UpdateAuthorizedAuthRecord(ctx, DefaultCollectionID, session.User.ID, session.User.ID, UpdateAuthRecordInput{Password: &password})
	for i := 0; i < cap(s.passwordSlots); i++ {
		<-s.passwordSlots
	}
	if !errors.Is(err, ErrRateLimited) || passwords.calls != 0 {
		t.Errorf("saturated KDF admission bypass: hashes=%d err=%v", passwords.calls, err)
	}
}

func TestAuthClosurePairedRealmRelationRoundTrip(t *testing.T) {
	ctx := context.Background()
	pool := newUserAuthPool(t)
	s := NewService(pool, stubPasswords{}, []byte("0123456789abcdef0123456789abcdef"), time.Hour)
	session, err := s.Register(ctx, "paired@example.test", "correct horse battery staple", "paired")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = pool.Exec(ctx, `insert into pgbase_meta.collections(id,name,table_name,status,collection_type,created_at,updated_at) values($1,'users','users','active','auth',CURRENT_TIMESTAMP,CURRENT_TIMESTAMP)`, uuid.New()); err != nil {
		t.Fatal(err)
	}
	fields, _ := json.Marshal([]schema.FieldInput{{Name: "peer", Type: schema.TypeRelation, Relation: &schema.RelationInput{TargetCollection: "users", Cardinality: schema.RelationSingle}}})
	if _, err = pool.Exec(ctx, `update pgbase_meta.auth_collections set fields_json=$2,update_rule='true' where id=$1`, DefaultCollectionID, fields); err != nil {
		t.Fatal(err)
	}
	profile, err := s.SetProfile(ctx, session.User.ID, map[string]any{"peer": session.User.ID.String()})
	if err != nil {
		t.Fatal(err)
	}
	if profile["peer"] != session.User.ID.String() {
		t.Fatalf("paired relation=%v", profile)
	}
}

func TestAuthClosureAuxiliaryRatePolicyAndCleanup(t *testing.T) {
	ctx := context.Background()
	pool := newUserAuthPool(t)
	s := NewService(pool, stubPasswords{}, []byte("0123456789abcdef0123456789abcdef"), time.Hour)
	if err := s.ConfigureRatePolicy(RatePolicy{AccountMaxFailures: 2, NetworkMaxFailures: 2, RegistrationNetworkMaxFailures: 2, Window: time.Hour, Block: 2 * time.Hour}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.RequestEmailOTP(ctx, "absent@example.test", "rate-policy"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.RequestEmailOTP(ctx, "absent@example.test", "rate-policy"); !errors.Is(err, ErrRateLimited) {
		t.Errorf("auxiliary ignored configured failure limit: %v", err)
	}
	var remaining float64
	if err := pool.QueryRow(ctx, `select coalesce(extract(epoch from blocked_until-CURRENT_TIMESTAMP),0)::double precision from pgbase_meta.user_auth_rate_limits where subject_hash=$1`, s.subjectHash("email-otp-request", "network", "rate-policy")).Scan(&remaining); err != nil {
		t.Fatal(err)
	}
	if remaining < 7190 {
		t.Errorf("auxiliary ignored block duration: %v", remaining)
	}
	for i := 1; i <= 3; i++ {
		age := "45 minutes"
		if i == 3 {
			age = "3 hours"
		}
		if _, err := pool.Exec(ctx, `insert into pgbase_meta.user_auth_rate_limits(subject_hash,window_started_at,failures,blocked_until,updated_at) values($1,CURRENT_TIMESTAMP-$2::interval,1,case when $3 then CURRENT_TIMESTAMP+interval '1 hour' end,CURRENT_TIMESTAMP-$2::interval)`, s.subjectHash("cleanup", "fixture", string(rune('0'+i))), age, i == 1); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.maybeCleanup(ctx); err != nil {
		t.Fatal(err)
	}
	for i := 1; i <= 3; i++ {
		var count int
		if err := pool.QueryRow(ctx, `select count(*) from pgbase_meta.user_auth_rate_limits where subject_hash=$1`, s.subjectHash("cleanup", "fixture", string(rune('0'+i)))).Scan(&count); err != nil {
			t.Fatal(err)
		}
		want := 1
		if i == 3 {
			want = 0
		}
		if count != want {
			t.Errorf("cleanup fixture %d count=%d want=%d", i, count, want)
		}
	}
}
