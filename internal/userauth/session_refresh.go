package userauth

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"time"

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
	var result Session
	err = s.pool.InTx(ctx, pgx.TxOptions{}, func(tx pgx.Tx) error {
		var lockedRealm uuid.UUID
		if err := tx.QueryRow(ctx, `select id from pgbase_meta.auth_collections where id=$1 for share`, collectionID).Scan(&lockedRealm); err != nil {
			return ErrInvalidCredentials
		}
		oldHash, err := s.sessionHash(ctx, rawToken)
		if err != nil {
			return ErrInvalidCredentials
		}
		refreshed := base64.RawURLEncoding.EncodeToString(raw)
		expires := now.Add(s.ttl)
		newHash := s.tokenHash(refreshed)
		var ttlSeconds int
		var version string
		var cipher []byte
		if err := tx.QueryRow(ctx, `select ttl_seconds,key_version,key_ciphertext from pgbase_meta.auth_token_policies where auth_collection_id=$1 and purpose='session'`, collectionID).Scan(&ttlSeconds, &version, &cipher); err == nil {
			key, decryptErr := s.decrypt("auth-token-policy:session", cipher)
			if decryptErr != nil {
				return decryptErr
			}
			expires = now.Add(time.Duration(ttlSeconds) * time.Second)
			refreshed = collectionID.String() + "." + version + "." + refreshed
			newHash = keyedDigest(key, "user-session\x00", refreshed)
		} else if !errors.Is(err, pgx.ErrNoRows) {
			return err
		}
		var user User
		err = tx.QueryRow(ctx, `
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
			newHash, expires, oldHash, collectionID).Scan(&user.ID, &user.AuthCollectionID, &user.Email, &user.Verified, &user.CreatedAt)
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrInvalidCredentials
		}
		if err != nil {
			return fmt.Errorf("refresh user session: %w", err)
		}
		user.CreatedAt = user.CreatedAt.UTC()
		result = Session{User: user, RawToken: refreshed, ExpiresAt: expires}
		return nil
	})
	return result, err
}
