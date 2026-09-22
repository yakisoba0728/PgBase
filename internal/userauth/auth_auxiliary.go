package userauth

import (
	"context"
	"crypto/hmac"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/yakisoba0728/PgBase/internal/identity"
)

// AuthAuxiliaryQuery is the bounded administrator query contract used by the
// PocketBase compatibility adapter. Empty UUID fields are omitted filters.
type AuthAuxiliaryQuery struct {
	Kind, Provider, Fingerprint string
	CollectionID, UserID, ID    uuid.UUID
}

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
	return s.listAuthAuxiliary(ctx, collectionID, userID, kind)
}

// AdminListAuthAuxiliary is the trusted administrator variant; callers must
// supply an authenticated administrator identity rather than a target user.
func (s *Service) AdminListAuthAuxiliary(ctx context.Context, administrator identity.Subject, collectionID, userID uuid.UUID, kind string) ([]AuthAuxiliaryRecord, error) {
	if administrator.Kind != identity.Administrator || administrator.ID == uuid.Nil || collectionID == uuid.Nil || userID == uuid.Nil {
		return nil, ErrValidation
	}
	if _, err := s.GetAuthRecord(ctx, collectionID, userID); err != nil {
		return nil, err
	}
	return s.listAuthAuxiliary(ctx, collectionID, userID, kind)
}
func (s *Service) listAuthAuxiliary(ctx context.Context, collectionID, userID uuid.UUID, kind string) ([]AuthAuxiliaryRecord, error) {
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
	return s.deleteAuthAuxiliary(ctx, collectionID, userID, kind, id)
}
func (s *Service) AdminDeleteAuthAuxiliary(ctx context.Context, administrator identity.Subject, collectionID, userID uuid.UUID, kind string, id uuid.UUID) error {
	if administrator.Kind != identity.Administrator || administrator.ID == uuid.Nil || collectionID == uuid.Nil || userID == uuid.Nil || id == uuid.Nil {
		return ErrValidation
	}
	if _, err := s.GetAuthRecord(ctx, collectionID, userID); err != nil {
		return err
	}
	return s.deleteAuthAuxiliary(ctx, collectionID, userID, kind, id)
}

// AdminQueryAuthAuxiliary implements the exact collection/record/id helpers
// without exposing credential material or accepting raw SQL.
func (s *Service) AdminQueryAuthAuxiliary(ctx context.Context, administrator identity.Subject, q AuthAuxiliaryQuery) ([]AuthAuxiliaryRecord, error) {
	if administrator.Kind != identity.Administrator || administrator.ID == uuid.Nil {
		return nil, ErrValidation
	}
	args := []any{}
	where := []string{"true"}
	add := func(sql string, value any) {
		args = append(args, value)
		where = append(where, sql+"$"+fmt.Sprint(len(args)))
	}
	var query string
	switch q.Kind {
	case "externalAuths":
		query = `select x.id,'externalAuth',x.provider,null::boolean,null::timestamptz,null::timestamptz,null::timestamptz,null::timestamptz,x.created_at from pgbase_meta.user_oauth_identities x`
		if q.CollectionID != uuid.Nil {
			add("x.auth_collection_id=", q.CollectionID)
		}
		if q.UserID != uuid.Nil {
			add("x.user_id=", q.UserID)
		}
		if q.ID != uuid.Nil {
			add("x.id=", q.ID)
		}
		if q.Provider != "" {
			add("x.provider=", q.Provider)
		}
	case "mfas":
		query = `select f.id,'mfa','',f.active,null::timestamptz,null::timestamptz,null::timestamptz,null::timestamptz,f.created_at from pgbase_meta.user_totp_factors f join pgbase_meta.users u on u.id=f.user_id`
		if q.CollectionID != uuid.Nil {
			add("u.auth_collection_id=", q.CollectionID)
		}
		if q.UserID != uuid.Nil {
			add("f.user_id=", q.UserID)
		}
		if q.ID != uuid.Nil {
			add("f.id=", q.ID)
		}
	case "otps":
		query = `select o.id,'otp','',null::boolean,o.expires_at,o.consumed_at,null::timestamptz,null::timestamptz,o.created_at from pgbase_meta.user_email_otps o`
		if q.CollectionID != uuid.Nil {
			add("o.auth_collection_id=", q.CollectionID)
		}
		if q.UserID != uuid.Nil {
			add("o.user_id=", q.UserID)
		}
		if q.ID != uuid.Nil {
			add("o.id=", q.ID)
		}
	case "authOrigins":
		query = `select o.id,'authOrigin','',null::boolean,null::timestamptz,null::timestamptz,o.first_seen_at,o.last_seen_at,o.first_seen_at from pgbase_meta.user_auth_origins o join pgbase_meta.users u on u.id=o.user_id`
		if q.CollectionID != uuid.Nil {
			add("u.auth_collection_id=", q.CollectionID)
		}
		if q.UserID != uuid.Nil {
			add("o.user_id=", q.UserID)
		}
		if q.ID != uuid.Nil {
			add("o.id=", q.ID)
		}
		if q.Fingerprint != "" {
			add("o.fingerprint_hash=", s.subjectHash("auth-origin", "network", q.Fingerprint))
		}
	default:
		return nil, ErrValidation
	}
	query += " where " + strings.Join(where, " and ") + " order by 9 desc limit 1000"
	rows, err := s.pool.Query(ctx, query, args...)
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

// AdminDeleteAuthAuxiliaryScope atomically deletes a whole record scope or
// expired global scope. External-auth deletion retains the native last-login guard.
func (s *Service) AdminDeleteAuthAuxiliaryScope(ctx context.Context, administrator identity.Subject, q AuthAuxiliaryQuery, expired bool) error {
	if administrator.Kind != identity.Administrator || administrator.ID == uuid.Nil {
		return ErrValidation
	}
	return s.pool.InTx(ctx, pgx.TxOptions{}, func(tx pgx.Tx) error {
		var tag pgconn.CommandTag
		var err error
		switch q.Kind {
		case "externalAuths":
			if q.CollectionID == uuid.Nil || q.UserID == uuid.Nil || expired {
				return ErrValidation
			}
			var actual uuid.UUID
			var password, otp bool
			var count int
			if err = tx.QueryRow(ctx, `select auth_collection_id,password_enabled from pgbase_meta.users where id=$1 and active for update`, q.UserID).Scan(&actual, &password); err != nil {
				return err
			}
			if actual != q.CollectionID {
				return ErrNotFound
			}
			if err = tx.QueryRow(ctx, `select otp_enabled from pgbase_meta.auth_collection_policies where auth_collection_id=$1 for share`, actual).Scan(&otp); err != nil {
				return err
			}
			if err = tx.QueryRow(ctx, `select count(*) from pgbase_meta.user_oauth_identities where auth_collection_id=$1 and user_id=$2`, actual, q.UserID).Scan(&count); err != nil {
				return err
			}
			if count > 0 && !password && !otp {
				return ErrValidation
			}
			tag, err = tx.Exec(ctx, `delete from pgbase_meta.user_oauth_identities where auth_collection_id=$1 and user_id=$2`, actual, q.UserID)
		case "mfas":
			if expired {
				tag, err = tx.Exec(ctx, `delete from pgbase_meta.user_mfa_challenges where expires_at<=CURRENT_TIMESTAMP or consumed_at is not null`)
			} else {
				tag, err = tx.Exec(ctx, `delete from pgbase_meta.user_totp_factors f using pgbase_meta.users u where f.user_id=u.id and u.auth_collection_id=$1 and f.user_id=$2`, q.CollectionID, q.UserID)
			}
		case "otps":
			if expired {
				tag, err = tx.Exec(ctx, `delete from pgbase_meta.user_email_otps where expires_at<=CURRENT_TIMESTAMP or consumed_at is not null`)
			} else {
				tag, err = tx.Exec(ctx, `delete from pgbase_meta.user_email_otps where auth_collection_id=$1 and user_id=$2`, q.CollectionID, q.UserID)
			}
		case "authOrigins":
			if expired {
				return ErrValidation
			}
			tag, err = tx.Exec(ctx, `delete from pgbase_meta.user_auth_origins o using pgbase_meta.users u where o.user_id=u.id and u.auth_collection_id=$1 and o.user_id=$2`, q.CollectionID, q.UserID)
		default:
			return ErrValidation
		}
		_ = tag
		return err
	})
}

func (s *Service) AdminFindAuthRecordByToken(ctx context.Context, administrator identity.Subject, token string, validTypes []string) (AuthRecord, error) {
	if administrator.Kind != identity.Administrator || administrator.ID == uuid.Nil || token == "" {
		return AuthRecord{}, ErrValidation
	}
	allowed := len(validTypes) == 0
	for _, v := range validTypes {
		if v == "auth" {
			allowed = true
		}
	}
	if allowed {
		p, err := s.Authenticate(ctx, token)
		if err == nil {
			return s.GetAuthRecord(ctx, p.User.AuthCollectionID, p.User.ID)
		}
	}
	typePurpose := map[string]string{"verifyEmail": "verify", "changeEmail": "email_change", "passwordReset": "password_reset"}
	for publicType, purpose := range typePurpose {
		accept := len(validTypes) == 0
		for _, v := range validTypes {
			if v == publicType {
				accept = true
			}
		}
		if !accept {
			continue
		}
		hash := s.actionTokenHash(purpose, token)
		if version, _, ok := splitPolicyToken(token); ok {
			var cipher []byte
			if err := s.pool.QueryRow(ctx, `select key_ciphertext from pgbase_meta.auth_token_policies where key_version=$1 and purpose=$2`, version, purpose).Scan(&cipher); err != nil {
				continue
			}
			key, err := s.decrypt("auth-token-policy:"+purpose, cipher)
			if err != nil {
				continue
			}
			hash = keyedDigest(key, "user-email-action\x00"+purpose+"\x00", token)
		}
		var id, collection uuid.UUID
		var credential []byte
		var encoded, email string
		err := s.pool.QueryRow(ctx, `select u.id,u.auth_collection_id,a.credential_hash,u.password_hash,u.email from pgbase_meta.user_email_actions a join pgbase_meta.users u on u.id=a.user_id where a.token_hash=$1 and a.purpose=$2 and a.consumed_at is null and a.expires_at>CURRENT_TIMESTAMP and u.active`, hash, purpose).Scan(&id, &collection, &credential, &encoded, &email)
		if err == nil && hmac.Equal(credential, s.emailActionCredentialHash(purpose, encoded, email)) {
			return s.GetAuthRecord(ctx, collection, id)
		}
	}
	return AuthRecord{}, ErrInvalidCredentials
}

func (s *Service) AdminFindAuthRecordByEmail(ctx context.Context, administrator identity.Subject, collectionID uuid.UUID, email string) (AuthRecord, error) {
	if administrator.Kind != identity.Administrator || administrator.ID == uuid.Nil || collectionID == uuid.Nil {
		return AuthRecord{}, ErrValidation
	}
	email, err := normalizeEmail(email)
	if err != nil {
		return AuthRecord{}, err
	}
	var id uuid.UUID
	err = s.pool.QueryRow(ctx, `select id from pgbase_meta.users where auth_collection_id=$1 and email=$2`, collectionID, email).Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) {
		return AuthRecord{}, ErrNotFound
	}
	if err != nil {
		return AuthRecord{}, err
	}
	return s.GetAuthRecord(ctx, collectionID, id)
}
func (s *Service) deleteAuthAuxiliary(ctx context.Context, collectionID, userID uuid.UUID, kind string, id uuid.UUID) error {
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
