package doctor

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/webisnoteasy/PgBase/internal/database"
	"github.com/webisnoteasy/PgBase/internal/migrate"
)

const recordMembershipMigrationVersion int64 = 7

// checkRecordMembership deliberately runs after the existing catalog checks so
// adding these findings does not reorder any established doctor diagnostics.
// It takes the transaction itself rather than a database.DBTX because the
// sidecar scans need savepoints; see sidecarScan.probe.
func checkRecordMembership(ctx context.Context, db pgx.Tx, status migrate.Status, report *Report) error {
	if status.Dirty || !status.Compatible {
		return nil
	}
	present, err := recordMembershipInstallationObjectCount(ctx, db)
	if err != nil {
		return err
	}
	if status.Current < recordMembershipMigrationVersion && present == 0 {
		return nil
	}
	if status.Current < recordMembershipMigrationVersion || present != 5 {
		appendInvalidMembershipInstallation(report)
		return nil
	}
	catalogValid, err := recordMembershipCatalogValid(ctx, db)
	if err != nil {
		return err
	}
	if !catalogValid {
		appendInvalidMembershipInstallation(report)
		return nil
	}

	state, coverageValid, err := readRecordMembershipStateAndCoverage(ctx, db)
	if err != nil {
		return err
	}
	if !coverageValid {
		appendInvalidMembershipInstallation(report)
		return nil
	}
	switch state {
	case "off", "cutover":
		return nil
	case "dual_write", "ready":
	default:
		appendInvalidMembershipInstallation(report)
		return nil
	}

	collections, err := readMembershipCollections(ctx, db)
	if err != nil {
		return err
	}
	// Everything above this point costs a fixed number of catalog queries. The
	// sidecar scans below do not: proving that no sidecar is orphaned and no
	// base row is unpaired requires visiting every row of both sides, so this
	// loop is O(total records), and each collection costs a savepoint around
	// one probe in dual_write or two in ready. `exists` stops at the first
	// offender, which only helps a database that is already broken; a healthy
	// one pays the full scan. These are also the only AccessShare locks doctor
	// takes on app tables, so a long run holds them against concurrent DDL.
	// The cost is accepted because a bounded probe cannot prove absence, and it
	// is bounded in the only way that matters to a caller: at most two findings
	// regardless of how many rows or collections are wrong.
	scan := sidecarScan{}
	for _, collection := range collections {
		if !collection.baseIDValid {
			continue
		}
		if err := scan.probe(ctx, db, collection, state); err != nil {
			return err
		}
		if scan.orphaned && (state != "ready" || scan.missing) {
			break
		}
	}
	// Each kind is appended exactly once here, after the loop, so the report
	// stays bounded no matter how many collections are wrong.
	if scan.orphaned {
		report.Findings = append(report.Findings, finding(
			"record_membership_sidecar_orphaned",
			"A record membership sidecar is missing its base record",
		))
	}
	if scan.missing {
		report.Findings = append(report.Findings, finding(
			"record_membership_sidecar_missing",
			"A base record is missing its required membership sidecar",
		))
	}
	return nil
}

type sidecarScan struct {
	orphaned, missing bool
}

// probe runs one collection's sidecar queries inside a savepoint.
//
// These queries are the only doctor statements that name a collection table as
// a dynamic identifier, and PostgreSQL resolves an identifier against the live
// catalog even under this transaction's RepeatableRead snapshot: a SQL read of
// pg_class or information_schema still reports a table that a concurrent
// session has already dropped, so readMembershipCollections can hand out a
// collection whose table no longer exists and the probe then raises 42P01.
//
// Doctor cannot close that window by pinning the schema the way the membership
// service does. Its transaction is READ ONLY and PostgreSQL rejects
// `SELECT ... FOR SHARE` there, so a pin would mean giving up read-only access
// mode and then holding a schema_state row lock for the whole O(total records)
// scan, blocking every concurrent schema apply on a diagnostic that is supposed
// to observe rather than interfere.
//
// Skipping is safe because a table that is missing for any reason other than a
// DROP committed after this snapshot is already reported: it is absent from the
// snapshot too, which clears baseIDValid and makes checkCollections emit
// collection_table_missing. Any other undefined relation is drift, so the
// savepoint is rolled back and the error returned.
func (scan *sidecarScan) probe(ctx context.Context, tx pgx.Tx, collection membershipCollection, state string) error {
	table := pgx.Identifier{"app", collection.table}.Sanitize()
	savepoint, err := tx.Begin(ctx)
	if err != nil {
		return fmt.Errorf("open record membership scan savepoint: %w", err)
	}
	defer func() { _ = savepoint.Rollback(ctx) }()

	if !scan.orphaned {
		if err := savepoint.QueryRow(ctx, `select exists (
			select 1
			from pgbase_meta.record_creation_ordinals as sidecar
			where sidecar.collection_id = $1
			  and not exists (select 1 from `+table+` as base where base.id = sidecar.record_id)
		)`, collection.id).Scan(&scan.orphaned); err != nil {
			return resumeAfterConcurrentDrop(ctx, tx, savepoint, table,
				fmt.Errorf("check orphaned record membership sidecars: %w", err))
		}
	}
	if state == "ready" && !scan.missing {
		if err := savepoint.QueryRow(ctx, `select exists (
			select 1
			from `+table+` as base
			where not exists (
				select 1 from pgbase_meta.record_creation_ordinals as sidecar
				where sidecar.collection_id = $1 and sidecar.record_id = base.id
			)
		)`, collection.id).Scan(&scan.missing); err != nil {
			return resumeAfterConcurrentDrop(ctx, tx, savepoint, table,
				fmt.Errorf("check missing record membership sidecars: %w", err))
		}
	}
	if err := savepoint.Commit(ctx); err != nil {
		return fmt.Errorf("release record membership scan savepoint: %w", err)
	}
	return nil
}

// resumeAfterConcurrentDrop returns nil only when scanErr is an undefined-table
// error raised because table is gone from the live catalog. The savepoint is
// rolled back first: a failed statement aborts the surrounding transaction
// until it is unwound, so without this the remaining collections would all fail
// with 25P02 and the caller would lose the findings already gathered.
func resumeAfterConcurrentDrop(ctx context.Context, tx pgx.Tx, savepoint pgx.Tx, table string, scanErr error) error {
	var postgresError *pgconn.PgError
	if !errors.As(scanErr, &postgresError) || postgresError.Code != "42P01" {
		return scanErr
	}
	if err := savepoint.Rollback(ctx); err != nil {
		return scanErr
	}
	var live bool
	if err := tx.QueryRow(ctx, `select to_regclass($1) is not null`, table).Scan(&live); err != nil {
		return scanErr
	}
	if live {
		return scanErr
	}
	return nil
}

func readRecordMembershipStateAndCoverage(ctx context.Context, db database.DBTX) (string, bool, error) {
	var state string
	var valid bool
	err := db.QueryRow(ctx, `with capability as materialized (
		select state, generation
		from pgbase_meta.record_membership_capability
		where singleton
	), invalid_collection as materialized (
		select collection.id
		from pgbase_meta.collections as collection
		left join pgbase_meta.collection_creation_counters as counter
		  on counter.collection_id = collection.id
		left join pgbase_meta.record_membership_backfills as backfill
		  on backfill.collection_id = collection.id
		cross join capability
		where counter.collection_id is null or backfill.collection_id is null or
			(case capability.state
				when 'off' then
					counter.generation = 0 and counter.last_ordinal = 0 and
					backfill.generation = 0 and backfill.state = 'off'
				when 'cutover' then
					counter.generation = 0 and counter.last_ordinal = 0 and
					backfill.generation = 0 and backfill.state = 'off'
				when 'dual_write' then
					counter.generation = capability.generation and
					backfill.generation = capability.generation and
					backfill.state in ('backfilling', 'ready') and
					backfill.last_assigned_ordinal <= counter.last_ordinal
				when 'ready' then
					counter.generation = capability.generation and
					backfill.generation = capability.generation and
					backfill.state = 'ready' and
					backfill.last_assigned_ordinal <= counter.last_ordinal
				else false
			end) is not true
	)
	select coalesce((select state from capability limit 1), ''),
		(select count(*) = 1 from capability)
		and not exists (select 1 from invalid_collection)
		and (select count(*) from pgbase_meta.collection_creation_counters) =
			(select count(*) from pgbase_meta.collections)
		and (select count(*) from pgbase_meta.record_membership_backfills) =
			(select count(*) from pgbase_meta.collections)
		and not exists (
			select 1
			from pgbase_meta.record_creation_ordinals as sidecar
			join pgbase_meta.collection_creation_counters as counter
			  on counter.collection_id = sidecar.collection_id
			where sidecar.ordinal > counter.last_ordinal
		)
		and (
			(select state from capability limit 1) not in ('off', 'cutover')
			or not exists (select 1 from pgbase_meta.record_creation_ordinals)
		)`).Scan(&state, &valid)
	if err != nil {
		return "", false, fmt.Errorf("validate record membership coverage: %w", err)
	}
	return state, valid, nil
}

func recordMembershipInstallationObjectCount(ctx context.Context, db database.DBTX) (int, error) {
	var capability, counters, ordinals, backfills, leases bool
	if err := db.QueryRow(ctx, `select
		to_regclass('pgbase_meta.record_membership_capability') is not null,
		to_regclass('pgbase_meta.collection_creation_counters') is not null,
		to_regclass('pgbase_meta.record_creation_ordinals') is not null,
		to_regclass('pgbase_meta.record_membership_backfills') is not null,
		to_regclass('pgbase_meta.runtime_writer_leases') is not null`).Scan(
		&capability, &counters, &ordinals, &backfills, &leases,
	); err != nil {
		return 0, fmt.Errorf("inspect record membership installation: %w", err)
	}
	present := 0
	for _, exists := range []bool{capability, counters, ordinals, backfills, leases} {
		if exists {
			present++
		}
	}
	return present, nil
}

type membershipCollection struct {
	id          uuid.UUID
	table       string
	baseIDValid bool
}

func readMembershipCollections(ctx context.Context, db database.DBTX) ([]membershipCollection, error) {
	rows, err := db.Query(ctx, `select collection.id, collection.table_name,
		exists (
			select 1
			from pg_class as table_record
			join pg_namespace as schema_record on schema_record.oid = table_record.relnamespace
			join pg_attribute as id_column
			  on id_column.attrelid = table_record.oid
			 and id_column.attname = 'id' and id_column.attnum > 0 and not id_column.attisdropped
			join pg_constraint as primary_key
			  on primary_key.conrelid = table_record.oid and primary_key.contype = 'p'
			 and primary_key.conkey = array[id_column.attnum]::smallint[]
			where schema_record.nspname = 'app' and table_record.relname = collection.table_name
			  and table_record.relkind in ('r', 'p')
			  and id_column.atttypid = 'uuid'::regtype and id_column.attnotnull
			  and primary_key.convalidated
		) as base_id_valid
		from pgbase_meta.collections as collection
		order by collection.name`)
	if err != nil {
		return nil, fmt.Errorf("read collections for record membership: %w", err)
	}
	defer rows.Close()
	collections := make([]membershipCollection, 0)
	for rows.Next() {
		var collection membershipCollection
		if err := rows.Scan(&collection.id, &collection.table, &collection.baseIDValid); err != nil {
			return nil, fmt.Errorf("scan collection for record membership: %w", err)
		}
		collections = append(collections, collection)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate collections for record membership: %w", err)
	}
	return collections, nil
}

// Every caller returns immediately after appending, so this cannot fire twice
// in one run and needs no deduplication of its own.
func appendInvalidMembershipInstallation(report *Report) {
	report.Findings = append(report.Findings, finding(
		"record_membership_installation_invalid",
		"Record membership schema installation is incomplete",
	))
}
