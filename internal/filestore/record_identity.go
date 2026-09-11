package filestore

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/yakisoba0728/PgBase/internal/recordidentity"
	"github.com/yakisoba0728/PgBase/internal/schema"
)

// ResolveRecordID translates the exact collection-facing file path identity
// while keeping all filestore ownership and policy checks on internal UUIDs.
func (s *Service) ResolveRecordID(ctx context.Context, collectionName, public string) (uuid.UUID, uuid.UUID, error) {
	if !fieldPattern.MatchString(collectionName) || public == "" || len(public) > 1024 {
		return uuid.Nil, uuid.Nil, ErrInvalid
	}
	var collectionID uuid.UUID
	var kind string
	var raw []byte
	err := s.pool.QueryRow(ctx, `with targets as (
		select id,'auth'::text kind,record_id from pgbase_meta.auth_collections where name=$1
		union all select id,'base'::text,record_id from pgbase_meta.collections where name=$1
	) select id,kind,record_id from targets where (select count(*) from targets)=1`, collectionName).Scan(&collectionID, &kind, &raw)
	if errors.Is(err, pgx.ErrNoRows) {
		return uuid.Nil, uuid.Nil, ErrNotFound
	}
	if err != nil {
		return uuid.Nil, uuid.Nil, err
	}
	var policy schema.RecordIDPolicy
	if json.Unmarshal(raw, &policy) != nil {
		return uuid.Nil, uuid.Nil, ErrInvalid
	}
	identities := recordidentity.New(nil)
	var internal uuid.UUID
	if kind == "auth" {
		internal, err = identities.ResolveAuth(ctx, s.pool, collectionID, policy, public)
	} else {
		internal, err = identities.Resolve(ctx, s.pool, schema.Collection{ID: collectionID, RecordID: policy}, public)
	}
	if errors.Is(err, recordidentity.ErrInvalid) {
		return uuid.Nil, uuid.Nil, ErrInvalid
	}
	if errors.Is(err, recordidentity.ErrNotFound) {
		return uuid.Nil, uuid.Nil, ErrNotFound
	}
	return collectionID, internal, err
}
