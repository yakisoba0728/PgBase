package userauth

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// RefreshSessionForCollection rotates a valid ordinary user session in place.
// The update predicates on the presented token hash, expiry, revocation, active
// user, and collection, so only one concurrent refresh can succeed. A success
// keeps the session ID (and therefore administrative revocation continuity),
// invalidates the old bearer token immediately, and starts a new sliding TTL.
//
// Sessions have no absolute lifetime in this release. If the new bearer token is
// lost after a successful response, the old token cannot be retried; the caller
// must authenticate again. Impersonation sessions are intentionally excluded.
func (s *Service) RefreshSessionForCollection(ctx context.Context, collectionID uuid.UUID, rawToken string) (Session, error) {
	if err := s.ready(); err != nil {
		return Session{}, err
	}
	if collectionID == uuid.Nil {
		return Session{}, ErrValidation
	}
	if err := validateToken(rawToken); err != nil {
		return Session{}, ErrInvalidCredentials
	}
	release, err := admit(s.sessionSlots)
	if err != nil {
		return Session{}, err
	}
	defer release()
	now, err := s.databaseNow(ctx)
	if err != nil {
		return Session{}, err
	}
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return Session{}, fmt.Errorf("generate refreshed user session token: %w", err)
	}
	refreshed := base64.RawURLEncoding.EncodeToString(raw)
	expires := now.Add(s.ttl)
	var user User
	err = s.pool.QueryRow(ctx, `
		update pgbase_meta.user_sessions as sessions
		set token_hash=$1, expires_at=$2
		from pgbase_meta.users as users
		where sessions.token_hash=$3
		  and sessions.revoked_at is null
		  and sessions.expires_at>CURRENT_TIMESTAMP
		  and sessions.session_kind='user'
		  and sessions.user_id=users.id
		  and users.active
		  and users.auth_collection_id=$4
		returning users.id,users.auth_collection_id,users.email,users.verified,users.created_at`,
		s.tokenHash(refreshed), expires, s.tokenHash(rawToken), collectionID).Scan(&user.ID, &user.AuthCollectionID, &user.Email, &user.Verified, &user.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return Session{}, ErrInvalidCredentials
	}
	if err != nil {
		return Session{}, fmt.Errorf("refresh user session: %w", err)
	}
	user.CreatedAt = user.CreatedAt.UTC()
	return Session{User: user, RawToken: refreshed, ExpiresAt: expires}, nil
}
