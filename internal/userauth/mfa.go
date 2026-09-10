package userauth

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha1"
	"crypto/sha256"
	"encoding/base32"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// MFARule is deliberately small and deterministic: an empty rule applies MFA
// whenever an active factor exists; otherwise a declared profile field (or the
// built-in `verified`) must equal the supplied JSON scalar.  Unsupported
// expressions are rejected at configuration time rather than silently stored.
type mfaRule struct {
	Field  string          `json:"field"`
	Equals json.RawMessage `json:"equals"`
}

func validMFARule(raw string) bool {
	if raw == "" {
		return true
	}
	var r mfaRule
	if json.Unmarshal([]byte(raw), &r) != nil || (r.Field != "verified" && !authCollectionName.MatchString(r.Field)) || len(r.Equals) == 0 {
		return false
	}
	var scalar any
	if json.Unmarshal(r.Equals, &scalar) != nil {
		return false
	}
	switch scalar.(type) {
	case string, bool, float64, nil:
		return true
	}
	return false
}
func (s *Service) mfaRequiredTx(ctx context.Context, tx pgx.Tx, user User) (bool, error) {
	var policy AuthPolicy
	if err := tx.QueryRow(ctx, `select password_enabled,minimum_password_bytes,maximum_password_bytes,password_pattern,identity_fields,mfa_enabled,mfa_duration_seconds,mfa_rule,otp_enabled,otp_duration_seconds,otp_max_attempts from pgbase_meta.auth_collection_policies where auth_collection_id=$1`, user.AuthCollectionID).Scan(&policy.PasswordEnabled, &policy.MinimumPasswordBytes, &policy.MaximumPasswordBytes, &policy.PasswordPattern, &policy.IdentityFields, &policy.MFAEnabled, &policy.MFADurationSeconds, &policy.MFARule, &policy.OTPEnabled, &policy.OTPDurationSeconds, &policy.OTPMaxAttempts); err != nil {
		return false, err
	}
	if !policy.MFAEnabled {
		return false, nil
	}
	var active bool
	if err := tx.QueryRow(ctx, `select exists(select 1 from pgbase_meta.user_totp_factors where user_id=$1 and active)`, user.ID).Scan(&active); err != nil {
		return false, err
	}
	if !active {
		return false, nil
	}
	if policy.MFARule == "" {
		return true, nil
	}
	var rule mfaRule
	_ = json.Unmarshal([]byte(policy.MFARule), &rule)
	var got json.RawMessage
	if rule.Field == "verified" {
		got = []byte("false")
		if user.Verified {
			got = []byte("true")
		}
	} else {
		var profile []byte
		err := tx.QueryRow(ctx, `select attributes from pgbase_meta.user_auth_profiles where user_id=$1`, user.ID).Scan(&profile)
		if err != nil && !errors.Is(err, pgx.ErrNoRows) {
			return false, err
		}
		var values map[string]json.RawMessage
		_ = json.Unmarshal(profile, &values)
		got = values[rule.Field]
	}
	return string(got) == string(rule.Equals), nil
}

const (
	totpStep          = 30 * time.Second
	totpDigits        = 6
	mfaChallengeTTL   = 5 * time.Minute
	recoveryCodeCount = 10
	mfaFailureLimit   = 5
)

func (s *Service) BeginTOTPEnrollment(ctx context.Context, userID uuid.UUID, currentPassword string) (TOTPEnrollment, error) {
	if userID == uuid.Nil {
		return TOTPEnrollment{}, ErrValidation
	}
	var collectionID uuid.UUID
	if err := s.pool.QueryRow(ctx, `select auth_collection_id from pgbase_meta.users where id=$1`, userID).Scan(&collectionID); err != nil {
		return TOTPEnrollment{}, ErrNotFound
	}
	policy, err := s.authPolicy(ctx, collectionID)
	if err != nil || !policy.MFAEnabled {
		return TOTPEnrollment{}, ErrValidation
	}
	user, _, err := s.verifyUserPassword(ctx, userID, currentPassword, "totp-enroll")
	if err != nil {
		return TOTPEnrollment{}, err
	}
	secret := make([]byte, 20)
	if _, err := rand.Read(secret); err != nil {
		return TOTPEnrollment{}, fmt.Errorf("generate TOTP secret: %w", err)
	}
	ciphertext, err := s.encrypt("totp-secret", secret)
	if err != nil {
		return TOTPEnrollment{}, err
	}
	tag, err := s.pool.Exec(ctx, `insert into pgbase_meta.user_totp_factors(user_id,secret_ciphertext,active,last_counter,created_at,updated_at) values($1,$2,false,null,CURRENT_TIMESTAMP,CURRENT_TIMESTAMP) on conflict(user_id) do update set secret_ciphertext=excluded.secret_ciphertext,last_counter=null,updated_at=CURRENT_TIMESTAMP where not pgbase_meta.user_totp_factors.active`, user.ID, ciphertext)
	if err != nil {
		return TOTPEnrollment{}, fmt.Errorf("store TOTP enrollment: %w", err)
	}
	if tag.RowsAffected() != 1 {
		return TOTPEnrollment{}, ErrConflict
	}
	encoded := base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString(secret)
	query := url.Values{"secret": {encoded}, "issuer": {"PgBase"}, "digits": {strconv.Itoa(totpDigits)}, "period": {strconv.Itoa(int(totpStep.Seconds()))}}
	return TOTPEnrollment{Secret: encoded, OTPAuthURL: "otpauth://totp/PgBase:" + url.PathEscape(user.Email) + "?" + query.Encode()}, nil
}

func (s *Service) ConfirmTOTPEnrollment(ctx context.Context, userID uuid.UUID, code string) (RecoveryCodes, error) {
	if userID == uuid.Nil {
		return RecoveryCodes{}, ErrValidation
	}
	now, err := s.databaseNow(ctx)
	if err != nil {
		return RecoveryCodes{}, err
	}
	buckets := []rateLimitBucket{{hash: s.subjectHash("mfa-enroll", "account", userID.String()), limit: mfaFailureLimit}}
	if err := s.ensureBucketsNotBlocked(ctx, buckets, now); err != nil {
		return RecoveryCodes{}, err
	}
	var result RecoveryCodes
	var invalid bool
	err = s.pool.InTx(ctx, pgx.TxOptions{}, func(tx pgx.Tx) error {
		var encrypted []byte
		if err := tx.QueryRow(ctx, `select secret_ciphertext from pgbase_meta.user_totp_factors where user_id=$1 and not active for update`, userID).Scan(&encrypted); errors.Is(err, pgx.ErrNoRows) {
			return ErrNotFound
		} else if err != nil {
			return err
		}
		secret, err := s.decrypt("totp-secret", encrypted)
		if err != nil {
			return err
		}
		counter, ok := validateTOTP(secret, code, now, -1)
		if !ok {
			invalid = true
			return nil
		}
		codes, hashes, err := s.generateRecoveryCodes()
		if err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `update pgbase_meta.user_totp_factors set active=true,last_counter=$2,updated_at=CURRENT_TIMESTAMP where user_id=$1`, userID, counter); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `delete from pgbase_meta.user_totp_recovery_codes where user_id=$1`, userID); err != nil {
			return err
		}
		for _, hash := range hashes {
			if _, err := tx.Exec(ctx, `insert into pgbase_meta.user_totp_recovery_codes(user_id,code_hash,created_at) values($1,$2,CURRENT_TIMESTAMP)`, userID, hash); err != nil {
				return err
			}
		}
		if _, err := revokeSessionsInTx(ctx, tx, userID); err != nil {
			return err
		}
		result.Codes = codes
		return nil
	})
	if err == nil && invalid {
		if err := s.recordFailures(ctx, buckets, now); err != nil {
			return RecoveryCodes{}, err
		}
		return RecoveryCodes{}, ErrInvalidCredentials
	}
	return result, err
}

func (s *Service) DisableTOTP(ctx context.Context, userID uuid.UUID, currentPassword, code string) error {
	if _, _, err := s.verifyUserPassword(ctx, userID, currentPassword, "totp-disable"); err != nil {
		return err
	}
	now, err := s.databaseNow(ctx)
	if err != nil {
		return err
	}
	return s.pool.InTx(ctx, pgx.TxOptions{}, func(tx pgx.Tx) error {
		valid, err := s.consumeFactorCode(ctx, tx, userID, code, now)
		if err != nil {
			return err
		}
		if !valid {
			return ErrInvalidCredentials
		}
		if _, err := tx.Exec(ctx, `delete from pgbase_meta.user_totp_factors where user_id=$1`, userID); err != nil {
			return err
		}
		_, err = revokeSessionsInTx(ctx, tx, userID)
		return err
	})
}

func (s *Service) CompleteMFA(ctx context.Context, challengeToken, code string) (Session, error) {
	if err := s.ready(); err != nil {
		return Session{}, err
	}
	if validateToken(challengeToken) != nil || len(code) < 6 || len(code) > 32 {
		return Session{}, ErrInvalidCredentials
	}
	now, err := s.databaseNow(ctx)
	if err != nil {
		return Session{}, err
	}
	buckets := []rateLimitBucket{{hash: s.subjectHash("mfa", "challenge", challengeToken), limit: mfaFailureLimit}}
	if err := s.ensureBucketsNotBlocked(ctx, buckets, now); err != nil {
		return Session{}, err
	}
	var issued Session
	var invalid bool
	err = s.pool.InTx(ctx, pgx.TxOptions{}, func(tx pgx.Tx) error {
		var challengeID, userID uuid.UUID
		err := tx.QueryRow(ctx, `select id,user_id from pgbase_meta.user_mfa_challenges where token_hash=$1 and consumed_at is null and expires_at>CURRENT_TIMESTAMP`, s.mfaChallengeHash(challengeToken)).Scan(&challengeID, &userID)
		if errors.Is(err, pgx.ErrNoRows) {
			invalid = true
			return nil
		}
		if err != nil {
			return err
		}
		var user User
		if err := tx.QueryRow(ctx, `select id,auth_collection_id,email,verified,created_at from pgbase_meta.users where id=$1 and active for update`, userID).Scan(&user.ID, &user.AuthCollectionID, &user.Email, &user.Verified, &user.CreatedAt); errors.Is(err, pgx.ErrNoRows) {
			invalid = true
			return nil
		} else if err != nil {
			return err
		}
		// Credential-changing operations lock the user before invalidating all
		// outstanding challenges. Take locks in that same order, then recheck
		// this challenge so an older login cannot mint a post-change session.
		err = tx.QueryRow(ctx, `select id from pgbase_meta.user_mfa_challenges where id=$1 and user_id=$2 and consumed_at is null and expires_at>CURRENT_TIMESTAMP for update`, challengeID, userID).Scan(&challengeID)
		if errors.Is(err, pgx.ErrNoRows) {
			invalid = true
			return nil
		}
		if err != nil {
			return err
		}
		valid, err := s.consumeFactorCode(ctx, tx, userID, code, now)
		if err != nil {
			return err
		}
		if !valid {
			invalid = true
			return nil
		}
		if _, err := tx.Exec(ctx, `update pgbase_meta.user_mfa_challenges set consumed_at=CURRENT_TIMESTAMP where id=$1`, challengeID); err != nil {
			return err
		}
		issued, err = s.insertSession(ctx, tx, user, now)
		return err
	})
	if err != nil {
		return Session{}, err
	}
	if invalid {
		if err := s.recordFailures(ctx, buckets, now); err != nil {
			return Session{}, err
		}
		return Session{}, ErrInvalidCredentials
	}
	s.recordIssuedAuthOrigin(ctx, issued, now)
	return issued, nil
}

func (s *Service) insertMFAChallenge(ctx context.Context, tx pgx.Tx, userID uuid.UUID, now time.Time) (*MFARequiredError, error) {
	rawBytes := make([]byte, 32)
	if _, err := rand.Read(rawBytes); err != nil {
		return nil, err
	}
	raw := base64.RawURLEncoding.EncodeToString(rawBytes)
	// The collection policy controls the lifespan of the single-use challenge.
	// Falling back to the historic five minute value is intentionally avoided:
	// every user row has an auth collection and a policy row created by trigger.
	var durationSeconds int
	if err := tx.QueryRow(ctx, `select p.mfa_duration_seconds from pgbase_meta.users u join pgbase_meta.auth_collection_policies p on p.auth_collection_id=u.auth_collection_id where u.id=$1`, userID).Scan(&durationSeconds); err != nil {
		return nil, err
	}
	expires := now.Add(time.Duration(durationSeconds) * time.Second)
	_, err := tx.Exec(ctx, `insert into pgbase_meta.user_mfa_challenges(id,user_id,token_hash,expires_at,created_at) values($1,$2,$3,$4,$5)`, uuid.New(), userID, s.mfaChallengeHash(raw), expires, now)
	if err != nil {
		return nil, err
	}
	return &MFARequiredError{ChallengeToken: raw, ExpiresAt: expires}, nil
}

func (s *Service) verifyUserPassword(ctx context.Context, userID uuid.UUID, password, operation string) (ManagedUser, string, error) {
	if userID == uuid.Nil {
		return ManagedUser{}, "", ErrValidation
	}
	now, err := s.databaseNow(ctx)
	if err != nil {
		return ManagedUser{}, "", err
	}
	buckets := []rateLimitBucket{{hash: s.subjectHash(operation, "account", userID.String()), limit: loginAccountLimit}}
	if err := s.ensureBucketsNotBlocked(ctx, buckets, now); err != nil {
		return ManagedUser{}, "", err
	}
	user, hash, lookupErr := userPasswordByIDDB(ctx, s.pool, userID, false)
	release, err := admit(s.passwordSlots)
	if err != nil {
		return ManagedUser{}, "", err
	}
	valid := lookupErr == nil && user.Active && validatePassword(password) == nil
	verifyHash, verifyPassword := hash, password
	if !valid {
		verifyHash, verifyPassword = dummyPasswordHash, dummyPassword
	}
	verifyErr := s.passwords.Verify(verifyHash, verifyPassword)
	release()
	if verifyErr != nil && !isPasswordMismatch(verifyErr) {
		return ManagedUser{}, "", verifyErr
	}
	if !valid || verifyErr != nil {
		if err := s.recordFailures(ctx, buckets, now); err != nil {
			return ManagedUser{}, "", err
		}
		return ManagedUser{}, "", ErrInvalidCredentials
	}
	return user, hash, nil
}

func (s *Service) consumeFactorCode(ctx context.Context, tx pgx.Tx, userID uuid.UUID, code string, now time.Time) (bool, error) {
	var encrypted []byte
	var last *int64
	err := tx.QueryRow(ctx, `select secret_ciphertext,last_counter from pgbase_meta.user_totp_factors where user_id=$1 and active for update`, userID).Scan(&encrypted, &last)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	minimum := int64(-1)
	if last != nil {
		minimum = *last
	}
	secret, err := s.decrypt("totp-secret", encrypted)
	if err != nil {
		return false, err
	}
	counter, valid := validateTOTP(secret, code, now, minimum)
	if valid {
		_, err = tx.Exec(ctx, `update pgbase_meta.user_totp_factors set last_counter=$2,updated_at=CURRENT_TIMESTAMP where user_id=$1`, userID, counter)
		return err == nil, err
	}
	hash := s.recoveryHash(code)
	tag, err := tx.Exec(ctx, `update pgbase_meta.user_totp_recovery_codes set consumed_at=CURRENT_TIMESTAMP where user_id=$1 and code_hash=$2 and consumed_at is null`, userID, hash)
	return err == nil && tag.RowsAffected() == 1, err
}

func validateTOTP(secret []byte, code string, now time.Time, minimum int64) (int64, bool) {
	if len(code) != totpDigits {
		return 0, false
	}
	for _, r := range code {
		if r < '0' || r > '9' {
			return 0, false
		}
	}
	center := now.Unix() / int64(totpStep.Seconds())
	for offset := int64(-1); offset <= 1; offset++ {
		counter := center + offset
		if counter <= minimum {
			continue
		}
		if hmac.Equal([]byte(totpCode(secret, counter)), []byte(code)) {
			return counter, true
		}
	}
	return 0, false
}
func totpCode(secret []byte, counter int64) string {
	var b [8]byte
	binary.BigEndian.PutUint64(b[:], uint64(counter))
	m := hmac.New(sha1.New, secret)
	m.Write(b[:])
	sum := m.Sum(nil)
	o := sum[len(sum)-1] & 15
	value := (uint32(sum[o])&127)<<24 | uint32(sum[o+1])<<16 | uint32(sum[o+2])<<8 | uint32(sum[o+3])
	return fmt.Sprintf("%06d", value%1_000_000)
}
func (s *Service) generateRecoveryCodes() ([]string, [][]byte, error) {
	codes := make([]string, recoveryCodeCount)
	hashes := make([][]byte, recoveryCodeCount)
	for i := range codes {
		raw := make([]byte, 10)
		if _, err := rand.Read(raw); err != nil {
			return nil, nil, err
		}
		codes[i] = strings.ToLower(base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString(raw))
		hashes[i] = s.recoveryHash(codes[i])
	}
	return codes, hashes, nil
}
func (s *Service) mfaChallengeHash(raw string) []byte { return s.digest("user-mfa-challenge\x00", raw) }
func (s *Service) recoveryHash(raw string) []byte {
	return s.digest("user-mfa-recovery\x00", strings.ToLower(strings.TrimSpace(raw)))
}
func (s *Service) encrypt(domain string, plaintext []byte) ([]byte, error) {
	key := sha256.Sum256(s.digest("encryption-key\x00", domain))
	block, err := aes.NewCipher(key[:])
	if err != nil {
		return nil, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return nil, err
	}
	return gcm.Seal(nonce, nonce, plaintext, []byte(domain)), nil
}
func (s *Service) decrypt(domain string, value []byte) ([]byte, error) {
	key := sha256.Sum256(s.digest("encryption-key\x00", domain))
	block, err := aes.NewCipher(key[:])
	if err != nil {
		return nil, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	if len(value) < gcm.NonceSize() {
		return nil, errors.New("invalid encrypted authentication factor")
	}
	plain, err := gcm.Open(nil, value[:gcm.NonceSize()], value[gcm.NonceSize():], []byte(domain))
	if err != nil {
		return nil, errors.New("decrypt authentication factor")
	}
	return plain, nil
}
