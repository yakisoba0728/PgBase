package filestore

import (
	"context"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/yakisoba0728/PgBase/internal/identity"
)

// ListRecordMetadata batches administrator grid metadata; downloads still reauthorize.
func (s *Service) ListRecordMetadata(ctx context.Context, principal identity.Subject, collectionID uuid.UUID, ids []string) (map[string]map[string][]File, error) {
	if !principal.Valid() || principal.Kind != identity.Administrator {
		return nil, ErrDenied
	}
	if collectionID == uuid.Nil || len(ids) > 200 {
		return nil, ErrInvalid
	}
	result := make(map[string]map[string][]File, len(ids))
	if len(ids) == 0 {
		return result, nil
	}
	err := s.pool.InTx(ctx, pgx.TxOptions{}, func(tx pgx.Tx) error {
		public := map[uuid.UUID]string{}
		for _, id := range ids {
			if len(id) == 0 || len(id) > 255 {
				return ErrInvalid
			}
			if internal, err := uuid.Parse(id); err == nil {
				public[internal] = id
			}
			result[id] = map[string][]File{}
		}
		rows, err := tx.Query(ctx, `select internal_id,external_id from pgbase_meta.record_identities where collection_kind='base' and collection_id=$1 and external_id=any($2::text[])`, collectionID, ids)
		if err != nil {
			return err
		}
		for rows.Next() {
			var id uuid.UUID
			var value string
			if err := rows.Scan(&id, &value); err != nil {
				rows.Close()
				return err
			}
			public[id] = value
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return err
		}
		fields, err := tx.Query(ctx, `select name from pgbase_meta.fields where collection_id=$1 and field_type='file'`, collectionID)
		if err != nil {
			return err
		}
		for fields.Next() {
			var field string
			if err := fields.Scan(&field); err != nil {
				fields.Close()
				return err
			}
			for _, value := range result {
				value[field] = []File{}
			}
		}
		err = fields.Err()
		fields.Close()
		if err != nil {
			return err
		}
		internalIDs := make([]uuid.UUID, 0, len(public))
		for id := range public {
			internalIDs = append(internalIDs, id)
		}
		rows, err = tx.Query(ctx, `select id,record_id,field_name,filename,content_type,size_bytes,coalesce(thumbnail_object_key,'') from pgbase_meta.record_files where collection_id=$1 and record_id=any($2::uuid[]) and status='ready' order by created_at,id limit 10001`, collectionID, internalIDs)
		if err != nil {
			return err
		}
		files := []File{}
		for rows.Next() {
			var file File
			file.CollectionID = collectionID
			if err := rows.Scan(&file.ID, &file.RecordID, &file.Field, &file.Filename, &file.ContentType, &file.Size, &file.ThumbnailKey); err != nil {
				rows.Close()
				return err
			}
			files = append(files, file)
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return err
		}
		if len(files) > 10000 {
			return ErrInvalid
		}
		policies := map[string]FieldPolicy{}
		authorized := map[string]bool{}
		for _, file := range files {
			key := file.RecordID.String() + ":" + file.Field
			if !authorized[key] {
				if err := s.authorizeField(ctx, tx, principal, collectionID, file.RecordID, file.Field, OperationRead); err != nil {
					return err
				}
				authorized[key] = true
			}
			policy, ok := policies[file.Field]
			if !ok {
				policy, err = s.policy(ctx, tx, collectionID, file.Field)
				if err != nil {
					return err
				}
				policies[file.Field] = policy
			}
			file.Public = !*policy.Protected && !policy.Hidden
			value := result[public[file.RecordID]]
			value[file.Field] = append(value[file.Field], file)
		}
		return nil
	})
	return result, err
}
