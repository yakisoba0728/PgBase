package membership

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/webisnoteasy/PgBase/internal/database"
)

type backfillCollection struct {
	id                  uuid.UUID
	tableName           string
	generation          int64
	cutoverCount        int64
	cutoverCreatedAt    *time.Time
	cutoverRecordID     *uuid.UUID
	checkpointCreatedAt *time.Time
	checkpointRecordID  *uuid.UUID
	lastAssigned        int64
}

type recordKey struct {
	id        uuid.UUID
	createdAt time.Time
}

func (s *Service) Backfill(ctx context.Context) (BackfillProgress, error) {
	if err := s.ready(); err != nil {
		return BackfillProgress{}, err
	}
	var progress BackfillProgress
	err := database.Retry(ctx, func() error {
		return s.pool.InTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead}, func(tx pgx.Tx) error {
			if err := pinSchemaShared(ctx, tx); err != nil {
				return err
			}
			if err := requireCompleteInstallation(ctx, tx); err != nil {
				return err
			}
			status, err := scanStatus(tx.QueryRow(ctx, capabilitySelect+` for share`))
			if err != nil {
				return fmt.Errorf("read backfill capability: %w", err)
			}
			if err := requireState(status, StateDualWrite, 4); err != nil || status.Generation < 1 ||
				status.MinimumWriterVersion != WriterA {
				return ErrInvalidState
			}
			now, err := databaseTime(ctx, tx)
			if err != nil {
				return err
			}
			collection, found, err := nextBackfillCollection(ctx, tx, status.Generation)
			if err != nil {
				return err
			}
			if !found {
				progress = BackfillProgress{Generation: status.Generation}
				progress.Done, err = backfillDone(ctx, tx, status.Generation)
				return err
			}
			keys, err := readBackfillKeys(ctx, tx, collection, s.backfillBatch)
			if err != nil {
				return err
			}
			progress = BackfillProgress{
				Generation: status.Generation, Collection: collection.id, Scanned: len(keys),
			}
			if len(keys) == 0 {
				if err := validateBackfillCollection(ctx, tx, collection, now); err != nil {
					return err
				}
				progress.Ready = true
				progress.Done, err = backfillDone(ctx, tx, status.Generation)
				return err
			}

			live, err := lockBackfillRows(ctx, tx, collection.tableName, keys)
			if err != nil {
				return err
			}
			ids := make([]uuid.UUID, 0, len(keys))
			ordinals := make([]int64, 0, len(keys))
			for _, key := range keys {
				if _, exists := live[key.id]; !exists {
					continue
				}
				collection.lastAssigned++
				ids = append(ids, key.id)
				ordinals = append(ordinals, collection.lastAssigned)
			}
			if len(ids) > 0 {
				table := pgx.Identifier{"app", collection.tableName}.Sanitize()
				tag, err := tx.Exec(ctx, `insert into pgbase_meta.record_creation_ordinals
					(collection_id, record_id, ordinal)
					select $1, input.record_id, input.ordinal
					from unnest($2::uuid[], $3::bigint[]) as input(record_id, ordinal)
					join `+table+` as root on root.id = input.record_id
					on conflict (collection_id, record_id) do nothing`, collection.id, ids, ordinals)
				if err != nil {
					return fmt.Errorf("insert backfilled record ordinals: %w", err)
				}
				if tag.RowsAffected() != int64(len(ids)) {
					return ErrMembershipDrift
				}
			}
			last := keys[len(keys)-1]
			tag, err := tx.Exec(ctx, `update pgbase_meta.record_membership_backfills set
				checkpoint_created_at = $2, checkpoint_record_id = $3,
				last_assigned_ordinal = $4, updated_at = $5
				where collection_id = $1 and generation = $6 and state = 'backfilling'`,
				collection.id, last.createdAt, last.id, collection.lastAssigned, now, status.Generation)
			if err := rowsAffectedOne(tag, err, "advance record membership backfill"); err != nil {
				return err
			}
			progress.Inserted = len(ids)
			collection.checkpointCreatedAt = &last.createdAt
			collection.checkpointRecordID = &last.id
			if len(keys) < s.backfillBatch {
				if err := validateBackfillCollection(ctx, tx, collection, now); err != nil {
					return err
				}
				progress.Ready = true
			}
			progress.Done, err = backfillDone(ctx, tx, status.Generation)
			return err
		})
	}, nil)
	if err != nil {
		return BackfillProgress{}, err
	}
	return progress, nil
}

func nextBackfillCollection(ctx context.Context, tx pgx.Tx, generation int64) (backfillCollection, bool, error) {
	row := tx.QueryRow(ctx, `select backfill.collection_id, collections.table_name,
		backfill.generation, backfill.cutover_count,
		backfill.cutover_last_created_at, backfill.cutover_last_record_id,
		backfill.checkpoint_created_at, backfill.checkpoint_record_id,
		backfill.last_assigned_ordinal
		from pgbase_meta.record_membership_backfills as backfill
		join pgbase_meta.collections as collections on collections.id = backfill.collection_id
		where backfill.generation = $1 and backfill.state = 'backfilling'
		order by backfill.collection_id limit 1 for update of backfill skip locked`, generation)
	var collection backfillCollection
	var cutoverCreatedAt, checkpointCreatedAt pgtype.Timestamptz
	var cutoverRecordID, checkpointRecordID pgtype.UUID
	if err := row.Scan(
		&collection.id,
		&collection.tableName,
		&collection.generation,
		&collection.cutoverCount,
		&cutoverCreatedAt,
		&cutoverRecordID,
		&checkpointCreatedAt,
		&checkpointRecordID,
		&collection.lastAssigned,
	); err != nil {
		if err == pgx.ErrNoRows {
			return backfillCollection{}, false, nil
		}
		return backfillCollection{}, false, fmt.Errorf("lock next record membership backfill: %w", err)
	}
	collection.cutoverCreatedAt = nullableTime(cutoverCreatedAt)
	collection.cutoverRecordID = nullableUUID(cutoverRecordID)
	collection.checkpointCreatedAt = nullableTime(checkpointCreatedAt)
	collection.checkpointRecordID = nullableUUID(checkpointRecordID)
	if collection.id == uuid.Nil || collection.tableName == "" || collection.generation != generation ||
		collection.cutoverCount < 0 || collection.lastAssigned < 0 ||
		(collection.cutoverCreatedAt == nil) != (collection.cutoverRecordID == nil) ||
		(collection.checkpointCreatedAt == nil) != (collection.checkpointRecordID == nil) ||
		(collection.cutoverCount == 0) != (collection.cutoverCreatedAt == nil) {
		return backfillCollection{}, false, ErrInvalidState
	}
	return collection, true, nil
}

func nullableUUID(value pgtype.UUID) *uuid.UUID {
	if !value.Valid {
		return nil
	}
	id := uuid.UUID(value.Bytes)
	if id == uuid.Nil {
		return nil
	}
	return &id
}

func readBackfillKeys(ctx context.Context, tx pgx.Tx, collection backfillCollection, limit int) ([]recordKey, error) {
	if collection.cutoverCount == 0 {
		return nil, nil
	}
	table := pgx.Identifier{"app", collection.tableName}.Sanitize()
	rows, err := tx.Query(ctx, `select root.id, root.created_at from `+table+` as root
		left join pgbase_meta.record_creation_ordinals as ordinal
		  on ordinal.collection_id = $1 and ordinal.record_id = root.id
		where ordinal.record_id is null
		  and ($2::timestamptz is null or (root.created_at, root.id) > ($2, $3))
		  and (root.created_at, root.id) <= ($4, $5)
		order by root.created_at, root.id limit $6`,
		collection.id,
		collection.checkpointCreatedAt, collection.checkpointRecordID,
		collection.cutoverCreatedAt, collection.cutoverRecordID, limit)
	if err != nil {
		return nil, fmt.Errorf("read record membership backfill keys: %w", err)
	}
	defer rows.Close()
	keys := make([]recordKey, 0, limit)
	for rows.Next() {
		var key recordKey
		if err := rows.Scan(&key.id, &key.createdAt); err != nil {
			return nil, fmt.Errorf("scan record membership backfill key: %w", err)
		}
		if key.id == uuid.Nil {
			return nil, ErrMembershipDrift
		}
		key.createdAt = key.createdAt.UTC()
		keys = append(keys, key)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate record membership backfill keys: %w", err)
	}
	return keys, nil
}

func lockBackfillRows(ctx context.Context, tx pgx.Tx, tableName string, keys []recordKey) (map[uuid.UUID]struct{}, error) {
	ids := make([]uuid.UUID, len(keys))
	for index, key := range keys {
		ids[index] = key.id
	}
	table := pgx.Identifier{"app", tableName}.Sanitize()
	rows, err := tx.Query(ctx, `select id from `+table+` where id = any($1::uuid[]) order by id for update`, ids)
	if err != nil {
		return nil, fmt.Errorf("lock record membership backfill rows: %w", err)
	}
	defer rows.Close()
	live := make(map[uuid.UUID]struct{}, len(ids))
	for rows.Next() {
		var id uuid.UUID
		if err := rows.Scan(&id); err != nil {
			return nil, fmt.Errorf("scan locked membership record: %w", err)
		}
		if id == uuid.Nil {
			return nil, ErrMembershipDrift
		}
		live[id] = struct{}{}
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate locked membership records: %w", err)
	}
	return live, nil
}

func validateBackfillCollection(ctx context.Context, tx pgx.Tx, collection backfillCollection, now time.Time) error {
	var counterGeneration, lastOrdinal int64
	if err := tx.QueryRow(ctx, `select generation, last_ordinal
		from pgbase_meta.collection_creation_counters
		where collection_id = $1`, collection.id).Scan(&counterGeneration, &lastOrdinal); err != nil {
		return fmt.Errorf("read record creation counter for validation: %w", err)
	}
	if counterGeneration != collection.generation || lastOrdinal < collection.cutoverCount ||
		collection.lastAssigned > collection.cutoverCount {
		return ErrMembershipDrift
	}
	table := pgx.Identifier{"app", collection.tableName}.Sanitize()
	var baseCount, sidecarCount int64
	if err := tx.QueryRow(ctx, `select count(*) from `+table).Scan(&baseCount); err != nil {
		return fmt.Errorf("count membership base records: %w", err)
	}
	if err := tx.QueryRow(ctx, `select count(*) from pgbase_meta.record_creation_ordinals
		where collection_id = $1`, collection.id).Scan(&sidecarCount); err != nil {
		return fmt.Errorf("count record membership sidecars: %w", err)
	}
	var drift bool
	if err := tx.QueryRow(ctx, `select exists (
		select 1 from `+table+` as root
		left join pgbase_meta.record_creation_ordinals as ordinal
		  on ordinal.collection_id = $1 and ordinal.record_id = root.id
		where ordinal.record_id is null
	) or exists (
		select 1 from pgbase_meta.record_creation_ordinals as ordinal
		left join `+table+` as root on root.id = ordinal.record_id
		where ordinal.collection_id = $1 and root.id is null
	) or exists (
		select 1 from pgbase_meta.record_creation_ordinals
		where collection_id = $1 and ordinal > $2
	)`, collection.id, lastOrdinal).Scan(&drift); err != nil {
		return fmt.Errorf("validate record membership sidecars: %w", err)
	}
	if drift || baseCount != sidecarCount {
		return ErrMembershipDrift
	}
	tag, err := tx.Exec(ctx, `update pgbase_meta.record_membership_backfills set
		state = 'ready', validated_base_count = $2, validated_sidecar_count = $3,
		updated_at = $4, completed_at = $4
		where collection_id = $1 and generation = $5 and state = 'backfilling'`,
		collection.id, baseCount, sidecarCount, now, collection.generation)
	return rowsAffectedOne(tag, err, "complete record membership backfill")
}

func backfillDone(ctx context.Context, tx pgx.Tx, generation int64) (bool, error) {
	var done bool
	if err := tx.QueryRow(ctx, `select not exists (
		select 1 from pgbase_meta.record_membership_backfills
		where generation <> $1 or state <> 'ready')`, generation).Scan(&done); err != nil {
		return false, fmt.Errorf("read record membership backfill completion: %w", err)
	}
	return done, nil
}
