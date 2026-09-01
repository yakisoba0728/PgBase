package membership

import (
	"bytes"
	"context"
	"fmt"
	"sort"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

type Writer struct {
	instanceID uuid.UUID
	version    WriterVersion
}

type WriteFence struct {
	state      State
	generation int64
	tx         pgx.Tx
}

type CreateFence struct {
	WriteFence
	collectionID  uuid.UUID
	tableName     string
	counterLocked bool
	reserved      int64
}

type Record struct {
	CollectionID uuid.UUID
	RecordID     uuid.UUID
}

func (w Writer) Begin(ctx context.Context, tx pgx.Tx) (WriteFence, error) {
	if tx == nil || w.instanceID == uuid.Nil || w.version < WriterA0 || w.version > WriterC {
		return WriteFence{}, ErrInvalidState
	}
	if err := pinSchemaShared(ctx, tx); err != nil {
		return WriteFence{}, err
	}
	return w.readFence(ctx, tx)
}

func (w Writer) readFence(ctx context.Context, tx pgx.Tx) (WriteFence, error) {
	installation, err := inspectInstallation(ctx, tx)
	if err != nil {
		return WriteFence{}, err
	}
	if installation == installationPreMigration {
		if w.version == WriterA0 {
			return WriteFence{state: StateOff, tx: tx}, nil
		}
		return WriteFence{}, ErrUnavailable
	}
	if installation != installationComplete {
		return WriteFence{}, ErrInvalidState
	}
	status, err := scanStatus(tx.QueryRow(ctx, capabilitySelect+` for share`))
	if err != nil {
		return WriteFence{}, fmt.Errorf("read record writer capability: %w", err)
	}
	if w.version < status.MinimumWriterVersion {
		return WriteFence{}, ErrWriterTooOld
	}
	if status.State == StateCutover {
		return WriteFence{}, ErrUnavailable
	}
	var current bool
	if err := tx.QueryRow(ctx, `select exists (
		select 1 from pgbase_meta.runtime_writer_leases
		where instance_id = $1 and writer_version = $2
		  and lease_expires_at > clock_timestamp())`, w.instanceID, w.version).Scan(&current); err != nil {
		return WriteFence{}, fmt.Errorf("verify record writer lease: %w", err)
	}
	if !current {
		return WriteFence{}, ErrWriterTooOld
	}
	return WriteFence{state: status.State, generation: status.Generation, tx: tx}, nil
}

func (w Writer) LockCreate(
	ctx context.Context,
	tx pgx.Tx,
	collectionID uuid.UUID,
) (CreateFence, error) {
	if tx == nil || collectionID == uuid.Nil || w.instanceID == uuid.Nil ||
		w.version < WriterA0 || w.version > WriterC {
		return CreateFence{}, ErrInvalidState
	}
	if err := pinSchemaShared(ctx, tx); err != nil {
		return CreateFence{}, err
	}
	var tableName string
	if err := tx.QueryRow(ctx, `select table_name from pgbase_meta.collections
		where id = $1`, collectionID).Scan(&tableName); err != nil {
		return CreateFence{}, fmt.Errorf("resolve membership collection table: %w", err)
	}
	if tableName == "" {
		return CreateFence{}, ErrInvalidState
	}
	if w.version == WriterA0 {
		write, err := w.readFence(ctx, tx)
		if err != nil {
			return CreateFence{}, err
		}
		return CreateFence{WriteFence: write, collectionID: collectionID, tableName: tableName}, nil
	}
	var counterGeneration, lastOrdinal int64
	if err := tx.QueryRow(ctx, `select generation, last_ordinal
		from pgbase_meta.collection_creation_counters
		where collection_id = $1 for no key update`, collectionID).Scan(&counterGeneration, &lastOrdinal); err != nil {
		return CreateFence{}, fmt.Errorf("lock record creation fence: %w", err)
	}
	write, err := w.readFence(ctx, tx)
	if err != nil {
		return CreateFence{}, err
	}
	if counterGeneration != write.generation || lastOrdinal < 0 {
		return CreateFence{}, ErrMembershipDrift
	}
	return CreateFence{
		WriteFence: write, collectionID: collectionID, tableName: tableName, counterLocked: true,
	}, nil
}

func (f *CreateFence) Reserve(ctx context.Context) error {
	if f == nil || f.tx == nil || f.collectionID == uuid.Nil || f.reserved != 0 {
		return ErrInvalidState
	}
	if f.state == StateOff {
		return nil
	}
	if !f.counterLocked || f.generation < 1 || (f.state != StateDualWrite && f.state != StateReady) {
		return ErrInvalidState
	}
	if err := f.tx.QueryRow(ctx, `update pgbase_meta.collection_creation_counters
		set last_ordinal = last_ordinal + 1, updated_at = clock_timestamp()
		where collection_id = $1 and generation = $2 returning last_ordinal`,
		f.collectionID, f.generation).Scan(&f.reserved); err != nil {
		return fmt.Errorf("reserve record creation ordinal: %w", err)
	}
	if f.reserved < 1 {
		return ErrMembershipDrift
	}
	return nil
}

func (f CreateFence) RecordCreated(ctx context.Context, recordID uuid.UUID) error {
	if f.tx == nil || f.collectionID == uuid.Nil || recordID == uuid.Nil {
		return ErrInvalidState
	}
	if f.state == StateOff {
		if f.reserved != 0 {
			return ErrInvalidState
		}
		return nil
	}
	if f.reserved < 1 {
		return ErrInvalidState
	}
	table := pgx.Identifier{"app", f.tableName}.Sanitize()
	tag, err := f.tx.Exec(ctx, `insert into pgbase_meta.record_creation_ordinals
		(collection_id, record_id, ordinal)
		select $1, root.id, $3 from `+table+` as root where root.id = $2`,
		f.collectionID, recordID, f.reserved)
	return rowsAffectedOne(tag, err, "insert record creation ordinal")
}

func (f WriteFence) Delete(ctx context.Context, records []Record) error {
	if f.tx == nil || f.generation < 0 {
		return ErrInvalidState
	}
	if f.state == StateOff {
		return nil
	}
	if f.state != StateDualWrite && f.state != StateReady {
		return ErrInvalidState
	}
	collectionIDs := make([]uuid.UUID, len(records))
	recordIDs := make([]uuid.UUID, len(records))
	seen := make(map[Record]struct{}, len(records))
	byCollection := make(map[uuid.UUID][]uuid.UUID)
	for index, record := range records {
		if record.CollectionID == uuid.Nil || record.RecordID == uuid.Nil {
			return ErrInvalidState
		}
		if _, duplicate := seen[record]; duplicate {
			return ErrInvalidState
		}
		seen[record] = struct{}{}
		byCollection[record.CollectionID] = append(byCollection[record.CollectionID], record.RecordID)
		collectionIDs[index] = record.CollectionID
		recordIDs[index] = record.RecordID
	}
	if len(records) == 0 {
		return nil
	}
	orderedCollections := make([]uuid.UUID, 0, len(byCollection))
	for collectionID := range byCollection {
		orderedCollections = append(orderedCollections, collectionID)
	}
	sort.Slice(orderedCollections, func(i, j int) bool {
		return bytes.Compare(orderedCollections[i][:], orderedCollections[j][:]) < 0
	})
	tables, err := resolveCollectionTables(ctx, f.tx, orderedCollections)
	if err != nil {
		return err
	}
	for _, collectionID := range orderedCollections {
		ids := byCollection[collectionID]
		sort.Slice(ids, func(i, j int) bool { return bytes.Compare(ids[i][:], ids[j][:]) < 0 })
		table := pgx.Identifier{"app", tables[collectionID]}.Sanitize()
		var basePresent bool
		if err := f.tx.QueryRow(ctx, `select exists (
			select 1 from `+table+` where id = any($1::uuid[]))`, ids).Scan(&basePresent); err != nil {
			return fmt.Errorf("verify deleted membership base rows: %w", err)
		}
		if basePresent {
			return ErrInvalidState
		}
	}
	tag, err := f.tx.Exec(ctx, `delete from pgbase_meta.record_creation_ordinals as ordinal
		using unnest($1::uuid[], $2::uuid[]) as removed(collection_id, record_id)
		where ordinal.collection_id = removed.collection_id
		  and ordinal.record_id = removed.record_id`, collectionIDs, recordIDs)
	if err != nil {
		return fmt.Errorf("delete record creation ordinals: %w", err)
	}
	if tag.RowsAffected() > int64(len(records)) ||
		(f.state == StateReady && tag.RowsAffected() != int64(len(records))) {
		return ErrMembershipDrift
	}
	return nil
}

func resolveCollectionTables(
	ctx context.Context,
	tx pgx.Tx,
	collectionIDs []uuid.UUID,
) (map[uuid.UUID]string, error) {
	rows, err := tx.Query(ctx, `select id, table_name from pgbase_meta.collections
		where id = any($1::uuid[]) order by id`, collectionIDs)
	if err != nil {
		return nil, fmt.Errorf("resolve deleted membership collections: %w", err)
	}
	defer rows.Close()
	tables := make(map[uuid.UUID]string, len(collectionIDs))
	for rows.Next() {
		var collectionID uuid.UUID
		var tableName string
		if err := rows.Scan(&collectionID, &tableName); err != nil {
			return nil, fmt.Errorf("scan deleted membership collection: %w", err)
		}
		if collectionID == uuid.Nil || tableName == "" {
			return nil, ErrInvalidState
		}
		tables[collectionID] = tableName
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate deleted membership collections: %w", err)
	}
	if len(tables) != len(collectionIDs) {
		return nil, ErrInvalidState
	}
	return tables, nil
}
