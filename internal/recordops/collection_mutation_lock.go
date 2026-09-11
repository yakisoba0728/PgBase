package recordops

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

const collectionMutationLockDomain = "pgbase-record-collection-mutation-v1:"

// lockCollectionMutation serializes collection-wide operations with record
// creation. Both paths acquire it after the shared schema pin. Create takes it
// after its read-only membership fence and generated-value locks but before
// counter reservation, relation locks, or the insert; truncate takes it before
// the delete graph. The transaction retains it through commit. A stable
// collection ID keeps rename from changing the lock identity.
func lockCollectionMutation(ctx context.Context, tx pgx.Tx, collectionID uuid.UUID) error {
	if tx == nil || collectionID == uuid.Nil {
		return errors.New("collection mutation lock requires a transaction and collection")
	}
	if _, err := tx.Exec(ctx,
		`select pg_catalog.pg_advisory_xact_lock(pg_catalog.hashtextextended($1, 0))`,
		collectionMutationLockDomain+collectionID.String(),
	); err != nil {
		return fmt.Errorf("lock collection mutation: %w", err)
	}
	return nil
}
