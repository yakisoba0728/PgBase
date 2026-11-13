package adminauth

import (
	"context"
	"errors"
	"net/netip"
	"sort"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

func (s *Service) Administrator(ctx context.Context, id uuid.UUID) (Administrator, error) {
	if err := s.ready(); err != nil {
		return Administrator{}, err
	}
	return NewStore(s.pool).AdministratorByID(ctx, id)
}

// ErrCurrentPasswordInvalid reports a failed step-up re-verification of the
// acting administrator's own password. The session itself stays valid.
var ErrCurrentPasswordInvalid = errors.New("current administrator password is missing or incorrect")

// confirmCurrentPassword is the step-up check for administrator-security
// changes: the acting administrator's password goes through the login limiter
// (the login buckets, keyed by subject), so a stolen session alone cannot take
// over an account. It returns the verified hash for the caller to re-check
// under its row lock.
func (s *Service) confirmCurrentPassword(ctx context.Context, id uuid.UUID, subject, password string) (Administrator, string, error) {
	releaseAdmission, err := acquireAdmission(s.credentialSlots)
	if err != nil {
		return Administrator{}, "", err
	}
	defer releaseAdmission()
	current, err := NewStore(s.pool).AdministratorByID(ctx, id)
	if err != nil {
		return Administrator{}, "", err
	}
	candidate, buckets, err := s.checkPasswordAttempt(ctx, current.Email, nil, password, subject, s.clock.Now(), s.currentRatePolicy())
	if errors.Is(err, ErrInvalidCredentials) {
		return Administrator{}, "", ErrCurrentPasswordInvalid
	}
	if err == nil {
		err = s.pool.InTx(ctx, pgx.TxOptions{}, func(tx pgx.Tx) error { return clearSuccessfulRateLimits(ctx, NewStore(tx), buckets) })
	}
	return current, candidate.passwordHash, err
}

// UpdateAdministratorCredentials changes the caller's own sign-in email and/or
// password; an empty value leaves that credential unchanged. It requires the
// current password (confirmCurrentPassword). A password change revokes every
// session and pending email credential of the account, the caller's included;
// an email change revokes the pending email credentials sent to the old address.
func (s *Service) UpdateAdministratorCredentials(ctx context.Context, id uuid.UUID, subject, currentPassword, email, newPassword string) (Administrator, error) {
	if err := s.ready(); err != nil {
		return Administrator{}, err
	}
	normalized, err := NormalizeEmail(email)
	if (email != "" && err != nil) || (newPassword != "" && validatePassword(newPassword) != nil) || email+newPassword == "" {
		return Administrator{}, ErrValidation
	}
	current, verified, err := s.confirmCurrentPassword(ctx, id, subject, currentPassword)
	now := s.clock.Now()
	hash := ""
	if err == nil && newPassword != "" {
		hash, err = s.hashPassword(ctx, newPassword)
	}
	if err != nil {
		return Administrator{}, err
	}
	var result Administrator
	err = s.pool.InTx(ctx, pgx.TxOptions{}, func(tx pgx.Tx) error {
		store := NewStore(tx)
		// The password verified above must still be the stored one.
		locked, storedHash, err := store.AdministratorByEmailForUpdate(ctx, current.Email)
		if err != nil {
			return err
		}
		if locked.ID != id || !constantTimeStringEqual(storedHash, verified) {
			return ErrCurrentPasswordInvalid
		}
		if hash != "" {
			if _, err := tx.Exec(ctx, `update pgbase_meta.admins set password_hash=$2,updated_at=$3 where id=$1`, id, hash, now.UTC()); err != nil {
				return err
			}
			if err := invalidateAccountCredentials(ctx, tx, id); err != nil {
				return err
			}
		} else if email != "" && normalized != current.Email {
			// Pending reset links and OTPs were delivered to the old address.
			if err := invalidatePendingEmailCredentials(ctx, tx, id); err != nil {
				return err
			}
		}
		if email == "" {
			email = current.Email
		}
		result, err = store.UpdateAdministratorEmail(ctx, id, email, now)
		return err
	})
	return result, err
}

func (s *Service) AdministratorIPPrefixes(ctx context.Context, id uuid.UUID) ([]string, error) {
	if err := s.ready(); err != nil {
		return nil, err
	}
	return NewStore(s.pool).IPPrefixes(ctx, id)
}

// ReplaceAdministratorIPPrefixes requires the current password
// (confirmCurrentPassword) and refuses, with ErrIPRestricted, a non-empty list
// that does not admit address, the caller's current address. The CLI is the
// override for that case.
func (s *Service) ReplaceAdministratorIPPrefixes(ctx context.Context, id uuid.UUID, address, subject, currentPassword string, values []string) ([]string, error) {
	if err := s.ready(); err != nil {
		return nil, err
	}
	if len(values) > 64 {
		return nil, ErrValidation
	}
	prefixes := make([]string, 0, len(values))
	seen := make(map[string]struct{}, len(values))
	for _, raw := range values {
		prefix, err := netip.ParsePrefix(strings.TrimSpace(raw))
		if err != nil || prefix.Bits() == 0 || prefix != prefix.Masked() {
			return nil, ErrValidation
		}
		value := prefix.String()
		if _, exists := seen[value]; exists {
			continue
		}
		seen[value] = struct{}{}
		prefixes = append(prefixes, value)
	}
	sort.Slice(prefixes, func(i, j int) bool {
		a, _ := netip.ParsePrefix(prefixes[i])
		b, _ := netip.ParsePrefix(prefixes[j])
		if a.Addr().BitLen() != b.Addr().BitLen() {
			return a.Addr().BitLen() < b.Addr().BitLen()
		}
		if a.Bits() != b.Bits() {
			return a.Bits() < b.Bits()
		}
		return a.Addr().Less(b.Addr())
	})
	if err := addressAllowed(prefixes, address); err != nil {
		return nil, err
	}
	_, verified, err := s.confirmCurrentPassword(ctx, id, subject, currentPassword)
	if err != nil {
		return nil, err
	}
	err = s.pool.InTx(ctx, pgx.TxOptions{}, func(tx pgx.Tx) error {
		if err := activeAccountActor(ctx, tx, id, verified); err != nil {
			return err
		}
		return NewStore(tx).ReplaceIPPrefixes(ctx, id, prefixes)
	})
	if err != nil {
		return nil, err
	}
	return prefixes, nil
}
