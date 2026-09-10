package userauth

import (
	"context"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

type AuthAuxiliaryRecord struct {
	ID          uuid.UUID  `json:"id"`
	Type        string     `json:"type"`
	Provider    string     `json:"provider,omitempty"`
	Active      *bool      `json:"active,omitempty"`
	ExpiresAt   *time.Time `json:"expiresAt,omitempty"`
	ConsumedAt  *time.Time `json:"consumedAt,omitempty"`
	FirstSeenAt *time.Time `json:"firstSeenAt,omitempty"`
	LastSeenAt  *time.Time `json:"lastSeenAt,omitempty"`
	CreatedAt   time.Time  `json:"createdAt"`
}

type authOriginContextKey struct{}

func WithAuthOrigin(ctx context.Context, address string) context.Context {
	if validateClientAddress(address) != nil {
		return ctx
	}
	return context.WithValue(ctx, authOriginContextKey{}, address)
}

func (s *Service) recordIssuedAuthOrigin(ctx context.Context, session Session, now time.Time) {
	address, _ := ctx.Value(authOriginContextKey{}).(string)
	if address == "" {
		return
	}
	if added, _ := s.recordAuthOrigin(ctx, session.User.ID, address, now); added {
		s.sendAuthAlert(ctx, session.User, "New sign-in origin: "+address)
	}
}

func (s *Service) recordAuthOrigin(ctx context.Context, userID uuid.UUID, address string, now time.Time) (bool, error) {
	hash := s.subjectHash("auth-origin", "network", address)
	var added bool
	err := s.pool.InTx(ctx, pgx.TxOptions{}, func(tx pgx.Tx) error {
		tag, err := tx.Exec(ctx, `insert into pgbase_meta.user_auth_origins(id,user_id,fingerprint_hash,first_seen_at,last_seen_at) values($1,$2,$3,$4,$4) on conflict(user_id,fingerprint_hash) do nothing`, uuid.New(), userID, hash, now)
		if err != nil {
			return err
		}
		added = tag.RowsAffected() == 1
		if !added {
			_, err = tx.Exec(ctx, `update pgbase_meta.user_auth_origins set last_seen_at=$3 where user_id=$1 and fingerprint_hash=$2`, userID, hash, now)
		}
		return err
	})
	return added, err
}

func (s *Service) ListAuthAuxiliary(ctx context.Context, collectionID, userID uuid.UUID, kind string) ([]AuthAuxiliaryRecord, error) {
	if collectionID == uuid.Nil || userID == uuid.Nil {
		return nil, ErrValidation
	}
	if !s.authorizeManageRule(ctx, collectionID, userID, userID) {
		return nil, ErrInvalidCredentials
	}
	var query string
	switch kind {
	case "externalAuths":
		query = `select id,'externalAuth',provider,null::boolean,null::timestamptz,null::timestamptz,null::timestamptz,null::timestamptz,created_at from pgbase_meta.user_oauth_identities where auth_collection_id=$1 and user_id=$2`
	case "mfas":
		query = `select id,'mfa','',active,null::timestamptz,null::timestamptz,null::timestamptz,null::timestamptz,created_at from pgbase_meta.user_totp_factors f where user_id=$2 and exists(select 1 from pgbase_meta.users u where u.id=f.user_id and u.auth_collection_id=$1)`
	case "otps":
		query = `select id,'otp','',null::boolean,expires_at,consumed_at,null::timestamptz,null::timestamptz,created_at from pgbase_meta.user_email_otps where auth_collection_id=$1 and user_id=$2 order by created_at desc limit 100`
	case "authOrigins":
		query = `select id,'authOrigin','',null::boolean,null::timestamptz,null::timestamptz,first_seen_at,last_seen_at,first_seen_at from pgbase_meta.user_auth_origins o where user_id=$2 and exists(select 1 from pgbase_meta.users u where u.id=o.user_id and u.auth_collection_id=$1) order by last_seen_at desc`
	default:
		return nil, ErrValidation
	}
	rows, err := s.pool.Query(ctx, query, collectionID, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := []AuthAuxiliaryRecord{}
	for rows.Next() {
		var v AuthAuxiliaryRecord
		if err := rows.Scan(&v.ID, &v.Type, &v.Provider, &v.Active, &v.ExpiresAt, &v.ConsumedAt, &v.FirstSeenAt, &v.LastSeenAt, &v.CreatedAt); err != nil {
			return nil, err
		}
		result = append(result, v)
	}
	return result, rows.Err()
}

func (s *Service) DeleteAuthAuxiliary(ctx context.Context, collectionID, userID uuid.UUID, kind string, id uuid.UUID) error {
	if collectionID == uuid.Nil || userID == uuid.Nil || id == uuid.Nil {
		return ErrValidation
	}
	if !s.authorizeManageRule(ctx, collectionID, userID, userID) {
		return ErrInvalidCredentials
	}
	var tag pgconn.CommandTag
	var err error
	switch kind {
	case "externalAuths":
		return s.unlinkOAuthIdentityByID(ctx, collectionID, userID, id)
	case "mfas":
		tag, err = s.pool.Exec(ctx, `delete from pgbase_meta.user_totp_factors where user_id=$1 and id=$2`, userID, id)
	case "otps":
		tag, err = s.pool.Exec(ctx, `delete from pgbase_meta.user_email_otps where auth_collection_id=$1 and user_id=$2 and id=$3`, collectionID, userID, id)
	case "authOrigins":
		tag, err = s.pool.Exec(ctx, `delete from pgbase_meta.user_auth_origins where user_id=$1 and id=$2`, userID, id)
	default:
		return ErrValidation
	}
	if err != nil {
		return err
	}
	if tag.RowsAffected() != 1 {
		return ErrNotFound
	}
	return nil
}

func (s *Service) authorizeManageRule(ctx context.Context, collectionID, actorID, targetID uuid.UUID) bool {
	fields, rules, err := s.authRecordRules(ctx, collectionID)
	if err != nil {
		return false
	}
	record, err := s.GetAuthRecord(ctx, collectionID, targetID)
	if err != nil {
		return false
	}
	return s.authorizeManagedAuthRecord(ctx, collectionID, actorID, record, fields, rules.Manage, nil)
}
