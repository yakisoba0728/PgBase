package adminauth

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
)

// AuthenticateSessionWithIPPrefixes is AuthenticateSession plus the
// administrator's IP allowlist, read in the same statement so an
// authenticated admin request costs one database round trip. The prefixes are
// exactly those IPPrefixes returns (canonical text); their order does not
// affect addressAllowed.
func (s *Store) AuthenticateSessionWithIPPrefixes(ctx context.Context, tokenHash []byte, now time.Time) (Administrator, time.Time, []string, error) {
	var administrator Administrator
	var expiresAt time.Time
	var prefixes []string
	err := s.db.QueryRow(ctx, `
		with refreshed as (
			update pgbase_meta.admin_sessions as sessions
			set last_seen_at = $2
			from pgbase_meta.admins as admins
			where sessions.token_hash = $1
			  and sessions.admin_id = admins.id
			  and sessions.revoked_at is null
			  and sessions.expires_at > $2
			  and admins.active
			  and sessions.last_seen_at <= $2 - interval '5 minutes'
			returning admins.id, admins.email, admins.active, admins.created_at, admins.updated_at, sessions.expires_at
		), authenticated as (
			select id, email, active, created_at, updated_at, expires_at from refreshed
			union all
			select admins.id, admins.email, admins.active, admins.created_at, admins.updated_at, sessions.expires_at
			from pgbase_meta.admin_sessions as sessions
			join pgbase_meta.admins as admins on admins.id = sessions.admin_id
			where sessions.token_hash = $1
			  and sessions.revoked_at is null
			  and sessions.expires_at > $2
			  and admins.active
			  and not exists (select 1 from refreshed)
		)
		select authenticated.id, authenticated.email, authenticated.active,
		       authenticated.created_at, authenticated.updated_at, authenticated.expires_at,
		       array(
		           select allowlist.prefix::text
		           from pgbase_meta.admin_ip_allowlist as allowlist
		           where allowlist.administrator_id = authenticated.id
		           order by family(allowlist.prefix), masklen(allowlist.prefix), allowlist.prefix
		       )
		from authenticated`, tokenHash, now).Scan(
		&administrator.ID, &administrator.Email, &administrator.Active,
		&administrator.CreatedAt, &administrator.UpdatedAt, &expiresAt, &prefixes)
	if errors.Is(err, pgx.ErrNoRows) {
		return Administrator{}, time.Time{}, nil, ErrInvalidCredentials
	}
	if err != nil {
		return Administrator{}, time.Time{}, nil, fmt.Errorf("authenticate administrator session: %w", err)
	}
	return administratorUTC(administrator), expiresAt.UTC(), prefixes, nil
}

// AuthenticateFromAddress is Authenticate followed by AuthorizeIP for the
// request's client address, in one database round trip. Session failures are
// returned first and unchanged; only an authenticated session is then checked
// against the allowlist, and a rejected (or unresolvable) address yields
// ErrIPRestricted, matching the separate AuthorizeIP call it replaces.
func (s *Service) AuthenticateFromAddress(ctx context.Context, rawToken, address string, addressValid bool) (Principal, error) {
	if err := s.ready(); err != nil {
		return Principal{}, err
	}
	releaseAdmission, err := acquireAdmission(s.sessionSlots)
	if err != nil {
		return Principal{}, err
	}
	defer releaseAdmission()
	administrator, expiresAt, prefixes, err := NewStore(s.pool).AuthenticateSessionWithIPPrefixes(
		ctx, HashSessionToken(s.tokenSecret, rawToken), s.clock.Now())
	if err != nil {
		return Principal{}, err
	}
	if !addressValid {
		return Principal{}, ErrIPRestricted
	}
	if err := addressAllowed(prefixes, address); err != nil {
		return Principal{}, err
	}
	return Principal{
		Administrator: administrator,
		ExpiresAt:     expiresAt,
		CSRFToken:     DeriveCSRFToken(s.tokenSecret, rawToken),
	}, nil
}
