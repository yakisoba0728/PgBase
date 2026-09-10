package doctor

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/yakisoba0728/PgBase/internal/database"
	"github.com/yakisoba0728/PgBase/internal/migrate"
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
	case "cutover":
		return nil
	case "off", "dual_write", "ready":
	default:
		appendInvalidMembershipInstallation(report)
		return nil
	}

	collections, err := readMembershipCollections(ctx, db)
	if err != nil {
		return err
	}
	// Everything above this point costs a fixed number of catalog queries, and
	// so does the scan below: every collection is folded into one statement
	// rather than one probe apiece, so doctor's statement count does not grow
	// with the catalog. The work does. Proving that no sidecar is orphaned and
	// no base row is unpaired requires visiting every row of both sides, so the
	// scan is O(total records), and it takes AccessShare on every collection
	// table for its duration, holding them against concurrent DDL. Folding the
	// collections together also gives up the old early exit: a database that is
	// already broken now pays the full scan instead of stopping at the first
	// offender. A healthy database, which is the case that matters, paid it
	// either way. The report stays bounded at two findings no matter how many
	// rows or collections are wrong.
	scannable := make([]membershipCollection, 0, len(collections))
	for _, collection := range collections {
		if collection.baseIDValid {
			scannable = append(scannable, collection)
		}
	}
	scan := sidecarScan{}
	if err := scan.probe(ctx, db, scannable, state); err != nil {
		return err
	}
	// Each kind is appended exactly once here, so the report stays bounded no
	// matter how many collections are wrong.
	if scan.orphaned {
		report.Findings = append(report.Findings, finding(
			"record_membership_sidecar_orphaned",
			"A record membership sidecar is missing its base record",
		))
	}
	if scan.missing {
		if state == "off" {
			report.Findings = append(report.Findings, finding(
				"record_membership_rollout_not_started",
				"Record membership rollout has not started for a database that contains records; follow the membership rollout runbook",
			))
		} else {
			report.Findings = append(report.Findings, finding(
				"record_membership_sidecar_missing",
				"A base record is missing its required membership sidecar",
			))
		}
	}
	return nil
}

type sidecarScan struct {
	orphaned, missing bool
}

// concurrentDropRetries bounds how many times one scan is rebuilt after a
// collection table disappears underneath it. Each retry drops the tables the
// live catalog no longer has, so the loop terminates on its own; the bound only
// stops an adversarial stream of concurrent DROPs from keeping doctor busy.
const concurrentDropRetries = 3

// probe runs every collection's sidecar checks as one statement inside a
// savepoint.
//
// These are the only doctor statements that name a collection table as a
// dynamic identifier, and PostgreSQL resolves an identifier against the live
// catalog even under this transaction's RepeatableRead snapshot: a SQL read of
// pg_class or information_schema still reports a table that a concurrent
// session has already dropped, so readMembershipCollections can hand out a
// collection whose table no longer exists and the scan then raises 42P01.
//
// Doctor cannot close that window by pinning the schema the way the membership
// service does. Its transaction is READ ONLY and PostgreSQL rejects
// `SELECT ... FOR SHARE` there, so a pin would mean giving up read-only access
// mode and then holding a schema_state row lock for the whole O(total records)
// scan, blocking every concurrent schema apply on a diagnostic that is supposed
// to observe rather than interfere.
//
// Dropping the vanished tables and rebuilding is safe because a table that is
// missing for any reason other than a DROP committed after this snapshot is
// already reported: it is absent from the snapshot too, which clears
// baseIDValid and makes the physical comparison emit collection_table_missing.
// Any other undefined relation is drift, so the savepoint is rolled back and
// the error returned.
func (scan *sidecarScan) probe(ctx context.Context, tx pgx.Tx, collections []membershipCollection, state string) error {
	for attempt := 0; attempt < concurrentDropRetries; attempt++ {
		if len(collections) == 0 {
			return nil
		}
		orphaned, missing, scanErr := runSidecarScan(ctx, tx, collections, state)
		if scanErr == nil {
			scan.orphaned = orphaned
			scan.missing = missing
			return nil
		}
		var postgresError *pgconn.PgError
		if !errors.As(scanErr, &postgresError) || postgresError.Code != "42P01" {
			return scanErr
		}
		remaining, err := liveMembershipCollections(ctx, tx, collections)
		if err != nil {
			return scanErr
		}
		if len(remaining) == len(collections) {
			return scanErr
		}
		collections = remaining
	}
	return fmt.Errorf("record membership scan could not complete: collection tables kept disappearing")
}

func runSidecarScan(ctx context.Context, tx pgx.Tx, collections []membershipCollection, state string) (bool, bool, error) {
	savepoint, err := tx.Begin(ctx)
	if err != nil {
		return false, false, fmt.Errorf("open record membership scan savepoint: %w", err)
	}
	defer func() { _ = savepoint.Rollback(ctx) }()

	query, arguments := sidecarScanStatement(collections, state)
	var orphaned, missing bool
	// The deferred rollback matters here: a failed statement aborts the
	// surrounding transaction until its savepoint is unwound, so without it the
	// recheck and retry below would fail with 25P02 instead.
	if err := savepoint.QueryRow(ctx, query, arguments...).Scan(&orphaned, &missing); err != nil {
		return false, false, fmt.Errorf("check record membership sidecars: %w", err)
	}
	if err := savepoint.Commit(ctx); err != nil {
		return false, false, fmt.Errorf("release record membership scan savepoint: %w", err)
	}
	return orphaned, missing, nil
}

func sidecarScanStatement(collections []membershipCollection, state string) (string, []any) {
	var statement strings.Builder
	arguments := make([]any, 0, len(collections))
	statement.WriteString("select coalesce(bool_or(orphaned), false), coalesce(bool_or(missing), false) from (")
	for index, collection := range collections {
		if index > 0 {
			statement.WriteString(" union all ")
		}
		arguments = append(arguments, collection.id)
		placeholder := "$" + strconv.Itoa(len(arguments))
		table := pgx.Identifier{"app", collection.table}.Sanitize()
		statement.WriteString(`select exists (
			select 1
			from pgbase_meta.record_creation_ordinals as sidecar
			where sidecar.collection_id = ` + placeholder + `
			  and not exists (select 1 from ` + table + ` as base where base.id = sidecar.record_id)
		) as orphaned, `)
		if state == "off" || state == "ready" {
			statement.WriteString(`exists (
				select 1
				from ` + table + ` as base
				where not exists (
					select 1 from pgbase_meta.record_creation_ordinals as sidecar
					where sidecar.collection_id = ` + placeholder + ` and sidecar.record_id = base.id
				)
			) as missing`)
			continue
		}
		statement.WriteString("false as missing")
	}
	statement.WriteString(") as scans")
	return statement.String(), arguments
}

// liveMembershipCollections returns the collections whose tables the live
// catalog still resolves, in one statement.
func liveMembershipCollections(ctx context.Context, tx pgx.Tx, collections []membershipCollection) ([]membershipCollection, error) {
	qualified := make([]string, len(collections))
	for index, collection := range collections {
		qualified[index] = pgx.Identifier{"app", collection.table}.Sanitize()
	}
	rows, err := tx.Query(ctx, `select entry from unnest($1::text[]) as entry where to_regclass(entry) is not null`, qualified)
	if err != nil {
		return nil, fmt.Errorf("recheck collection tables after a concurrent drop: %w", err)
	}
	defer rows.Close()
	live := make(map[string]struct{}, len(collections))
	for rows.Next() {
		var entry string
		if err := rows.Scan(&entry); err != nil {
			return nil, fmt.Errorf("scan collection table recheck: %w", err)
		}
		live[entry] = struct{}{}
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate collection table recheck: %w", err)
	}
	remaining := make([]membershipCollection, 0, len(collections))
	for index, collection := range collections {
		if _, ok := live[qualified[index]]; ok {
			remaining = append(remaining, collection)
		}
	}
	return remaining, nil
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
