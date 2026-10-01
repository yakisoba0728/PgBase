package filestore

import (
	"context"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/yakisoba0728/PgBase/internal/identity"
	"io"
)

// OpenPublic opens an explicitly public file field. It never accepts a token
// and still resolves the file and field policy transactionally, so changing a
// field back to protected immediately revokes public URLs.
func (s *Service) OpenPublic(ctx context.Context, id uuid.UUID, variant Variant) (File, io.ReadCloser, error) {
	file, err := s.authorizePublicOpen(ctx, id, variant)
	if err != nil {
		return File{}, nil, err
	}
	return s.openAuthorizedObject(ctx, file, identity.Subject{Kind: identity.Anonymous}, variant)
}

func (s *Service) authorizePublicOpen(ctx context.Context, id uuid.UUID, variant Variant) (File, error) {
	if id == uuid.Nil || !validVariant(variant) {
		return File{}, ErrInvalid
	}
	var file File
	err := s.pool.InTx(ctx, pgx.TxOptions{}, func(tx pgx.Tx) error {
		if err := scanFile(tx.QueryRow(ctx, `SELECT id,coalesce(collection_id,auth_collection_id),coalesce(record_id,auth_user_id),coalesce(owner_id,'00000000-0000-0000-0000-000000000000'),field_name,filename,content_type,size_bytes,sha256,object_key,coalesce(thumbnail_object_key,''),created_at,ready_at FROM pgbase_meta.record_files WHERE id=$1 AND status='ready'`, id), &file); err != nil {
			return err
		}
		policy, err := s.resolvedPolicy(ctx, tx, identity.Subject{Kind: identity.Anonymous}, file.CollectionID, file.RecordID, file.Field, OperationRead)
		if err != nil {
			return err
		}
		if *policy.Protected || policy.Hidden {
			return ErrNotFound
		}
		if variant != Original && variant != Thumbnail && !containsVariant(policy.Thumbs, string(variant)) {
			return ErrNotFound
		}
		return nil
	})
	if err != nil {
		return File{}, err
	}
	return file, nil
}

func (s *Service) OpenPublicRange(ctx context.Context, id uuid.UUID, variant Variant, requested ByteRange) (File, RangeRead, error) {
	file, err := s.authorizePublicOpen(ctx, id, variant)
	if err != nil {
		return File{}, RangeRead{}, err
	}
	return s.openAuthorizedRange(ctx, file, identity.Subject{Kind: identity.Anonymous}, variant, requested)
}
