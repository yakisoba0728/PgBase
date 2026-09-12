package recordops

import (
	"context"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/yakisoba0728/PgBase/internal/database"
	"github.com/yakisoba0728/PgBase/internal/recordaccess"
	"github.com/yakisoba0728/PgBase/internal/schema"
)

type TruncateRequest struct {
	Principal  Principal
	RequestID  string
	Collection string
}

type TruncateResult struct {
	Name             string `json:"name"`
	Truncated        bool   `json:"truncated"`
	RecordsDeleted   int64  `json:"recordsDeleted"`
	FilesDeleted     int64  `json:"filesDeleted"`
	RelationsDeleted int64  `json:"relationsDeleted"`
}

// Truncate deletes every record through the ordinary delete graph in one
// transaction. This preserves hooks, audit/outbox artifacts, relation actions,
// membership state and the durable file-deletion queue. The transaction takes
// one stable, ordered snapshot of all root IDs before deleting any of them;
// consequently its memory and lock duration scale with collection cardinality.
// Callers that require bounded bulk work must issue bounded ordinary deletes.
func (s *Service) Truncate(ctx context.Context, request TruncateRequest) (result TruncateResult, err error) {
	if err := s.validateRequest(request.Principal, request.RequestID); err != nil {
		return result, err
	}
	if request.Principal.Kind != PrincipalAdministrator {
		return result, recordaccess.ErrDenied
	}
	ctx, hooks := s.hookContext(ctx)
	if hooks != nil {
		hooks.deferAfter++
		defer func() { hooks.deferAfter--; hooks.after(ctx, &err) }()
	}
	result.Name = request.Collection
	err = database.Retry(ctx, func() error {
		if hooks != nil {
			hooks.events = nil
		}
		result.RecordsDeleted, result.FilesDeleted, result.RelationsDeleted = 0, 0, 0
		return s.pool.InTx(ctx, pgx.TxOptions{IsoLevel: pgx.ReadCommitted}, func(tx pgx.Tx) error {
			catalog, err := s.schemas.EnsureCurrent(ctx, tx)
			if err != nil {
				return err
			}
			collection, ok := catalog.Collections[request.Collection]
			if !ok {
				return recordaccess.ErrUnsupported
			}
			if collection.System || collection.IsView() {
				return recordaccess.ErrUnsupported
			}
			if err = lockCollectionMutation(ctx, tx, collection.ID); err != nil {
				return err
			}
			ids, err := truncateRecordIDs(ctx, tx, collection)
			if err != nil {
				return err
			}
			result.RecordsDeleted = int64(len(ids))
			if len(ids) == 0 {
				result.Truncated = true
				return nil
			}
			if err = tx.QueryRow(ctx, `select count(*) from pgbase_meta.record_files where collection_id=$1 and record_id=any($2::uuid[])`, collection.ID, ids).Scan(&result.FilesDeleted); err != nil {
				return err
			}
			result.RelationsDeleted, err = countTruncateRelations(ctx, tx, catalog, collection, ids)
			if err != nil {
				return err
			}
			txctx := WithTransaction(ctx, tx)
			for i, id := range ids {
				var exists bool
				if err = tx.QueryRow(ctx, "select exists(select 1 from "+quotedRecordCollection(collection)+" where id=$1)", id).Scan(&exists); err != nil {
					return err
				}
				if !exists {
					continue
				}
				childRequestID := fmt.Sprintf("%s-%d", request.RequestID, i)
				if len(childRequestID) > 128 {
					childRequestID = fmt.Sprintf("truncate-%s-%d", collection.ID.String(), i)
				}
				_, err = s.Delete(txctx, DeleteRequest{Principal: request.Principal, RequestID: childRequestID, Collection: collection.Name, ID: id, IfMatch: EntityTagSet{Present: true, Any: true}})
				if err != nil {
					return err
				}
			}
			result.Truncated = true
			return nil
		})
	}, nil)
	return result, membershipBoundaryError(err)
}

func truncateRecordIDs(ctx context.Context, tx pgx.Tx, collection schema.Collection) ([]uuid.UUID, error) {
	rows, err := tx.Query(ctx, "select id from "+quotedRecordCollection(collection)+" order by id")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	ids := []uuid.UUID{}
	for rows.Next() {
		var id uuid.UUID
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

func countTruncateRelations(ctx context.Context, tx pgx.Tx, catalog schema.Catalog, root schema.Collection, ids []uuid.UUID) (int64, error) {
	var total int64
	for _, source := range catalog.Collections {
		for _, field := range source.Fields {
			if field.Relation == nil {
				continue
			}
			var count int64
			if field.Relation.Cardinality == schema.RelationMultiple {
				where := "target_id=any($1::uuid[])"
				if source.ID == root.ID {
					where = "source_id=any($1::uuid[])"
				} else if field.Relation.TargetCollectionID != root.ID {
					continue
				}
				err := tx.QueryRow(ctx, "select count(*) from "+pgx.Identifier{"pgbase_relation", field.Relation.Storage.Name}.Sanitize()+" where "+where, ids).Scan(&count)
				if err != nil {
					return 0, err
				}
			} else {
				where := pgx.Identifier{field.ColumnName}.Sanitize() + " is not null and id=any($1::uuid[])"
				if source.ID != root.ID {
					if field.Relation.TargetCollectionID != root.ID {
						continue
					}
					where = pgx.Identifier{field.ColumnName}.Sanitize() + "=any($1::uuid[])"
				}
				err := tx.QueryRow(ctx, "select count(*) from "+quotedRecordCollection(source)+" where "+where, ids).Scan(&count)
				if err != nil {
					return 0, err
				}
			}
			total += count
		}
	}
	return total, nil
}
