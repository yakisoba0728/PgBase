package userauth

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"encoding/binary"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/yakisoba0728/PgBase/internal/database"
	"github.com/yakisoba0728/PgBase/internal/mailer"
)

type EmailOTPRequest struct {
	OTPID     string    `json:"otpId"`
	ExpiresAt time.Time `json:"expiresAt"`
}

func (s *Service) RequestEmailOTP(ctx context.Context, email, clientAddress string) (EmailOTPRequest, error) {
	return s.RequestEmailOTPInCollection(ctx, DefaultCollectionID, email, clientAddress)
}
func (s *Service) RequestEmailOTPInCollection(ctx context.Context, collectionID uuid.UUID, email, clientAddress string) (EmailOTPRequest, error) {
	if collectionID == uuid.Nil || validateClientAddress(clientAddress) != nil {
		return EmailOTPRequest{}, ErrValidation
	}
	normalized, emailErr := normalizeEmail(email)
	now, err := s.databaseNow(ctx)
	if err != nil {
		return EmailOTPRequest{}, err
	}
	if err := s.consumeRateLimit(ctx, s.subjectHash("email-otp-request", "network", clientAddress), loginNetworkLimit, now); err != nil {
		return EmailOTPRequest{}, err
	}
	account := normalized
	if emailErr != nil {
		account = "<invalid>"
	}
	if err := s.consumeRateLimit(ctx, s.subjectHash("email-otp-request", "account", collectionID.String()+"\x00"+account), loginAccountLimit, now); err != nil {
		return EmailOTPRequest{}, err
	}
	result := EmailOTPRequest{OTPID: uuid.NewString()}
	if emailErr != nil {
		return result, nil
	}
	var random [8]byte
	if _, err := rand.Read(random[:]); err != nil {
		return EmailOTPRequest{}, err
	}
	code := fmt.Sprintf("%06d", binary.BigEndian.Uint64(random[:])%1_000_000)
	id := uuid.MustParse(result.OTPID)
	var userID uuid.UUID
	var otpHash []byte
	var keyVersion string
	err = s.pool.InTx(ctx, pgx.TxOptions{}, func(tx pgx.Tx) error {
		var enabled bool
		var duration, maxAttempts int
		if err := tx.QueryRow(ctx, `select otp_enabled,otp_duration_seconds,otp_max_attempts from pgbase_meta.auth_collection_policies where auth_collection_id=$1 for update`, collectionID).Scan(&enabled, &duration, &maxAttempts); err != nil {
			return err
		}
		if !enabled {
			return ErrInvalidCredentials
		}
		var configuredTTL int
		var cipher []byte
		if err := tx.QueryRow(ctx, `select ttl_seconds,key_version,key_ciphertext from pgbase_meta.auth_token_policies where auth_collection_id=$1 and purpose='otp'`, collectionID).Scan(&configuredTTL, &keyVersion, &cipher); err == nil {
			duration = configuredTTL
			key, e := s.decrypt("auth-token-policy:otp", cipher)
			if e != nil {
				return e
			}
			otpHash = keyedDigest(key, "email-otp\x00"+id.String()+"\x00", code)
		} else if !errors.Is(err, pgx.ErrNoRows) {
			return err
		}
		result.ExpiresAt = now.Add(time.Duration(duration) * time.Second)
		if err := tx.QueryRow(ctx, `select id from pgbase_meta.users where auth_collection_id=$1 and email=$2 and active for update`, collectionID, normalized).Scan(&userID); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `update pgbase_meta.user_email_otps set consumed_at=CURRENT_TIMESTAMP where user_id=$1 and consumed_at is null`, userID); err != nil {
			return err
		}
		if otpHash == nil {
			otpHash = s.digest("email-otp\x00"+id.String()+"\x00", code)
		}
		_, err := tx.Exec(ctx, `insert into pgbase_meta.user_email_otps(id,auth_collection_id,user_id,code_hash,token_key_version,max_attempts,expires_at,created_at) values($1,$2,$3,$4,$5,$6,$7,$8)`, id, collectionID, userID, otpHash, keyVersion, maxAttempts, result.ExpiresAt, now)
		return err
	})
	if errors.Is(err, pgx.ErrNoRows) || errors.Is(err, ErrInvalidCredentials) {
		return result, nil
	}
	if database.Classify(err) == database.KindConflict {
		return result, nil
	}
	if err != nil {
		return EmailOTPRequest{}, err
	}
	if err := s.sendOTP(ctx, collectionID, normalized, result.OTPID, code); err != nil {
		cleanupCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		defer cancel()
		_, _ = s.pool.Exec(cleanupCtx, `update pgbase_meta.user_email_otps set consumed_at=CURRENT_TIMESTAMP where id=$1`, id)
	}
	return result, nil
}

func (s *Service) CompleteEmailOTP(ctx context.Context, otpID, code string) (Session, error) {
	return s.CompleteEmailOTPInCollection(ctx, DefaultCollectionID, otpID, code)
}
func (s *Service) CompleteEmailOTPInCollection(ctx context.Context, collectionID uuid.UUID, otpID, code string) (Session, error) {
	id, err := uuid.Parse(otpID)
	if err != nil || len(code) != 6 {
		return Session{}, ErrInvalidCredentials
	}
	now, err := s.databaseNow(ctx)
	if err != nil {
		return Session{}, err
	}
	var issued Session
	var operationErr error
	err = s.pool.InTx(ctx, pgx.TxOptions{}, func(tx pgx.Tx) error {
		var enabled bool
		if err := tx.QueryRow(ctx, `select otp_enabled from pgbase_meta.auth_collection_policies where auth_collection_id=$1 for update`, collectionID).Scan(&enabled); err != nil || !enabled {
			operationErr = ErrInvalidCredentials
			return nil
		}
		var user User
		var stored []byte
		var attempts, maxAttempts int
		var keyVersion string
		err := tx.QueryRow(ctx, `select u.id,u.auth_collection_id,u.email,u.verified,u.created_at,o.code_hash,o.token_key_version,o.attempts,o.max_attempts from pgbase_meta.user_email_otps o join pgbase_meta.users u on u.id=o.user_id where o.id=$1 and o.auth_collection_id=$2 and o.consumed_at is null and o.expires_at>CURRENT_TIMESTAMP and u.active for update of o,u`, id, collectionID).Scan(&user.ID, &user.AuthCollectionID, &user.Email, &user.Verified, &user.CreatedAt, &stored, &keyVersion, &attempts, &maxAttempts)
		if errors.Is(err, pgx.ErrNoRows) {
			operationErr = ErrInvalidCredentials
			return nil
		}
		if err != nil {
			return err
		}
		expected := s.digest("email-otp\x00"+id.String()+"\x00", code)
		if keyVersion != "" {
			var cipher []byte
			if e := tx.QueryRow(ctx, `select key_ciphertext from pgbase_meta.auth_token_policies where auth_collection_id=$1 and purpose='otp' and key_version=$2`, collectionID, keyVersion).Scan(&cipher); e != nil {
				operationErr = ErrInvalidCredentials
				return nil
			}
			key, e := s.decrypt("auth-token-policy:otp", cipher)
			if e != nil {
				operationErr = ErrInvalidCredentials
				return nil
			}
			expected = keyedDigest(key, "email-otp\x00"+id.String()+"\x00", code)
		}
		if !hmac.Equal(stored, expected) {
			attempts++
			_, err = tx.Exec(ctx, `update pgbase_meta.user_email_otps set attempts=$2,consumed_at=case when $2>=max_attempts then CURRENT_TIMESTAMP else null end where id=$1`, id, attempts)
			operationErr = ErrInvalidCredentials
			return err
		}
		if _, err = tx.Exec(ctx, `update pgbase_meta.user_email_otps set consumed_at=CURRENT_TIMESTAMP where id=$1`, id); err != nil {
			return err
		}
		active, requiredErr := s.mfaRequiredTx(ctx, tx, user)
		if requiredErr != nil {
			return requiredErr
		}
		if active {
			challenge, e := s.insertMFAChallenge(ctx, tx, user.ID, now)
			operationErr = challenge
			return e
		}
		issued, err = s.insertSession(ctx, tx, user, now)
		return err
	})
	if err != nil {
		return Session{}, err
	}
	if operationErr != nil {
		return Session{}, operationErr
	}
	return issued, nil
}

func (s *Service) sendOTP(ctx context.Context, collectionID uuid.UUID, to, otpID, code string) error {
	subject, body, htmlBody := "Your PgBase login code", "Your one-time login code is: "+code, ""
	var cs, cb, ch string
	if err := s.pool.QueryRow(ctx, `select subject,body,html_body from pgbase_meta.auth_mail_templates where auth_collection_id=$1 and purpose='otp'`, collectionID).Scan(&cs, &cb, &ch); err == nil {
		subject = cs
		body = strings.ReplaceAll(strings.ReplaceAll(cb, "{{otp}}", code), "{{otp_id}}", otpID)
		if ch != "" {
			var wrapErr error
			htmlBody, wrapErr = mailer.WrapHTML(strings.ReplaceAll(strings.ReplaceAll(ch, "{{otp}}", code), "{{otp_id}}", otpID))
			if wrapErr != nil {
				return wrapErr
			}
		}
	} else if !errors.Is(err, pgx.ErrNoRows) {
		return err
	}
	sender, err := s.smtpSender(ctx, collectionID)
	if err != nil {
		return err
	}
	return sender.Send(ctx, mailer.Message{Purpose: "otp", To: to, Subject: subject, Text: body, HTML: htmlBody})
}
