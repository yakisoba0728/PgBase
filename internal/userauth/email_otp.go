package userauth

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
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
	if err := s.consumeRateLimit(ctx, s.subjectHash("email-otp-request", "network", clientAddress), s.currentRatePolicy().NetworkMaxFailures, now); err != nil {
		return EmailOTPRequest{}, err
	}
	account := normalized
	if emailErr != nil {
		account = "<invalid>"
	}
	if err := s.consumeRateLimit(ctx, s.subjectHash("email-otp-request", "account", collectionID.String()+"\x00"+account), s.currentRatePolicy().AccountMaxFailures, now); err != nil {
		return EmailOTPRequest{}, err
	}
	id, err := s.newEmailOTPID(collectionID, account)
	if err != nil {
		return EmailOTPRequest{}, err
	}
	result := EmailOTPRequest{OTPID: id.String()}
	if emailErr != nil {
		return result, nil
	}
	// Only the collection policy is read here. Looking the address up,
	// storing the code and sending it all run in the mail queue, so a
	// registered and an unknown address do the same work before this answers,
	// however slow the mail server is.
	var enabled bool
	var duration int
	err = s.pool.QueryRow(ctx, `select p.otp_enabled,coalesce(t.ttl_seconds,p.otp_duration_seconds) from pgbase_meta.auth_collection_policies p left join pgbase_meta.auth_token_policies t on t.auth_collection_id=p.auth_collection_id and t.purpose='otp' where p.auth_collection_id=$1`, collectionID).Scan(&enabled, &duration)
	if errors.Is(err, pgx.ErrNoRows) {
		return result, nil
	}
	if err != nil {
		return EmailOTPRequest{}, err
	}
	if !enabled {
		return result, nil
	}
	result.ExpiresAt = now.Add(time.Duration(duration) * time.Second)
	s.mailQueue.Go(ctx, func(ctx context.Context) {
		s.issueEmailOTP(ctx, collectionID, normalized, id, result.ExpiresAt, now)
	})
	return result, nil
}

// issueEmailOTP stores and sends a code for an address that has an account
// and does nothing for any other address. It runs in the mail queue, so its
// outcome is never visible to the requester.
func (s *Service) issueEmailOTP(ctx context.Context, collectionID uuid.UUID, email string, id uuid.UUID, expiresAt, now time.Time) {
	var random [8]byte
	if _, err := rand.Read(random[:]); err != nil {
		return
	}
	code := fmt.Sprintf("%06d", binary.BigEndian.Uint64(random[:])%1_000_000)
	err := s.pool.InTx(ctx, pgx.TxOptions{}, func(tx pgx.Tx) error {
		var enabled bool
		var maxAttempts int
		if err := tx.QueryRow(ctx, `select otp_enabled,otp_max_attempts from pgbase_meta.auth_collection_policies where auth_collection_id=$1 for share`, collectionID).Scan(&enabled, &maxAttempts); err != nil {
			return err
		}
		if !enabled {
			return ErrInvalidCredentials
		}
		var otpHash []byte
		var keyVersion string
		var cipher []byte
		if err := tx.QueryRow(ctx, `select key_version,key_ciphertext from pgbase_meta.auth_token_policies where auth_collection_id=$1 and purpose='otp' for share`, collectionID).Scan(&keyVersion, &cipher); err == nil {
			key, e := s.decrypt("auth-token-policy:otp", cipher)
			if e != nil {
				return e
			}
			otpHash = keyedDigest(key, emailOTPDomain(id, email), code)
		} else if !errors.Is(err, pgx.ErrNoRows) {
			return err
		}
		var userID uuid.UUID
		if err := tx.QueryRow(ctx, `select id from pgbase_meta.users where auth_collection_id=$1 and email=$2 and active for update`, collectionID, email).Scan(&userID); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `update pgbase_meta.user_email_otps set consumed_at=CURRENT_TIMESTAMP where user_id=$1 and consumed_at is null`, userID); err != nil {
			return err
		}
		if otpHash == nil {
			otpHash = s.digest(emailOTPDomain(id, email), code)
		}
		_, err := tx.Exec(ctx, `insert into pgbase_meta.user_email_otps(id,auth_collection_id,user_id,code_hash,token_key_version,max_attempts,expires_at,created_at) values($1,$2,$3,$4,$5,$6,$7,$8)`, id, collectionID, userID, otpHash, keyVersion, maxAttempts, expiresAt, now)
		return err
	})
	if err != nil {
		return
	}
	if err := s.sendOTP(ctx, collectionID, email, id.String(), code); err != nil {
		cleanupCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		defer cancel()
		_, _ = s.pool.Exec(cleanupCtx, `update pgbase_meta.user_email_otps set consumed_at=CURRENT_TIMESTAMP where id=$1`, id)
	}
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
	// Each code allows max_attempts guesses, but a fresh request would reset
	// that, so failures also spend one budget per collection and requested
	// address, shared by every code issued for it, like password login
	// failures. The budget comes from the authenticated account tag inside the
	// OTP id, which ids issued for unknown addresses carry too, so a missing
	// account spends and exhausts it exactly like an existing one. An
	// exhausted budget refuses even a correct code until the block ends. The
	// attempt is charged before the code is checked and refunded unless it is
	// refused, so concurrent wrong codes cannot overshoot the budget.
	tag, authentic := s.emailOTPIDTag(collectionID, id)
	if !authentic {
		// Only ids issued before account tags existed reach this; they are
		// still honored until they expire.
		var email string
		err = s.pool.QueryRow(ctx, `select u.email from pgbase_meta.user_email_otps o join pgbase_meta.users u on u.id=o.user_id where o.id=$1 and o.auth_collection_id=$2`, id, collectionID).Scan(&email)
		if errors.Is(err, pgx.ErrNoRows) {
			return Session{}, ErrInvalidCredentials
		}
		if err != nil {
			return Session{}, err
		}
		tag = s.emailOTPAccountTag(collectionID, email)
	}
	reservation, err := s.reserveFailures(ctx, s.emailOTPBudget(collectionID, tag), now)
	if err != nil {
		return Session{}, err
	}
	defer reservation.refund(ctx)
	var issued Session
	var operationErr error
	var wrongCode bool
	err = s.pool.InTx(ctx, pgx.TxOptions{}, func(tx pgx.Tx) error {
		var enabled bool
		if err := tx.QueryRow(ctx, `select otp_enabled from pgbase_meta.auth_collection_policies where auth_collection_id=$1 for share`, collectionID).Scan(&enabled); err != nil || !enabled {
			operationErr = ErrInvalidCredentials
			return nil
		}
		var user User
		var stored []byte
		var attempts, maxAttempts int
		var keyVersion string
		err := scanSessionUser(tx.QueryRow(ctx, `select `+sessionUserColumns+`,o.code_hash,o.token_key_version,o.attempts,o.max_attempts from pgbase_meta.user_email_otps o join pgbase_meta.users u on u.id=o.user_id where o.id=$1 and o.auth_collection_id=$2 and o.consumed_at is null and o.expires_at>CURRENT_TIMESTAMP and u.active for update of o,u`, id, collectionID), &user, &stored, &keyVersion, &attempts, &maxAttempts)
		if errors.Is(err, pgx.ErrNoRows) {
			operationErr = ErrInvalidCredentials
			return nil
		}
		if err != nil {
			return err
		}
		expected := s.digest(emailOTPDomain(id, user.Email), code)
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
			expected = keyedDigest(key, emailOTPDomain(id, user.Email), code)
		}
		if !hmac.Equal(stored, expected) {
			attempts++
			_, err = tx.Exec(ctx, `update pgbase_meta.user_email_otps set attempts=$2,consumed_at=case when $2>=max_attempts then CURRENT_TIMESTAMP else null end where id=$1`, id, attempts)
			operationErr = ErrInvalidCredentials
			wrongCode = true
			return err
		}
		if err := s.enforceAuthRuleForSession(ctx, tx, user.AuthCollectionID, user.ID); err != nil {
			operationErr = ErrInvalidCredentials
			return nil
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
	// With an authentic id every refusal spends the budget, whether or not a
	// stored code exists, is live or still has attempts: an unknown address
	// never has one, so anything narrower would tell the two apart.
	if wrongCode || authentic && errors.Is(operationErr, ErrInvalidCredentials) {
		if err := reservation.fail(); err != nil {
			return Session{}, err
		}
	}
	if operationErr != nil {
		return Session{}, operationErr
	}
	s.recordIssuedAuthOrigin(ctx, issued, now)
	return issued, nil
}

// An OTP id stays a version 4 UUID but carries, without storage, which budget
// its failures spend: bytes 0-5 are a keyed tag of collection and requested
// address, bytes 6-11 are random (except the version and variant bits), and
// bytes 12-15 authenticate the rest. Ids issued for unknown addresses are
// built the same way, so completion treats both alike, and forged ids cannot
// create budgets for arbitrary tags.
const (
	emailOTPIDTagBytes = 6
	emailOTPIDMACBytes = 4
)

func (s *Service) emailOTPAccountTag(collectionID uuid.UUID, account string) []byte {
	return s.digest("email-otp-account\x00"+collectionID.String()+"\x00", account)[:emailOTPIDTagBytes]
}

func (s *Service) emailOTPIDMAC(collectionID uuid.UUID, id uuid.UUID) []byte {
	return s.digest("email-otp-id\x00"+collectionID.String()+"\x00", string(id[:len(id)-emailOTPIDMACBytes]))[:emailOTPIDMACBytes]
}

func (s *Service) newEmailOTPID(collectionID uuid.UUID, account string) (uuid.UUID, error) {
	var id uuid.UUID
	copy(id[:emailOTPIDTagBytes], s.emailOTPAccountTag(collectionID, account))
	if _, err := rand.Read(id[emailOTPIDTagBytes : len(id)-emailOTPIDMACBytes]); err != nil {
		return uuid.Nil, err
	}
	id[6] = id[6]&0x0f | 0x40
	id[8] = id[8]&0x3f | 0x80
	copy(id[len(id)-emailOTPIDMACBytes:], s.emailOTPIDMAC(collectionID, id))
	return id, nil
}

// emailOTPIDTag returns the account tag of id and whether this service issued
// it for this collection.
func (s *Service) emailOTPIDTag(collectionID uuid.UUID, id uuid.UUID) ([]byte, bool) {
	return id[:emailOTPIDTagBytes], hmac.Equal(id[len(id)-emailOTPIDMACBytes:], s.emailOTPIDMAC(collectionID, id))
}

func (s *Service) emailOTPBudget(collectionID uuid.UUID, tag []byte) []rateLimitBucket {
	return []rateLimitBucket{{hash: s.subjectHash("email-otp-verify", "account", collectionID.String()+"\x00"+hex.EncodeToString(tag)), limit: s.currentRatePolicy().AccountMaxFailures}}
}

// Bind the code to its delivery address, including when a collection key is
// configured. Older address-unbound codes fail closed and must be reissued.
func emailOTPDomain(id uuid.UUID, email string) string {
	return "email-otp-v2\x00" + id.String() + "\x00" + email + "\x00"
}

func (s *Service) sendOTP(ctx context.Context, collectionID uuid.UUID, to, otpID, code string) error {
	subject, body, htmlBody := "Your PgBase login code", "Your one-time login code is: "+code, ""
	var cs, cb, ch string
	if err := s.pool.QueryRow(ctx, `select subject,body,html_body from pgbase_meta.auth_mail_templates where auth_collection_id=$1 and purpose='otp'`, collectionID).Scan(&cs, &cb, &ch); err == nil {
		subject = cs
		body = strings.ReplaceAll(strings.ReplaceAll(cb, "{{otp}}", code), "{{otp_id}}", otpID)
		if ch != "" {
			var wrapErr error
			htmlBody, wrapErr = mailer.RenderHTML(ch, mailer.HTMLVariables{OTP: code, OTPID: otpID})
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
	return sender.Send(ctx, mailer.Message{Purpose: "otp", To: to, Subject: subject, Text: body, HTML: htmlBody, Meta: map[string]any{"otpId": otpID, "password": code}})
}
