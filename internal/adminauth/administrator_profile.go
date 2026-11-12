package adminauth

import (
	"context"
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

// UpdateAdministratorCredentials changes the caller's own sign-in email and/or
// password; an empty value leaves that credential unchanged. The current
// password goes through the login limiter first, so a stolen session alone
// cannot take over the account. A password change revokes every session and
// pending email credential of the account, the caller's included.
func (s *Service) UpdateAdministratorCredentials(ctx context.Context, id uuid.UUID, subject, currentPassword, email, newPassword string) (Administrator, error) {
	if err := s.ready(); err != nil {
		return Administrator{}, err
	}
	if _, err := NormalizeEmail(email); (email != "" && err != nil) || (newPassword != "" && validatePassword(newPassword) != nil) || email+newPassword == "" {
		return Administrator{}, ErrValidation
	}
	releaseAdmission, err := acquireAdmission(s.credentialSlots)
	if err != nil {
		return Administrator{}, err
	}
	defer releaseAdmission()
	current, err := NewStore(s.pool).AdministratorByID(ctx, id)
	if err != nil {
		return Administrator{}, err
	}
	now := s.clock.Now()
	candidate, buckets, err := s.checkPasswordAttempt(ctx, current.Email, nil, currentPassword, subject, now, s.currentRatePolicy())
	if err == nil {
		err = s.pool.InTx(ctx, pgx.TxOptions{}, func(tx pgx.Tx) error { return clearSuccessfulRateLimits(ctx, NewStore(tx), buckets) })
	}
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
		if locked.ID != id || !constantTimeStringEqual(storedHash, candidate.passwordHash) {
			return ErrInvalidCredentials
		}
		if hash != "" {
			if _, err := tx.Exec(ctx, `update pgbase_meta.admins set password_hash=$2,updated_at=$3 where id=$1`, id, hash, now.UTC()); err != nil {
				return err
			}
			if err := invalidateAccountCredentials(ctx, tx, id); err != nil {
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

// ReplaceAdministratorIPPrefixes refuses, with ErrIPRestricted, a non-empty
// list that does not admit address, the caller's current address. The CLI is
// the override for that case.
func (s *Service) ReplaceAdministratorIPPrefixes(ctx context.Context, id uuid.UUID, address string, values []string) ([]string, error) {
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
	err := s.pool.InTx(ctx, pgx.TxOptions{}, func(tx pgx.Tx) error { return NewStore(tx).ReplaceIPPrefixes(ctx, id, prefixes) })
	if err != nil {
		return nil, err
	}
	return prefixes, nil
}
