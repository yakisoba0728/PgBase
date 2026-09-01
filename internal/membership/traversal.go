package membership

import (
	"context"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// ValidateTraversal admits a signed pagination traversal against the durable
// membership generation and high-water bound in the caller's transaction.
func ValidateTraversal(
	ctx context.Context,
	tx pgx.Tx,
	collectionID uuid.UUID,
	generation int64,
	highWater int64,
) error {
	if tx == nil || collectionID == uuid.Nil || generation < 1 || highWater < 0 {
		return ErrTraversalInvalid
	}
	if err := pinSchemaShared(ctx, tx); err != nil {
		return traversalInvalid(err)
	}
	if err := requireCompleteInstallation(ctx, tx); err != nil {
		return traversalInvalid(err)
	}
	status, err := scanStatus(tx.QueryRow(ctx, capabilitySelect))
	if err != nil {
		return traversalInvalid(err)
	}
	if status.State != StateReady || status.Generation < 1 || status.Generation != generation {
		return ErrTraversalInvalid
	}
	var counterGeneration, lastOrdinal int64
	if err := tx.QueryRow(ctx, `select generation, last_ordinal
		from pgbase_meta.collection_creation_counters where collection_id = $1`,
		collectionID,
	).Scan(&counterGeneration, &lastOrdinal); err != nil {
		return traversalInvalid(err)
	}
	if counterGeneration != generation || lastOrdinal < 0 || highWater > lastOrdinal {
		return ErrTraversalInvalid
	}
	return nil
}

func traversalInvalid(cause error) error {
	return fmt.Errorf("%w: %v", ErrTraversalInvalid, cause)
}
