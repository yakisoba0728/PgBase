package membership

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/webisnoteasy/PgBase/internal/database"
)

// seededOffCapabilityPredicate is exactly the row migration 7 inserts. It is
// written as SQL rather than as a Go comparison so the unlocked probe and the
// locked decision cannot drift apart.
const seededOffCapabilityPredicate = `state = 'off' and generation = 0
	and minimum_writer_version = 0 and cursor_issue_version = 4
	and cutover_at is null and cutover_collection_count is null
	and cutover_record_count is null and ready_at is null
	and ready_collection_count is null and v5_enabled_at is null
	and v4_decode_until is null and v4_retired_at is null`

const seededOffCapabilityProbe = `select ` + seededOffCapabilityPredicate + `
	from pgbase_meta.record_membership_capability where singleton`

const seededOffCapabilityLock = seededOffCapabilityProbe + ` for update`

// bootstrapVacuitySelect proves, under the locks the transaction already
// holds, that the rollout would have nothing to do. Every clause is a count so
// one row answers the whole precondition.
const bootstrapVacuitySelect = `select
	(select count(*) from pgbase_meta.collection_creation_counters
		where generation <> 0 or last_ordinal <> 0),
	(select count(*) from pgbase_meta.record_membership_backfills),
	(select count(*) from pgbase_meta.record_membership_backfills
		where state <> 'off' or generation <> 0),
	(select count(*) from pgbase_meta.record_creation_ordinals),
	(select count(*) from pgbase_meta.collections
		where to_regclass('app.' || quote_ident(table_name)) is null),
	(select count(*) from pgbase_meta.runtime_writer_leases
		where lease_expires_at > $1 and writer_version < $2)`

// The whole rollout happens at one instant because there was nothing to do at
// any of its steps, and every v7 timeline CHECK is satisfied by equal stamps.
// v4_decode_until is that same instant: a database that never left 'off' with
// no record in it has no page that could carry a v4 cursor, so there is
// nothing to drain.
const bootstrapCapabilityUpdate = `update pgbase_meta.record_membership_capability set
	state = 'ready',
	generation = 1,
	minimum_writer_version = $2,
	cursor_issue_version = 5,
	cutover_at = $1,
	cutover_collection_count = $3,
	cutover_record_count = 0,
	ready_at = $1,
	ready_collection_count = $3,
	v5_enabled_at = $1,
	v4_decode_until = $1,
	v4_retired_at = $1,
	updated_at = $1
	where singleton`

const bootstrapCounterUpdate = `update pgbase_meta.collection_creation_counters
	set generation = 1, last_ordinal = 0, updated_at = $1`

const bootstrapBackfillUpdate = `update pgbase_meta.record_membership_backfills set
	generation = 1,
	state = 'ready',
	cutover_count = 0,
	cutover_last_created_at = null,
	cutover_last_record_id = null,
	checkpoint_created_at = null,
	checkpoint_record_id = null,
	last_assigned_ordinal = 0,
	validated_base_count = 0,
	validated_sidecar_count = 0,
	updated_at = $1,
	completed_at = $1`

// BootstrapFreshInstall moves a database that has nothing to migrate straight
// to the retired terminal state, so a new install starts where an upgraded
// fleet ends instead of failing every list closed until an operator has driven
// a rollout with no work in it.
//
// The invariant the rollout establishes is that every base record carries a
// membership sidecar. Skipping to the end is sound only where nothing could
// violate it, so the precondition is that no collection holds a record, not
// merely that no collection exists: an installation that has already been
// given its collections still has nothing to backfill. Emptiness is proven
// under this transaction's own locks. The exclusive schema pin keeps a
// concurrent collection create out, and the capability row is the one every
// record writer takes a share lock on before it inserts, so a record that
// would commit without a sidecar cannot slip between the proof and the
// promotion.
//
// It reports whether this call performed the promotion. Anything short of the
// exact vacuous shape — any rollout history, any data, any writer still on an
// older release — is not an error: it is a database the real rollout owns, and
// a process must not fail to start because of it.
func (s *Service) BootstrapFreshInstall(ctx context.Context) (bool, error) {
	if err := s.ready(); err != nil {
		return false, err
	}
	var bootstrapped bool
	err := database.Retry(ctx, func() error {
		bootstrapped = false
		// Read Committed on purpose. The vacuity proof has to observe every
		// record committed before this transaction's locks were granted, and a
		// repeatable-read snapshot would have been pinned before them.
		return s.pool.InTx(ctx, pgx.TxOptions{}, func(tx pgx.Tx) error {
			promoted, err := bootstrapFreshInstall(ctx, tx)
			if err != nil {
				return err
			}
			bootstrapped = promoted
			return nil
		})
	}, nil)
	if err != nil {
		return false, err
	}
	return bootstrapped, nil
}

func bootstrapFreshInstall(ctx context.Context, tx pgx.Tx) (bool, error) {
	if err := requireCompleteInstallation(ctx, tx); err != nil {
		return false, err
	}
	// Deliberately unlocked. Nothing in the product ever returns the
	// capability to 'off', so a database that is not seeded-off here can never
	// become bootstrappable later and needs none of the locks below. Taking
	// them first would also invert the schema_state -> counters -> capability
	// order Cutover already holds them in.
	var seeded bool
	if err := tx.QueryRow(ctx, seededOffCapabilityProbe).Scan(&seeded); err != nil {
		return false, fmt.Errorf("probe record membership capability: %w", err)
	}
	if !seeded {
		return false, nil
	}
	if err := pinSchemaExclusive(ctx, tx); err != nil {
		return false, err
	}
	collections, err := lockCutoverCollections(ctx, tx)
	if err != nil {
		// A catalog that has drifted from its counters is a database the real
		// rollout has to reconcile, not one this can promote.
		if errors.Is(err, ErrMembershipDrift) {
			return false, nil
		}
		return false, err
	}
	if err := tx.QueryRow(ctx, seededOffCapabilityLock).Scan(&seeded); err != nil {
		return false, fmt.Errorf("lock record membership capability: %w", err)
	}
	if !seeded {
		return false, nil
	}
	now, err := databaseTime(ctx, tx)
	if err != nil {
		return false, err
	}
	vacuous, err := bootstrapVacuous(ctx, tx, collections, now)
	if err != nil || !vacuous {
		return false, err
	}
	return true, writeBootstrappedRollout(ctx, tx, len(collections), now)
}

// bootstrapVacuous reports whether the locked database has nothing the rollout
// would have to do. The lease clause is the fleet condition retirement itself
// requires: the terminal state raises the minimum writer version to Release B,
// so a still-running older writer keeps the real rollout and its explicit
// operator fleet proof.
func bootstrapVacuous(
	ctx context.Context,
	tx pgx.Tx,
	collections []cutoverCollection,
	now time.Time,
) (bool, error) {
	var startedCounters, backfills, startedBackfills, ordinals, missingTables, oldWriters int64
	if err := tx.QueryRow(ctx, bootstrapVacuitySelect, now, WriterB).Scan(
		&startedCounters, &backfills, &startedBackfills, &ordinals, &missingTables, &oldWriters,
	); err != nil {
		return false, fmt.Errorf("prove record membership bootstrap is vacuous: %w", err)
	}
	if startedCounters != 0 || backfills != int64(len(collections)) || startedBackfills != 0 ||
		ordinals != 0 || missingTables != 0 || oldWriters != 0 {
		return false, nil
	}
	for _, collection := range collections {
		var populated bool
		table := pgx.Identifier{"app", collection.tableName}.Sanitize()
		if err := tx.QueryRow(ctx, `select exists (select 1 from `+table+`)`).Scan(&populated); err != nil {
			return false, fmt.Errorf("read collection records for membership bootstrap: %w", err)
		}
		if populated {
			return false, nil
		}
	}
	return true, nil
}

// writeBootstrappedRollout stamps the receipts the whole rollout would have
// left. Every statement asserts its own affected-row count, so a catalog row
// whose counter or backfill row is missing aborts the promotion instead of
// leaving a collection behind at an unreachable generation.
func writeBootstrappedRollout(ctx context.Context, tx pgx.Tx, collections int, now time.Time) error {
	tag, err := tx.Exec(ctx, bootstrapCounterUpdate, now)
	if err := rowsAffectedExactly(tag, err, int64(collections),
		"reset record creation counters for bootstrap"); err != nil {
		return err
	}
	tag, err = tx.Exec(ctx, bootstrapBackfillUpdate, now)
	if err := rowsAffectedExactly(tag, err, int64(collections),
		"complete record membership backfills for bootstrap"); err != nil {
		return err
	}
	tag, err = tx.Exec(ctx, bootstrapCapabilityUpdate, now, WriterB, collections)
	return rowsAffectedOne(tag, err, "bootstrap record membership to the retired terminal state")
}
