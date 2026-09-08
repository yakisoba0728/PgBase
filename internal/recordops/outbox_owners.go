package recordops

import (
	"context"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// Capture before revision deletion cascades to the ownership table. Creates
// install ownership later in this same transaction, so their audience comes
// from the admitted principal. Administrators do not implicitly own records.
func captureOutboxOwners(ctx context.Context, tx pgx.Tx, principal Principal, batch artifactBatch) error {
	if batch.operation == "create" {
		if principal.Kind == PrincipalUser {
			for index := range batch.rows {
				batch.rows[index].ownerID = &principal.ID
			}
		}
		return nil
	}
	ids := make([]uuid.UUID, len(batch.rows))
	positions := make(map[uuid.UUID]int, len(batch.rows))
	for index, row := range batch.rows {
		ids[index] = row.recordID
		positions[row.recordID] = index
	}
	rows, err := tx.Query(ctx, `SELECT record_id,user_id FROM pgbase_meta.record_owners
		WHERE collection_id=$1 AND record_id=ANY($2::uuid[])`, batch.collection.ID, ids)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var id, owner uuid.UUID
		if err := rows.Scan(&id, &owner); err != nil {
			return err
		}
		batch.rows[positions[id]].ownerID = &owner
	}
	return rows.Err()
}
