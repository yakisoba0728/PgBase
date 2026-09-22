package userauth

import (
	"context"
	"crypto/hmac"
	"errors"
	"fmt"
	"html"
	"net/url"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/yakisoba0728/PgBase/internal/database"
	"github.com/yakisoba0728/PgBase/internal/mailer"
)

const maximumEmailActionTTL = 24 * time.Hour

type emailAction struct {
	id             uuid.UUID
	userID         uuid.UUID
	purpose        string
	payloadEmail   *string
	credentialHash []byte
	expiresAt      time.Time
}

func (s *Service) ConfigureEmailActions(sender mailer.Sender, baseURL string, ttl time.Duration) error {
	if s == nil || sender == nil || ttl <= 0 || ttl > maximumEmailActionTTL {
		return ErrValidation
	}
	if err := s.ConfigureEmailActionBaseURL(baseURL, ttl); err != nil {
		return err
	}
	s.mailer = sender
	return nil
}

// ConfigureEmailActionBaseURL enables database-managed per-realm SMTP without
// requiring a process-wide SMTP credential.
func (s *Service) ConfigureEmailActionBaseURL(baseURL string, ttl time.Duration) error {
	if s == nil || ttl <= 0 || ttl > maximumEmailActionTTL {
		return ErrValidation
	}
	parsed, err := url.Parse(baseURL)
	if err != nil || parsed.Scheme == "" || parsed.Host == "" || parsed.User != nil || parsed.Fragment != "" {
		return ErrValidation
	}
	s.emailBaseURL, s.emailActionTTL = strings.TrimRight(baseURL, "/"), ttl
	return nil
}

func (s *Service) RequestVerification(ctx context.Context, userID uuid.UUID) error {
	if err := s.emailReady(); err != nil {
		return err
	}
	if userID == uuid.Nil {
		return ErrValidation
	}
	user, passwordHash, err := userPasswordByIDDB(ctx, s.pool, userID, false)
	if err != nil {
		if errors.Is(err, ErrInvalidCredentials) {
			return ErrNotFound
		}
		return err
	}
	if !user.Active {
		return ErrNotFound
	}
	if user.Verified {
		return nil
	}
	raw, err := s.issueEmailAction(ctx, user.ID, "verify", nil, passwordHash, user.Email)
	if err != nil {
		return err
	}
	return s.sendAction(ctx, user.ID, user.Email, "Verify your email", "verify", raw)
}

func (s *Service) ConfirmVerification(ctx context.Context, rawToken string) error {
	return s.consumeEmailAction(ctx, "verify", rawToken, func(ctx context.Context, tx pgx.Tx, action emailAction) error {
		_, err := tx.Exec(ctx, `update pgbase_meta.users set verified=true,updated_at=CURRENT_TIMESTAMP where id=$1`, action.userID)
		return err
	})
}

func (s *Service) RequestPasswordReset(ctx context.Context, email, clientAddress string) error {
	return s.RequestPasswordResetInCollection(ctx, DefaultCollectionID, email, clientAddress)
}

func (s *Service) RequestPasswordResetInCollection(ctx context.Context, collectionID uuid.UUID, email, clientAddress string) error {
	if err := s.emailReady(); err != nil {
		return err
	}
	normalized, emailErr := normalizeEmail(email)
	if err := validateClientAddress(clientAddress); err != nil {
		return err
	}
	account := normalized
	if emailErr != nil {
		account = "<invalid>"
	}
	now, err := s.databaseNow(ctx)
	if err != nil {
		return err
	}
	account = collectionID.String() + "\x00" + account
	buckets := []rateLimitBucket{{hash: s.subjectHash("password-reset", "account", account), limit: loginAccountLimit}, {hash: s.subjectHash("password-reset", "network", clientAddress), limit: loginNetworkLimit}}
	if err := s.ensureBucketsNotBlocked(ctx, buckets, now); err != nil {
		return err
	}
	if err := s.recordFailures(ctx, buckets, now); err != nil {
		return err
	}
	if emailErr != nil {
		return nil
	}
	user, passwordHash, lookupErr := userByEmailDB(ctx, s.pool, collectionID, normalized, false)
	if errors.Is(lookupErr, ErrInvalidCredentials) {
		return nil
	}
	if lookupErr != nil {
		return lookupErr
	}
	raw, err := s.issueEmailAction(ctx, user.ID, "password_reset", nil, passwordHash, user.Email)
	if err != nil {
		return err
	}
	// Reset requests deliberately do not disclose delivery or account state.
	// The token remains bounded by expiry and cleanup if delivery fails.
	_ = s.sendAction(ctx, user.ID, user.Email, "Reset your password", "reset", raw)
	return nil
}

func (s *Service) ConfirmPasswordReset(ctx context.Context, rawToken, newPassword string) error {
	if err := s.emailReady(); err != nil {
		return err
	}
	if err := validatePassword(newPassword); err != nil {
		return err
	}
	if validateToken(rawToken) != nil && !policyTokenValid(rawToken) {
		return ErrInvalidCredentials
	}
	release, err := admit(s.passwordSlots)
	if err != nil {
		return err
	}
	newHash, err := s.passwords.Hash(newPassword)
	release()
	if err != nil {
		return err
	}
	return s.consumeEmailAction(ctx, "password_reset", rawToken, func(ctx context.Context, tx pgx.Tx, action emailAction) error {
		var realm uuid.UUID
		if err := tx.QueryRow(ctx, `select auth_collection_id from pgbase_meta.users where id=$1`, action.userID).Scan(&realm); err != nil {
			return err
		}
		policy, err := authPolicyTx(ctx, tx, realm)
		if err != nil || !passwordMatchesPolicy(newPassword, policy) {
			return ErrValidation
		}
		if _, err := tx.Exec(ctx, `update pgbase_meta.users set password_hash=$2,updated_at=CURRENT_TIMESTAMP where id=$1`, action.userID, newHash); err != nil {
			return err
		}
		_, err = revokeSessionsInTx(ctx, tx, action.userID)
		return err
	})
}

func (s *Service) RequestEmailChange(ctx context.Context, userID uuid.UUID, currentPassword, newEmail string) error {
	if err := s.emailReady(); err != nil {
		return err
	}
	if userID == uuid.Nil {
		return ErrValidation
	}
	normalized, err := normalizeEmail(newEmail)
	if err != nil {
		return err
	}
	now, err := s.databaseNow(ctx)
	if err != nil {
		return err
	}
	buckets := []rateLimitBucket{{hash: s.subjectHash("email-change", "account", userID.String()), limit: loginAccountLimit}}
	if err := s.ensureBucketsNotBlocked(ctx, buckets, now); err != nil {
		return err
	}
	user, hash, lookupErr := userPasswordByIDDB(ctx, s.pool, userID, false)
	if lookupErr != nil && !errors.Is(lookupErr, ErrInvalidCredentials) {
		return lookupErr
	}
	release, err := admit(s.passwordSlots)
	if err != nil {
		return err
	}
	shapeValid := validatePassword(currentPassword) == nil && lookupErr == nil && user.Active
	verifyHash, verifyPassword := hash, currentPassword
	if !shapeValid {
		verifyHash, verifyPassword = dummyPasswordHash, dummyPassword
	}
	verifyErr := s.passwords.Verify(verifyHash, verifyPassword)
	release()
	if verifyErr != nil && !isPasswordMismatch(verifyErr) {
		return verifyErr
	}
	if !shapeValid || verifyErr != nil {
		if err := s.recordFailures(ctx, buckets, now); err != nil {
			return err
		}
		return ErrInvalidCredentials
	}
	var existing bool
	if err := s.pool.QueryRow(ctx, `select exists(select 1 from pgbase_meta.users where auth_collection_id=(select auth_collection_id from pgbase_meta.users where id=$2) and email=$1 and id<>$2)`, normalized, userID).Scan(&existing); err != nil {
		return fmt.Errorf("check changed user email: %w", err)
	}
	if existing {
		return ErrConflict
	}
	raw, err := s.issueEmailAction(ctx, user.ID, "email_change", &normalized, hash, user.Email)
	if database.Classify(err) == database.KindConflict {
		return ErrConflict
	}
	if err != nil {
		return err
	}
	return s.sendAction(ctx, user.ID, normalized, "Confirm your email change", "email-change", raw)
}

// AdminRequestEmailChange is the explicitly privileged counterpart used by
// operator-configured hosts. It never exposes the generated action token.
func (s *Service) AdminRequestEmailChange(ctx context.Context, userID uuid.UUID, newEmail string) error {
	if err := s.emailReady(); err != nil {
		return err
	}
	normalized, err := normalizeEmail(newEmail)
	if err != nil {
		return err
	}
	user, hash, err := userPasswordByIDDB(ctx, s.pool, userID, false)
	if err != nil {
		return ErrNotFound
	}
	raw, err := s.issueEmailAction(ctx, user.ID, "email_change", &normalized, hash, user.Email)
	if err != nil {
		return err
	}
	return s.sendAction(ctx, user.ID, normalized, "Confirm your email change", "email-change", raw)
}

// AdminSendAuthAlert retains server template and SMTP ownership.
func (s *Service) AdminSendAuthAlert(ctx context.Context, userID uuid.UUID) error {
	managed, _, err := userPasswordByIDDB(ctx, s.pool, userID, false)
	if err != nil {
		return ErrNotFound
	}
	return s.sendAuthAlertMessage(ctx, managed.User, "Administrator requested security alert")
}

// AdminSendRecordOTP sends caller-selected OTP credentials without creating,
// consuming, or replacing an OTP challenge.
func (s *Service) AdminSendRecordOTP(ctx context.Context, collectionID, userID uuid.UUID, otpID, password string) error {
	if collectionID == uuid.Nil || userID == uuid.Nil || strings.TrimSpace(otpID) == "" || strings.TrimSpace(password) == "" {
		return ErrValidation
	}
	managed, _, err := userPasswordByIDDB(ctx, s.pool, userID, false)
	if err != nil || managed.AuthCollectionID != collectionID {
		return ErrNotFound
	}
	return s.sendOTP(ctx, collectionID, managed.Email, otpID, password)
}

// AdminSendRecordAuthAlert renders the supplied alert information and returns
// transport/template failures to the explicitly privileged caller.
func (s *Service) AdminSendRecordAuthAlert(ctx context.Context, userID uuid.UUID, alertInfo string) error {
	if strings.TrimSpace(alertInfo) == "" {
		return ErrValidation
	}
	managed, _, err := userPasswordByIDDB(ctx, s.pool, userID, false)
	if err != nil {
		return ErrNotFound
	}
	return s.sendAuthAlertMessage(ctx, managed.User, alertInfo)
}

func (s *Service) ConfirmEmailChange(ctx context.Context, rawToken string) error {
	return s.consumeEmailAction(ctx, "email_change", rawToken, func(ctx context.Context, tx pgx.Tx, action emailAction) error {
		if action.payloadEmail == nil {
			return ErrInvalidCredentials
		}
		tag, err := tx.Exec(ctx, `update pgbase_meta.users set email=$2,verified=true,updated_at=CURRENT_TIMESTAMP where id=$1`, action.userID, *action.payloadEmail)
		if database.Classify(err) == database.KindConflict {
			return ErrConflict
		}
		if err != nil {
			return err
		}
		if tag.RowsAffected() != 1 {
			return ErrInvalidCredentials
		}
		_, err = revokeSessionsInTx(ctx, tx, action.userID)
		return err
	})
}

func (s *Service) emailReady() error {
	if err := s.ready(); err != nil {
		return err
	}
	if s.emailBaseURL == "" || s.emailActionTTL <= 0 {
		return errors.New("user email actions unavailable")
	}
	return nil
}

func (s *Service) issueEmailAction(ctx context.Context, userID uuid.UUID, purpose string, payload *string, passwordHash, email string) (string, error) {
	var raw string
	err := s.pool.InTx(ctx, pgx.TxOptions{}, func(tx pgx.Tx) error {
		var hash []byte
		var ttl time.Duration
		var err error
		raw, hash, ttl, err = s.issueActionPolicy(ctx, tx, userID, purpose)
		if err != nil {
			return err
		}
		if hash == nil {
			hash = s.actionTokenHash(purpose, raw)
		}
		_, err = tx.Exec(ctx, `insert into pgbase_meta.user_email_actions(id,user_id,purpose,token_hash,payload_email,credential_hash,expires_at,created_at) values($1,$2,$3,$4,$5,$6,CURRENT_TIMESTAMP+$7::interval,CURRENT_TIMESTAMP)`, uuid.New(), userID, purpose, hash, payload, s.emailActionCredentialHash(purpose, passwordHash, email), postgresInterval(ttl))
		return err
	})
	if err != nil {
		return "", fmt.Errorf("create user email action: %w", err)
	}
	return raw, nil
}

func (s *Service) consumeEmailAction(ctx context.Context, purpose, rawToken string, mutate func(context.Context, pgx.Tx, emailAction) error) error {
	if err := s.emailReady(); err != nil {
		return err
	}
	if validateToken(rawToken) != nil && !policyTokenValid(rawToken) {
		return ErrInvalidCredentials
	}
	return s.pool.InTx(ctx, pgx.TxOptions{}, func(tx pgx.Tx) error {
		hash := s.actionTokenHash(purpose, rawToken)
		if version, _, ok := splitPolicyToken(rawToken); ok {
			var cipher []byte
			err := tx.QueryRow(ctx, `select key_ciphertext from pgbase_meta.auth_token_policies where key_version=$1 and purpose=$2`, version, purpose).Scan(&cipher)
			if err != nil {
				return ErrInvalidCredentials
			}
			key, err := s.decrypt("auth-token-policy:"+purpose, cipher)
			if err != nil {
				return ErrInvalidCredentials
			}
			hash = keyedDigest(key, "user-email-action\x00"+purpose+"\x00", rawToken)
		}
		var action emailAction
		var currentHash, currentEmail string
		err := tx.QueryRow(ctx, `select a.id,a.user_id,a.purpose,a.payload_email,a.credential_hash,a.expires_at,u.password_hash,u.email from pgbase_meta.user_email_actions a join pgbase_meta.users u on u.id=a.user_id where a.token_hash=$1 and a.purpose=$2 and a.consumed_at is null and a.expires_at>CURRENT_TIMESTAMP and u.active for update of a,u`, hash, purpose).Scan(&action.id, &action.userID, &action.purpose, &action.payloadEmail, &action.credentialHash, &action.expiresAt, &currentHash, &currentEmail)
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrInvalidCredentials
		}
		if err != nil {
			return fmt.Errorf("read user email action: %w", err)
		}
		if !hmac.Equal(action.credentialHash, s.emailActionCredentialHash(purpose, currentHash, currentEmail)) {
			return ErrInvalidCredentials
		}
		if err := mutate(ctx, tx, action); err != nil {
			return err
		}
		tag, err := tx.Exec(ctx, `update pgbase_meta.user_email_actions set consumed_at=CURRENT_TIMESTAMP where id=$1 and consumed_at is null`, action.id)
		if err != nil {
			return err
		}
		if tag.RowsAffected() != 1 {
			return ErrInvalidCredentials
		}
		return nil
	})
}

func (s *Service) sendAction(ctx context.Context, userID uuid.UUID, to, subject, action, raw string) error {
	var collectionID uuid.UUID
	if err := s.pool.QueryRow(ctx, `select auth_collection_id from pgbase_meta.users where id=$1`, userID).Scan(&collectionID); err != nil {
		return errors.New("send user email action")
	}
	actionURL := s.emailBaseURL + "/auth/action#action=" + action + "&token=" + url.QueryEscape(raw)
	body, htmlBody := actionURL, ""
	var configuredSubject, configuredBody, configuredHTML string
	templatePurpose := action
	if action == "reset" {
		templatePurpose = "password_reset"
	} else if action == "email-change" {
		templatePurpose = "email_change"
	}
	if err := s.pool.QueryRow(ctx, `select subject,body,html_body from pgbase_meta.auth_mail_templates where auth_collection_id=$1 and purpose=$2`, collectionID, templatePurpose).Scan(&configuredSubject, &configuredBody, &configuredHTML); err == nil {
		subject, body = configuredSubject, strings.ReplaceAll(configuredBody, "{{action_url}}", actionURL)
		if configuredHTML != "" {
			htmlBody, err = mailer.WrapHTML(strings.ReplaceAll(configuredHTML, "{{action_url}}", actionURL))
			if err != nil {
				return errors.New("send user email action")
			}
		}
	} else if !errors.Is(err, pgx.ErrNoRows) {
		return errors.New("send user email action")
	}
	sender, err := s.smtpSender(ctx, collectionID)
	if err != nil {
		return errors.New("send user email action")
	}
	purpose := "verification"
	if action == "reset" {
		purpose = "password_reset"
	} else if action == "email-change" {
		purpose = "email_change"
	}
	message := mailer.Message{Purpose: purpose, To: to, Subject: subject, Text: body, HTML: htmlBody}
	if err := sender.Send(ctx, message); err != nil {
		return errors.New("send user email action")
	}
	return nil
}

// sendAuthAlert is deliberately best-effort: a mail transport outage must not
// turn a successfully verified credential into a failed login. Administrators
// opt in by storing the auth_alert template for the collection.
func (s *Service) sendAuthAlert(ctx context.Context, user User, alert string) {
	_ = s.sendAuthAlertMessage(ctx, user, alert)
}

func (s *Service) sendAuthAlertMessage(ctx context.Context, user User, alert string) error {
	var subject, body, htmlBody string
	err := s.pool.QueryRow(ctx, `select subject,body,html_body from pgbase_meta.auth_mail_templates where auth_collection_id=$1 and purpose='auth_alert'`, user.AuthCollectionID).Scan(&subject, &body, &htmlBody)
	if err != nil {
		return err
	}
	body = strings.ReplaceAll(body, "{{alert}}", alert)
	if htmlBody != "" {
		var wrapErr error
		htmlBody, wrapErr = mailer.WrapHTML(strings.ReplaceAll(htmlBody, "{{alert}}", html.EscapeString(alert)))
		if wrapErr != nil {
			return wrapErr
		}
	}
	sender, err := s.smtpSender(ctx, user.AuthCollectionID)
	if err != nil {
		return err
	}
	return sender.Send(ctx, mailer.Message{Purpose: "auth_alert", To: user.Email, Subject: subject, Text: body, HTML: htmlBody, Meta: map[string]any{"info": alert}})
}

func (s *Service) actionTokenHash(purpose, raw string) []byte {
	return s.digest("user-email-action\x00"+purpose+"\x00", raw)
}
func (s *Service) credentialHash(hash string) []byte {
	return s.digest("user-credential-version\x00", hash)
}

// Email actions authorize the address and password state observed at issuance.
// A pending action must not survive an email-only account reassignment. Older
// password-only bindings deliberately fail closed and require a fresh email.
func (s *Service) emailActionCredentialHash(purpose, passwordHash, email string) []byte {
	return s.digest("user-email-credential-v2\x00"+purpose+"\x00", passwordHash+"\x00"+email)
}

func postgresInterval(ttl time.Duration) string { return fmt.Sprintf("%f seconds", ttl.Seconds()) }

// ConfigureMailerDecorator installs a startup-only transport wrapper for both
// environment and per-collection SMTP settings.
func (s *Service) ConfigureMailerDecorator(decorate func(mailer.Sender) mailer.Sender) {
	s.decorateMailer = decorate
}
