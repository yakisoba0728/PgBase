package membership

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"sort"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/webisnoteasy/PgBase/migrations/system"
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
	if w.version != WriterA0 {
		return w.readCurrentFence(ctx, tx)
	}
	return w.readA0Fence(ctx, tx)
}

func (w Writer) readA0Fence(ctx context.Context, tx pgx.Tx) (WriteFence, error) {
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

const currentWriterValidationSelect = `with locked_capability as materialized (
	select singleton, ` + capabilityColumns + `
	from pgbase_meta.record_membership_capability
	where singleton
	for share
), known_migrations(version, checksum_hex, ordinal) as (
	select known.version, known.checksum_hex, known.ordinal
	from unnest($3::bigint[], $4::text[]) with ordinality
		as known(version, checksum_hex, ordinal)
), applied_migrations as materialized (
	select version, checksum, row_number() over (order by version) as ordinal
	from pgbase_meta.system_migrations
	order by version
	limit $5
)
select ` + capabilityColumns + `,
	to_regclass('pgbase_meta.record_membership_capability') is not null,
	to_regclass('pgbase_meta.collection_creation_counters') is not null,
	to_regclass('pgbase_meta.record_creation_ordinals') is not null,
	to_regclass('pgbase_meta.record_membership_backfills') is not null,
	to_regclass('pgbase_meta.runtime_writer_leases') is not null,
	(select count(*) from applied_migrations),
	coalesce((
		select bool_and(coalesce(
			applied.version = known.version
			and encode(applied.checksum, 'hex') = known.checksum_hex,
			false))
		from applied_migrations as applied
		left join known_migrations as known using (ordinal)
	), false),
	exists (
		select 1
		from pgbase_meta.runtime_writer_leases as lease
		where lease.instance_id = $1
		  and lease.writer_version = $2
		  and lease.lease_expires_at > clock_timestamp()
		  and locked_capability.singleton
		for key share
	)
from locked_capability`

func (w Writer) readCurrentFence(ctx context.Context, tx pgx.Tx) (WriteFence, error) {
	versions, checksums, err := currentWriterMigrationHistory()
	if err != nil {
		return WriteFence{}, err
	}
	var capability, counters, ordinals, backfills, leases bool
	var historyCount int64
	var historyMatches, leaseCurrent bool
	status, err := scanStatusWithTrailing(
		tx.QueryRow(
			ctx,
			currentWriterValidationSelect,
			w.instanceID,
			w.version,
			versions,
			checksums,
			len(versions)+1,
		),
		&capability,
		&counters,
		&ordinals,
		&backfills,
		&leases,
		&historyCount,
		&historyMatches,
		&leaseCurrent,
	)
	if err != nil {
		return WriteFence{}, currentWriterValidationError(err)
	}
	present := 0
	for _, exists := range []bool{capability, counters, ordinals, backfills, leases} {
		if exists {
			present++
		}
	}
	if present != 5 || !historyMatches || historyCount < 7 || historyCount > int64(len(versions)) {
		return WriteFence{}, ErrInvalidState
	}
	if w.version < status.MinimumWriterVersion {
		return WriteFence{}, ErrWriterTooOld
	}
	if status.State == StateCutover {
		return WriteFence{}, ErrUnavailable
	}
	if !leaseCurrent {
		return WriteFence{}, ErrWriterTooOld
	}
	return WriteFence{state: status.State, generation: status.Generation, tx: tx}, nil
}

func currentWriterMigrationHistory() ([]int64, []string, error) {
	migrations := system.Migrations()
	if len(migrations) < 7 || migrations[5].Version != 6 || migrations[6].Version != 7 {
		return nil, nil, ErrInvalidState
	}
	versions := make([]int64, len(migrations))
	checksums := make([]string, len(migrations))
	for index, migration := range migrations {
		versions[index] = migration.Version
		checksums[index] = fmt.Sprintf("%x", migration.SHA256)
	}
	return versions, checksums, nil
}

func currentWriterValidationError(err error) error {
	if currentWriterStateInvalid(err) {
		return fmt.Errorf("validate current record writer: %w", ErrInvalidState)
	}
	return fmt.Errorf("validate current record writer: %w", err)
}

func currentWriterStateInvalid(err error) bool {
	var postgresError *pgconn.PgError
	return errors.Is(err, pgx.ErrNoRows) ||
		(errors.As(err, &postgresError) && postgresError.Code == "42P01")
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
	if w.version == WriterA0 {
		var tableName string
		if err := tx.QueryRow(ctx, `select table_name from pgbase_meta.collections
			where id = $1`, collectionID).Scan(&tableName); err != nil {
			return CreateFence{}, fmt.Errorf("resolve membership collection table: %w", err)
		}
		if tableName == "" {
			return CreateFence{}, ErrInvalidState
		}
		write, err := w.readFence(ctx, tx)
		if err != nil {
			return CreateFence{}, err
		}
		return CreateFence{WriteFence: write, collectionID: collectionID, tableName: tableName}, nil
	}
	var tableName string
	var counterGeneration, lastOrdinal int64
	if err := tx.QueryRow(ctx, `select collection.table_name,
		creation_counter.generation, creation_counter.last_ordinal
		from pgbase_meta.collections as collection
		join pgbase_meta.collection_creation_counters as creation_counter
		  on creation_counter.collection_id = collection.id
		where collection.id = $1
		for no key update of creation_counter`, collectionID).Scan(
		&tableName,
		&counterGeneration,
		&lastOrdinal,
	); err != nil {
		if currentWriterStateInvalid(err) {
			return CreateFence{}, fmt.Errorf("lock record creation fence: %w", ErrInvalidState)
		}
		return CreateFence{}, fmt.Errorf("lock record creation fence: %w", err)
	}
	if tableName == "" {
		return CreateFence{}, ErrInvalidState
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
