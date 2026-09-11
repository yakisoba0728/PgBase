package adminauth

import (
	"context"
	"encoding/base64"
	"errors"
	"net"
	"net/netip"
	"net/url"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/yakisoba0728/PgBase/internal/mailer"
)

// ResetAdministratorPassword replaces an active administrator password and
// revokes all of that administrator's sessions without issuing a replacement.
func (s *Service) ResetAdministratorPassword(ctx context.Context, email, password string) (Administrator, error) {
	if err := s.ready(); err != nil {
		return Administrator{}, err
	}
	normalizedEmail, err := NormalizeEmail(email)
	if err != nil {
		return Administrator{}, err
	}
	passwordHash, err := s.passwords.Hash(password)
	if err != nil {
		return Administrator{}, err
	}
	now := s.clock.Now().UTC()
	var administrator Administrator
	err = s.pool.InTx(ctx, pgx.TxOptions{}, func(tx pgx.Tx) error {
		var resetErr error
		store := NewStore(tx)
		administrator, resetErr = store.ResetAdministratorPassword(ctx, normalizedEmail, passwordHash, now)
		if resetErr != nil {
			return resetErr
		}
		emailHash := rateLimitSubjectHash(s.tokenSecret, "login", "account", normalizedEmail)
		if err := store.LockRateLimits(ctx, []int64{advisoryKey(emailHash)}); err != nil {
			return err
		}
		return store.ClearRateLimit(ctx, emailHash)
	})
	if err != nil {
		return Administrator{}, err
	}
	return administrator, nil
}

// ConfigurePasswordReset enables mail-backed administrator password recovery.
// It is startup-only configuration so request handling never retains SMTP
// credentials itself.
func (s *Service) ConfigurePasswordReset(sender mailer.Sender, baseURL string, ttl time.Duration) error {
	if s == nil || sender == nil || ttl <= 0 || ttl > maximumPasswordResetTTL {
		return ErrValidation
	}
	if !validPasswordResetURL(baseURL) {
		return ErrValidation
	}
	s.resetMailer = sender
	s.resetBaseURL = strings.TrimRight(baseURL, "/")
	s.resetTTL = ttl
	return nil
}

func validPasswordResetURL(raw string) bool {
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Host == "" || parsed.User != nil || parsed.Fragment != "" {
		return false
	}
	if strings.EqualFold(parsed.Scheme, "https") {
		return true
	}
	if !strings.EqualFold(parsed.Scheme, "http") {
		return false
	}
	host := parsed.Hostname()
	ip := net.ParseIP(host)
	return strings.EqualFold(host, "localhost") || (ip != nil && ip.IsLoopback())
}

func (s *Service) passwordResetReady() error {
	if err := s.ready(); err != nil {
		return err
	}
	if s.resetMailer == nil || s.resetBaseURL == "" || s.resetTTL <= 0 {
		return errors.New("administrator password reset is unavailable")
	}
	return nil
}

// RequestPasswordReset is intentionally indistinguishable for malformed and
// unknown addresses. It reserves account and network rate-limit capacity
// before lookup, then suppresses mail delivery failures from the caller.
func (s *Service) RequestPasswordReset(ctx context.Context, email, clientAddress string) error {
	if err := s.passwordResetReady(); err != nil {
		return err
	}
	if _, err := netip.ParseAddr(clientAddress); err != nil {
		return ErrValidation
	}
	normalized, emailErr := NormalizeEmail(email)
	account := normalized
	if emailErr != nil {
		account = "<invalid>"
	}
	now := s.clock.Now().UTC()
	policy := s.currentRatePolicy()
	buckets := []rateLimitBucket{
		{hash: rateLimitSubjectHash(s.tokenSecret, "password-reset", "account", account), limit: policy.AccountMaxFailures, window: policy.Window, block: policy.Block},
		{hash: rateLimitSubjectHash(s.tokenSecret, "password-reset", "network", clientAddress), limit: policy.NetworkMaxFailures, window: policy.Window, block: policy.Block},
	}
	if err := s.admitRateLimit(ctx, buckets, now); err != nil {
		return err
	}
	if emailErr != nil {
		return nil
	}
	administratorID, err := NewStore(s.pool).AdministratorID(ctx, normalized)
	if errors.Is(err, ErrInvalidCredentials) {
		return nil
	}
	if err != nil {
		return err
	}
	raw, err := GenerateToken()
	if err != nil {
		return err
	}
	if err := s.pool.InTx(ctx, pgx.TxOptions{}, func(tx pgx.Tx) error {
		return NewStore(tx).CreatePasswordReset(ctx, hashPasswordResetToken(s.tokenSecret, raw), administratorID, now.Add(s.resetTTL))
	}); err != nil {
		return err
	}
	message := mailer.Message{
		Purpose: "admin_password_reset", To: normalized, Subject: "Reset your PgBase administrator password",
		Text: s.resetBaseURL + "/admin/reset-password#token=" + url.QueryEscape(raw),
	}
	// Deliberately preserve the enumeration-safe response when SMTP is down.
	_ = s.resetMailer.Send(ctx, message)
	return nil
}

func (s *Service) ConfirmPasswordReset(ctx context.Context, rawToken, password string) error {
	if err := s.passwordResetReady(); err != nil {
		return err
	}
	decoded, decodeErr := base64.RawURLEncoding.DecodeString(rawToken)
	if decodeErr != nil || len(decoded) != tokenBytes || password == "" {
		return ErrInvalidCredentials
	}
	release, err := acquireAdmission(s.passwordSlots)
	if err != nil {
		return err
	}
	passwordHash, err := s.passwords.Hash(password)
	release()
	if err != nil {
		return err
	}
	now := s.clock.Now().UTC()
	return s.pool.InTx(ctx, pgx.TxOptions{}, func(tx pgx.Tx) error {
		store := NewStore(tx)
		administratorID, err := store.ConsumePasswordReset(ctx, hashPasswordResetToken(s.tokenSecret, rawToken), now)
		if err != nil {
			return err
		}
		_, err = store.ResetAdministratorPasswordByID(ctx, administratorID, passwordHash, now)
		return err
	})
}
