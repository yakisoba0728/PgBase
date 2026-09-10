package adminauth

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"encoding/binary"
	"errors"
	"fmt"
	"net/netip"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/yakisoba0728/PgBase/internal/mailer"
)

const maximumEmailOTPTTL = 15 * time.Minute

// ConfigureEmailOTP installs the delivery transport used by the explicit CLI
// issuance command. The code is not stored or logged by this package.
func (s *Service) ConfigureEmailOTP(sender mailer.Sender, ttl time.Duration) error {
	if s == nil || sender == nil || ttl <= 0 || ttl > maximumEmailOTPTTL {
		return ErrValidation
	}
	s.emailOTPMailer, s.emailOTPTTL = sender, ttl
	return nil
}

func (s *Service) emailOTPReady() error {
	if err := s.ready(); err != nil {
		return err
	}
	if s.emailOTPMailer == nil || s.emailOTPTTL <= 0 {
		return errors.New("administrator email OTP is unavailable")
	}
	return nil
}

// IssueEmailOTP replaces an earlier unused code for the account, persists only
// its HMAC, sends it through the configured mailer, and returns it solely for
// the caller to render in the explicit local administrator command.
func (s *Service) IssueEmailOTP(ctx context.Context, email string) (EmailOTP, error) {
	if err := s.emailOTPReady(); err != nil {
		return EmailOTP{}, err
	}
	normalized, err := NormalizeEmail(email)
	if err != nil {
		return EmailOTP{}, err
	}
	now := s.clock.Now().UTC()
	if err := s.admitRateLimit(ctx, []rateLimitBucket{{hash: rateLimitSubjectHash(s.tokenSecret, "email-otp-issue", "account", normalized), limit: accountFailureLimit}}, now); err != nil {
		return EmailOTP{}, err
	}
	var random [8]byte
	if _, err := rand.Read(random[:]); err != nil {
		return EmailOTP{}, fmt.Errorf("generate administrator email OTP: %w", err)
	}
	result := EmailOTP{ID: uuid.NewString(), Code: fmt.Sprintf("%06d", binary.BigEndian.Uint64(random[:])%1_000_000), ExpiresAt: now.Add(s.emailOTPTTL)}
	id := uuid.MustParse(result.ID)
	err = s.pool.InTx(ctx, pgx.TxOptions{}, func(tx pgx.Tx) error {
		store := NewStore(tx)
		administratorID, err := store.AdministratorID(ctx, normalized)
		if err != nil {
			return err
		}
		return store.ReplaceEmailOTP(ctx, id, administratorID, hashEmailOTPCode(s.tokenSecret, result.ID, result.Code), result.ExpiresAt, now)
	})
	if err != nil {
		return EmailOTP{}, err
	}
	if err := s.emailOTPMailer.Send(ctx, mailer.Message{Purpose: "admin_email_otp", To: normalized, Subject: "Your PgBase administrator login code", Text: "Your one-time administrator login code is: " + result.Code + "\nOTP ID: " + result.ID}); err != nil {
		cleanupCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		defer cancel()
		_, _ = s.pool.Exec(cleanupCtx, `update pgbase_meta.admin_email_otps set consumed_at=$2 where id=$1 and consumed_at is null`, id, now)
		return EmailOTP{}, fmt.Errorf("send administrator email OTP: %w", err)
	}
	return result, nil
}

// CompleteEmailOTP proves possession of an unexpired CLI-issued code and then
// applies the same IP admission, session creation, and successful limiter
// cleanup as administrator password login. No password path is involved.
func (s *Service) CompleteEmailOTP(ctx context.Context, otpID, code, subject string) (Session, error) {
	if err := s.emailOTPReady(); err != nil {
		return Session{}, err
	}
	id, err := uuid.Parse(otpID)
	if err != nil || len(code) != 6 {
		return Session{}, ErrInvalidCredentials
	}
	if _, err := netip.ParseAddr(subject); err != nil {
		return Session{}, ErrValidation
	}
	now := s.clock.Now().UTC()
	network := rateLimitBucket{hash: rateLimitSubjectHash(s.tokenSecret, "email-otp-complete", "network", subject), limit: networkFailureLimit}
	if err := s.admitRateLimit(ctx, []rateLimitBucket{network}, now); err != nil {
		return Session{}, err
	}
	var issued Session
	var operationErr error
	err = s.pool.InTx(ctx, pgx.TxOptions{}, func(tx pgx.Tx) error {
		store := NewStore(tx)
		administrator, stored, err := store.ConsumeEmailOTP(ctx, id, now)
		if err != nil {
			operationErr = ErrInvalidCredentials
			return nil
		}
		account := rateLimitBucket{hash: rateLimitSubjectHash(s.tokenSecret, "email-otp-complete", "account", administrator.Email), limit: accountFailureLimit, clearOnSuccess: true}
		if err := store.LockRateLimits(ctx, rateLimitLockKeys([]rateLimitBucket{account})); err != nil {
			return err
		}
		blocked, err := rateLimitBlocked(ctx, store, []rateLimitBucket{account}, now)
		if err != nil {
			return err
		}
		if blocked || !hmac.Equal(stored, hashEmailOTPCode(s.tokenSecret, otpID, code)) {
			operationErr = ErrInvalidCredentials
			if !blocked {
				return recordRateLimitAttempt(ctx, store, []rateLimitBucket{account}, now)
			}
			return nil
		}
		if err := authorizeIP(ctx, store, administrator.ID, subject); err != nil {
			if errors.Is(err, ErrIPRestricted) {
				operationErr = ErrInvalidCredentials
				return recordRateLimitAttempt(ctx, store, []rateLimitBucket{account}, now)
			}
			return err
		}
		if err := store.MarkEmailOTPConsumed(ctx, id, now); err != nil {
			return err
		}
		raw, err := GenerateToken()
		if err != nil {
			return err
		}
		expiresAt := now.Add(s.sessionTTL)
		if err := store.CreateSession(ctx, uuid.New(), administrator.ID, HashSessionToken(s.tokenSecret, raw), expiresAt, now); err != nil {
			return err
		}
		if err := clearSuccessfulRateLimits(ctx, store, []rateLimitBucket{account, network}); err != nil {
			return err
		}
		issued = Session{Administrator: administrator, RawToken: raw, ExpiresAt: expiresAt, CSRFToken: DeriveCSRFToken(s.tokenSecret, raw)}
		return nil
	})
	if err != nil {
		return Session{}, err
	}
	if operationErr != nil {
		return Session{}, operationErr
	}
	return issued, nil
}
