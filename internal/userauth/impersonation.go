package userauth

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/webisnoteasy/PgBase/internal/adminauth"
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
		if adminExpiry.Before(expires) {
			expires = adminExpiry
		}
		if !expires.After(now) {
			return ErrInvalidCredentials
		}
		_, err = tx.Exec(ctx, `insert into pgbase_meta.user_sessions(id,user_id,token_hash,expires_at,revoked_at,created_at,session_kind,impersonating_admin_id,impersonating_admin_session_id) values($1,$2,$3,$4,null,$5,'impersonation',$6,$7)`, uuid.New(), user.ID, s.tokenHash(raw), expires, now, adminID, adminSessionID)
		if err != nil {
			return err
		}
		result = Session{User: user, RawToken: raw, ExpiresAt: expires, Impersonated: true, ImpersonatingAdministratorID: adminID}
		return nil
	})
	return result, err
}
