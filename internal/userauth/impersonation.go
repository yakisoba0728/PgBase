package userauth

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/yakisoba0728/PgBase/internal/adminauth"
)

func (s *Service) Impersonate(ctx context.Context, userID uuid.UUID, rawAdminSession string, duration time.Duration) (Session, error) {
	if userID == uuid.Nil || validateToken(rawAdminSession) != nil || duration < time.Minute || duration > 15*time.Minute {
		return Session{}, ErrValidation
	}
	now, err := s.databaseNow(ctx)
	if err != nil {
		return Session{}, err
	}
	tokenBytes := make([]byte, 32)
	if _, err := rand.Read(tokenBytes); err != nil {
		return Session{}, err
	}
	raw := base64.RawURLEncoding.EncodeToString(tokenBytes)
	keyed := false
	var result Session
	err = s.pool.InTx(ctx, pgx.TxOptions{}, func(tx pgx.Tx) error {
		var adminID, adminSessionID uuid.UUID
		var adminExpiry time.Time
		err := tx.QueryRow(ctx, `select a.id,s.id,s.expires_at from pgbase_meta.admin_sessions s join pgbase_meta.admins a on a.id=s.admin_id where s.token_hash=$1 and s.revoked_at is null and s.expires_at>CURRENT_TIMESTAMP and a.active for update of s,a`, adminauth.HashSessionToken(s.secret, rawAdminSession)).Scan(&adminID, &adminSessionID, &adminExpiry)
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrInvalidCredentials
		}
		if err != nil {
			return err
		}
		var user User
		if err := tx.QueryRow(ctx, `select id,auth_collection_id,email,verified,created_at from pgbase_meta.users where id=$1 and active for update`, userID).Scan(&user.ID, &user.AuthCollectionID, &user.Email, &user.Verified, &user.CreatedAt); errors.Is(err, pgx.ErrNoRows) {
			return ErrNotFound
		} else if err != nil {
			return err
		}
		expires := now.Add(duration)
		var configuredTTL int
		var version string
		var cipher []byte
		if err := tx.QueryRow(ctx, `select ttl_seconds,key_version,key_ciphertext from pgbase_meta.auth_token_policies where auth_collection_id=$1 and purpose='impersonation' for share`, user.AuthCollectionID).Scan(&configuredTTL, &version, &cipher); err == nil {
			key, decryptErr := s.decrypt("auth-token-policy:impersonation", cipher)
			if decryptErr != nil {
				return decryptErr
			}
			policyExpiry := now.Add(time.Duration(configuredTTL) * time.Second)
			if policyExpiry.Before(expires) {
				expires = policyExpiry
			}
			raw = user.AuthCollectionID.String() + "." + version + "." + raw
			// Keep impersonation tokens in a separate keyed domain from ordinary
			// sessions, even when a caller accidentally supplies the same key.
			tokenBytes = keyedDigest(key, "user-impersonation\x00", raw)
			keyed = true
		} else if !errors.Is(err, pgx.ErrNoRows) {
			return err
		}
		if adminExpiry.Before(expires) {
			expires = adminExpiry
		}
		if !expires.After(now) {
			return ErrInvalidCredentials
		}
		tokenHash := s.tokenHash(raw)
		if keyed {
			tokenHash = tokenBytes
		}
		_, err = tx.Exec(ctx, `insert into pgbase_meta.user_sessions(id,user_id,token_hash,expires_at,revoked_at,created_at,session_kind,impersonating_admin_id,impersonating_admin_session_id) values($1,$2,$3,$4,null,$5,'impersonation',$6,$7)`, uuid.New(), user.ID, tokenHash, expires, now, adminID, adminSessionID)
		if err != nil {
			return err
		}
		if err := s.projectUser(ctx, tx, &user); err != nil {
			return err
		}
		result = Session{User: user, RawToken: raw, ExpiresAt: expires, Impersonated: true, ImpersonatingAdministratorID: adminID}
		return nil
	})
	return result, err
}
