// Package serviceauth manages scoped bearer credentials for automation clients.
package serviceauth

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/yakisoba0728/PgBase/internal/database"
	"github.com/yakisoba0728/PgBase/internal/identity"
)

var ErrValidation = errors.New("invalid service token request")
var ErrInvalidToken = errors.New("invalid service token")
var ErrNotFound = errors.New("service token not found")

const maximumTTL = 365 * 24 * time.Hour

type Scope struct {
	CollectionID uuid.UUID `json:"collectionId"`
	Action       string    `json:"action"`
}
type Token struct {
	ID        uuid.UUID
	Name      string
	Active    bool
	ExpiresAt time.Time
	RevokedAt *time.Time
	CreatedAt time.Time
	Scopes    []Scope
}
type IssuedToken struct {
	Token
	RawToken string
}
type Principal struct {
	Subject   identity.Subject
	ExpiresAt time.Time
}
type Service struct {
	pool   *database.Pool
	secret []byte
}

func NewService(pool *database.Pool, secret []byte) *Service {
	return &Service{pool: pool, secret: append([]byte(nil), secret...)}
}

func (s *Service) Issue(ctx context.Context, name string, ttl time.Duration, scopes []Scope) (IssuedToken, error) {
	name = strings.TrimSpace(name)
	if s == nil || s.pool == nil || len(s.secret) < 32 || name == "" || len(name) > 128 || ttl <= 0 || ttl > maximumTTL || !validScopes(scopes) {
		return IssuedToken{}, ErrValidation
	}
	rawBytes := make([]byte, 32)
	if _, err := rand.Read(rawBytes); err != nil {
		return IssuedToken{}, err
	}
	raw := base64.RawURLEncoding.EncodeToString(rawBytes)
	now, err := s.now(ctx)
	if err != nil {
		return IssuedToken{}, err
	}
	value := Token{ID: uuid.New(), Name: name, Active: true, ExpiresAt: now.Add(ttl), CreatedAt: now, Scopes: scopes}
	err = s.pool.InTx(ctx, pgx.TxOptions{}, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `insert into pgbase_meta.service_tokens(id,name,token_hash,active,expires_at,created_at) values($1,$2,$3,true,$4,$5)`, value.ID, value.Name, s.hash(raw), value.ExpiresAt, now); err != nil {
			return err
		}
		for _, scope := range scopes {
			if _, err := tx.Exec(ctx, `insert into pgbase_meta.service_token_scopes(token_id,collection_id,action) values($1,$2,$3)`, value.ID, scope.CollectionID, scope.Action); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return IssuedToken{}, err
	}
	return IssuedToken{Token: value, RawToken: raw}, nil
}
func (s *Service) Authenticate(ctx context.Context, raw string) (Principal, error) {
	if len(raw) < 32 || len(raw) > 128 {
		return Principal{}, ErrInvalidToken
	}
	var p Principal
	var id uuid.UUID
	err := s.pool.QueryRow(ctx, `select id,expires_at from pgbase_meta.service_tokens where token_hash=$1 and active and revoked_at is null and expires_at>CURRENT_TIMESTAMP`, s.hash(raw)).Scan(&id, &p.ExpiresAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return Principal{}, ErrInvalidToken
	}
	if err != nil {
		return Principal{}, err
	}
	p.Subject = identity.Subject{Kind: identity.Kind("service"), ID: id}
	return p, nil
}

// Authorize locks the live token row so revocation and the protected operation
// serialize when called with the operation transaction.
func (s *Service) Authorize(ctx context.Context, tx pgx.Tx, subject identity.Subject, collectionID uuid.UUID, action string) error {
	return Authorize(ctx, tx, subject, collectionID, action)
}

func Authorize(ctx context.Context, tx pgx.Tx, subject identity.Subject, collectionID uuid.UUID, action string) error {
	if subject.Kind != identity.Kind("service") || subject.ID == uuid.Nil || !validAction(action) {
		return ErrInvalidToken
	}
	var id uuid.UUID
	err := tx.QueryRow(ctx, `select t.id from pgbase_meta.service_tokens t join pgbase_meta.service_token_scopes s on s.token_id=t.id where t.id=$1 and t.active and t.revoked_at is null and t.expires_at>CURRENT_TIMESTAMP and s.collection_id=$2 and s.action=$3 for share of t`, subject.ID, collectionID, action).Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrInvalidToken
	}
	return err
}
func (s *Service) Revoke(ctx context.Context, id uuid.UUID) error {
	if id == uuid.Nil {
		return ErrValidation
	}
	tag, err := s.pool.Exec(ctx, `update pgbase_meta.service_tokens set active=false,revoked_at=coalesce(revoked_at,CURRENT_TIMESTAMP) where id=$1`, id)
	if err != nil {
		return err
	}
	if tag.RowsAffected() != 1 {
		return ErrNotFound
	}
	return nil
}
func (s *Service) List(ctx context.Context) ([]Token, error) {
	rows, err := s.pool.Query(ctx, `with listed as (
		select * from pgbase_meta.service_tokens order by created_at desc,id limit 64
	) select t.id,t.name,t.active,t.expires_at,t.revoked_at,t.created_at,s.collection_id,s.action
	  from listed t left join pgbase_meta.service_token_scopes s on s.token_id=t.id
	 order by t.created_at desc,t.id,s.collection_id,s.action`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	values := make([]Token, 0)
	index := map[uuid.UUID]int{}
	for rows.Next() {
		var v Token
		var collection *uuid.UUID
		var action *string
		if err := rows.Scan(&v.ID, &v.Name, &v.Active, &v.ExpiresAt, &v.RevokedAt, &v.CreatedAt, &collection, &action); err != nil {
			return nil, err
		}
		i, ok := index[v.ID]
		if !ok {
			index[v.ID] = len(values)
			values = append(values, v)
			i = len(values) - 1
		}
		if collection != nil {
			values[i].Scopes = append(values[i].Scopes, Scope{*collection, *action})
		}
	}
	return values, rows.Err()
}
func (s *Service) hash(raw string) []byte {
	m := hmac.New(sha256.New, s.secret)
	m.Write([]byte("service-token\x00"))
	m.Write([]byte(raw))
	return m.Sum(nil)
}
func (s *Service) now(ctx context.Context) (time.Time, error) {
	var now time.Time
	if err := s.pool.QueryRow(ctx, `select CURRENT_TIMESTAMP`).Scan(&now); err != nil {
		return now, fmt.Errorf("read service token time: %w", err)
	}
	return now.UTC(), nil
}
func validScopes(v []Scope) bool {
	if len(v) < 1 || len(v) > 256 {
		return false
	}
	seen := map[string]bool{}
	for _, s := range v {
		key := s.CollectionID.String() + "/" + s.Action
		if s.CollectionID == uuid.Nil || !validAction(s.Action) || seen[key] {
			return false
		}
		seen[key] = true
	}
	return true
}
func validAction(v string) bool {
	return v == "read" || v == "create" || v == "update" || v == "delete"
}
