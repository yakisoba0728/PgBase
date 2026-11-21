package filestore

import (
	"context"
	"errors"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/yakisoba0728/PgBase/internal/identity"
	"github.com/yakisoba0728/PgBase/internal/schema"
)

const recordMetadataLimit = 10000

// ErrRecordMetadataLimit lets the record page fall back to individual file lists.
var ErrRecordMetadataLimit = errors.New("record file metadata exceeds the batch limit")

// ListRecordMetadata batches administrator grid metadata; downloads still reauthorize.
func (s *Service) ListRecordMetadata(ctx context.Context, principal identity.Subject, collectionID uuid.UUID, ids []string) (map[string]map[string][]File, error) {
	return s.ListRecordMetadataFields(ctx, principal, collectionID, ids, nil)
}

// ListRecordMetadataFields optionally restricts the response to named file fields.
func (s *Service) ListRecordMetadataFields(ctx context.Context, principal identity.Subject, collectionID uuid.UUID, ids, names []string) (map[string]map[string][]File, error) {
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
		policies := map[string]bool{}
		var recordIDKind schema.RecordIDKind
		builtIn := s.options.MetadataCatalog != nil
		if builtIn {
			collection, err := s.options.MetadataCatalog(ctx, tx, collectionID)
			if err != nil {
				return err
			}
			recordIDKind = collection.RecordID.Kind
			for _, field := range collection.Fields {
				if field.Type == schema.TypeFile {
					policies[field.Name] = !field.Hidden && field.Options.Protected != nil && !*field.Options.Protected
				}
			}
		} else {
			rows, err := tx.Query(ctx, `select name from pgbase_meta.fields where collection_id=$1 and field_type='file'`, collectionID)
			if err != nil {
				return err
			}
			fields, err := pgx.CollectRows(rows, pgx.RowTo[string])
			if err != nil {
				return err
			}
			for _, field := range fields {
				policies[field] = false
			}
		}
		if len(names) > 0 {
			allowed := make(map[string]bool, len(names))
			for _, field := range names {
				if public, ok := policies[field]; ok {
					allowed[field] = public
				}
			}
			policies = allowed
		}
		fields := make([]string, 0, len(policies))
		for field := range policies {
			fields = append(fields, field)
		}
		for _, id := range ids {
			if id == "" || !utf8.ValidString(id) || utf8.RuneCountInString(id) > 255 {
				return ErrInvalid
			}
			value := make(map[string][]File, len(fields))
			for _, field := range fields {
				value[field] = []File{}
			}
			result[id] = value
		}
		if len(fields) == 0 {
			return nil
		}

		public := make(map[uuid.UUID]string, len(ids))
		if !builtIn || recordIDKind != schema.RecordIDText {
			for _, id := range ids {
				if internal, err := uuid.Parse(id); err == nil {
					public[internal] = id
				}
			}
		}
		if !builtIn || recordIDKind == schema.RecordIDText {
			rows, err := tx.Query(ctx, `select internal_id,external_id from pgbase_meta.record_identities where collection_kind='base' and collection_id=$1 and scope_id=$1 and external_id=any($2::text[])`, collectionID, ids)
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
			if err := rows.Err(); err != nil {
				return err
			}
		}
		internalIDs := make([]uuid.UUID, 0, len(public))
		for id := range public {
			internalIDs = append(internalIDs, id)
		}
		if len(internalIDs) == 0 {
			return nil
		}

		// The built-in administrator authorizer checks collection, field and
		// record membership. The transaction-pinned catalog covers the first
		// two; the join checks record membership for the entire page at once.
		join := ""
		if builtIn {
			join = "join pgbase_meta.record_revisions r on r.collection_id=f.collection_id and r.record_id=f.record_id"
		}
		rows, err := tx.Query(ctx, `select f.id,f.record_id,f.field_name,f.filename,f.content_type,f.size_bytes,coalesce(f.thumbnail_object_key,'')
			from pgbase_meta.record_files f `+join+`
			where f.collection_id=$1 and f.record_id=any($2::uuid[]) and f.field_name=any($3::text[]) and f.status='ready'
			order by f.created_at,f.id limit $4`, collectionID, internalIDs, fields, recordMetadataLimit+1)
		if err != nil {
			return err
		}
		files, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (File, error) {
			file := File{CollectionID: collectionID}
			err := row.Scan(&file.ID, &file.RecordID, &file.Field, &file.Filename, &file.ContentType, &file.Size, &file.ThumbnailKey)
			return file, err
		})
		if err != nil {
			return err
		}
		if len(files) > recordMetadataLimit {
			return ErrRecordMetadataLimit
		}
		type pair struct {
			record uuid.UUID
			field  string
		}
		authorized := map[pair]bool{}
		resolved := map[string]bool{}
		for _, file := range files {
			if !builtIn {
				key := pair{file.RecordID, file.Field}
				if !authorized[key] {
					if err := s.authorizeField(ctx, tx, principal, collectionID, file.RecordID, file.Field, OperationRead); err != nil {
						return err
					}
					authorized[key] = true
				}
				if !resolved[file.Field] {
					policy, err := s.policy(ctx, tx, collectionID, file.Field)
					if err != nil {
						return err
					}
					policies[file.Field] = !*policy.Protected && !policy.Hidden
					resolved[file.Field] = true
				}
			}
			file.Public = policies[file.Field]
			value := result[public[file.RecordID]]
			value[file.Field] = append(value[file.Field], file)
		}
		return nil
	})
	return result, err
}
