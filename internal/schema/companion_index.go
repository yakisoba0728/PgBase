package schema

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"slices"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/webisnoteasy/PgBase/internal/database"
	"github.com/webisnoteasy/PgBase/internal/releasephase"
)

// schemaCompilerVersion is 2 in feature-off P0 and 3 in the final artifact.
const schemaCompilerVersion int16 = releasephase.SchemaCompilerGeneration

// companionIndexAdvisoryKey is a session lock because CONCURRENTLY cannot run in a transaction.
const companionIndexAdvisoryKey int64 = 0x7067626173650008

// The v8 constraint caps this below two minutes from the heartbeat.
const schemaWriterLeaseTTL = 90 * time.Second

// CompanionIndexState is the durable nullable-DESC companion rollout state.
// Only ready makes the companions a required physical contract.
type CompanionIndexState string

const (
	CompanionIndexPending  CompanionIndexState = "pending"
	CompanionIndexBuilding CompanionIndexState = "building"
	CompanionIndexReady    CompanionIndexState = "ready"
)

var (
	// ErrCompanionIndexUnavailable reports an uninstalled or artifact-disabled capability.
	ErrCompanionIndexUnavailable = errors.New("schema companion index capability is unavailable")
	// ErrCompanionIndexState reports a transition the rollout does not allow.
	ErrCompanionIndexState = errors.New("invalid schema companion index capability state")
	// ErrCompanionIndexFleet reports a live schema writer that predates the
	// companion contract, or an operator proof that does not match the leases.
	ErrCompanionIndexFleet = errors.New("active schema writer fleet is too old")
	// ErrCompanionIndexIncomplete reports companions that are missing or invalid.
	ErrCompanionIndexIncomplete = errors.New("schema companion indexes are incomplete")
	// ErrCompanionIndexCatalogMoved reports a catalog change during a build,
	// which has already dropped the capability to pending.
	ErrCompanionIndexCatalogMoved = errors.New("schema catalog changed during companion index maintenance")
)

// CompanionIndexStatus is the capability row plus whether it exists. Before v9
// it reports Installed false and pending so P0 works on both declared histories.
type CompanionIndexStatus struct {
	Installed  bool
	State      CompanionIndexState
	Generation int64
	// ProvenCompilerVersion is the generation the fleet was proved at when this rollout
	// was opened, and 0 when none is. State alone cannot say which binary proved it.
	ProvenCompilerVersion int16
	CatalogVersion        *int64
	BuildingAt            *time.Time
	ReadyAt               *time.Time
	ReadyIndexCount       *int64
	UpdatedAt             time.Time
}

// Ready reports whether companions are a required physical contract right now.
func (status CompanionIndexStatus) Ready() bool {
	return status.Installed && status.State == CompanionIndexReady
}

// SchemaWriterLease is one live schema writer as the database records it.
type SchemaWriterLease struct {
	InstanceID      uuid.UUID
	CompilerVersion int16
	ExpiresAt       time.Time
}

// SchemaWriterFleetProof is the operator-confirmed live writer set. Activation
// re-reads it under lock and requires an exact match.
type SchemaWriterFleetProof struct {
	Instances []uuid.UUID
}

// CompanionIndexReceipt records one completed rollout transition.
type CompanionIndexReceipt struct {
	State          CompanionIndexState
	Generation     int64
	CatalogVersion int64
	IndexCount     int64
	At             time.Time
}

// CompanionIndexDiagnostics is the physical truth behind the capability row,
// read from one snapshot so a caller cannot mix a state with a later catalog.
type CompanionIndexDiagnostics struct {
	Status CompanionIndexStatus
	// CatalogReadable is false when the catalog is inconsistent and the companion
	// contract is therefore undefined.
	CatalogReadable bool
	CatalogVersion  int64
	Expected        int
	Valid           int
	// Invalid counts companions PostgreSQL left behind by an interrupted
	// CREATE INDEX CONCURRENTLY. They are recoverable by rerunning maintenance.
	Invalid int
	// Mismatched counts companions whose stored keys are not the exact shape the
	// read path needs, including their operator classes and collations.
	Mismatched int
	Missing    int
}

// catalogShapeError reports a failure that means the stored catalog is not a
// consistent snapshot, as opposed to a database or context failure.
func catalogShapeError(err error) bool {
	return err != nil && (errors.Is(err, ErrConflict) ||
		errors.Is(err, ErrCatalogInvalid) || errors.Is(err, errCatalogIntegrity))
}

// companionIndexObserved is PostgreSQL's physical shape; attributes includes
// INCLUDE columns so they cannot hide behind matching keys.
type companionIndexObserved struct {
	valid      bool
	attributes int
	keys       []string
}

func (s *Service) instance() (uuid.UUID, error) {
	s.instanceOnce.Do(func() {
		s.instanceID, s.instanceErr = uuid.NewV7()
	})
	return s.instanceID, s.instanceErr
}

// CompanionIndexStatus reads the capability without requiring it to exist.
func (s *Service) CompanionIndexStatus(ctx context.Context) (CompanionIndexStatus, error) {
	if err := s.ready(); err != nil {
		return CompanionIndexStatus{}, err
	}
	var status CompanionIndexStatus
	err := database.Retry(ctx, func() error {
		return s.pool.InTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly}, func(tx pgx.Tx) error {
			var readErr error
			status, readErr = ReadCompanionIndexStatus(ctx, tx)
			return readErr
		})
	}, nil)
	return status, err
}

// SchemaWriterFleet lists the leases an operator confirms before activation.
// The transition re-reads them under lock, rejecting a changed fleet.
func (s *Service) SchemaWriterFleet(ctx context.Context) ([]SchemaWriterLease, error) {
	if err := s.ready(); err != nil {
		return nil, err
	}
	var leases []SchemaWriterLease
	err := database.Retry(ctx, func() error {
		leases = nil
		return s.pool.InTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly}, func(tx pgx.Tx) error {
			installed, err := companionIndexInstalled(ctx, tx)
			if err != nil || !installed {
				return err
			}
			leases, err = readSchemaWriterLeases(ctx, tx)
			return err
		})
	}, nil)
	return leases, err
}

// readSchemaWriterLeases returns live leases in PostgreSQL's uuid order.
func readSchemaWriterLeases(ctx context.Context, db database.DBTX) ([]SchemaWriterLease, error) {
	rows, err := db.Query(ctx, `select instance_id, compiler_version, lease_expires_at
		from pgbase_meta.schema_writer_leases
		where lease_expires_at > transaction_timestamp()
		order by instance_id`)
	if err != nil {
		return nil, fmt.Errorf("read schema writer leases: %w", err)
	}
	defer rows.Close()
	var leases []SchemaWriterLease
	for rows.Next() {
		var lease SchemaWriterLease
		if err := rows.Scan(&lease.InstanceID, &lease.CompilerVersion, &lease.ExpiresAt); err != nil {
			return nil, fmt.Errorf("scan schema writer leases: %w", err)
		}
		leases = append(leases, lease)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate schema writer leases: %w", err)
	}
	return leases, nil
}

func companionIndexInstalled(ctx context.Context, db database.DBTX) (bool, error) {
	var installed bool
	// Missing relations yield a null attrelid, so the v9 column is the whole test.
	if err := db.QueryRow(ctx, `select exists (select 1 from pg_attribute
		where attname = 'proven_compiler_version' and attrelid =
		to_regclass('pgbase_meta.schema_companion_index_capability'))`).Scan(&installed); err != nil {
		return false, fmt.Errorf("inspect schema companion index installation: %w", err)
	}
	return installed, nil
}

const companionCapabilitySelect = `select state, generation,
	coalesce(proven_compiler_version, 0), catalog_version, ready_index_count,
	building_at, ready_at, updated_at
	from pgbase_meta.schema_companion_index_capability where singleton`

// ReadCompanionIndexStatus reads the rollout capability alone. A caller that
// already holds a manifest pairs it with CompanionIndexDiagnosticsForManifest.
func ReadCompanionIndexStatus(ctx context.Context, db database.DBTX) (CompanionIndexStatus, error) {
	installed, err := companionIndexInstalled(ctx, db)
	if err != nil {
		return CompanionIndexStatus{}, err
	}
	if !installed {
		return CompanionIndexStatus{State: CompanionIndexPending}, nil
	}
	return scanCompanionIndexStatus(db.QueryRow(ctx, companionCapabilitySelect))
}

func scanCompanionIndexStatus(row pgx.Row) (CompanionIndexStatus, error) {
	status := CompanionIndexStatus{Installed: true}
	var state string
	if err := row.Scan(&state, &status.Generation, &status.ProvenCompilerVersion,
		&status.CatalogVersion, &status.ReadyIndexCount,
		&status.BuildingAt, &status.ReadyAt, &status.UpdatedAt); err != nil {
		return CompanionIndexStatus{}, fmt.Errorf("read schema companion index capability: %w", err)
	}
	switch state {
	case "pending":
		status.State = CompanionIndexPending
	case "building_v3":
		status.State = CompanionIndexBuilding
	case "ready_v3":
		status.State = CompanionIndexReady
	default:
		return CompanionIndexStatus{}, ErrCompanionIndexState
	}
	return status, nil
}

// refreshSchemaWriterLease joins heartbeat or DDL to its generation claim.
// Lease-first locking avoids a same-instance heartbeat/Apply upgrade deadlock.
func (s *Service) refreshSchemaWriterLease(ctx context.Context, tx pgx.Tx) error {
	version := s.compilerVersion
	var shapeValid, generationGate bool
	if err := tx.QueryRow(ctx, `with expected as (select exists (
		select from pgbase_meta.system_migrations where version = 9) as v9)
		select to_regclass('pgbase_meta.schema_writer_leases') is not null
			and to_regclass('pgbase_meta.schema_companion_index_capability') is not null
			and (exists (select from pg_attribute a where a.attrelid = to_regclass('pgbase_meta.schema_companion_index_capability') and a.attname = 'proven_compiler_version' and a.atttypid = 'smallint'::regtype and not a.attnotnull and not a.attisdropped and not exists (select from pg_attrdef d where d.adrelid = a.attrelid and d.adnum = a.attnum))) = v9
			and (exists (select from pg_attribute a where a.attrelid = to_regclass('pgbase_meta.schema_writer_leases') and a.attname = 'writer_protocol' and a.atttypid = 'smallint'::regtype and a.attnotnull and not a.attisdropped and not exists (select from pg_attrdef d where d.adrelid = a.attrelid and d.adnum = a.attnum))) = v9
			and (exists (select from pg_constraint c where c.conrelid = to_regclass('pgbase_meta.schema_writer_leases') and c.conname = 'schema_writer_leases_protocol_check' and c.convalidated and pg_get_constraintdef(c.oid) = 'CHECK ((writer_protocol = 1))')) = v9
			and (exists (select from pg_constraint c where c.conrelid = to_regclass('pgbase_meta.schema_companion_index_capability') and c.conname = 'schema_companion_index_capability_state_check' and c.convalidated and pg_get_constraintdef(c.oid) = $$CHECK ((state = ANY (ARRAY['pending'::text, 'building_v3'::text, 'ready_v3'::text])))$$)) = v9,
			v9 from expected`).Scan(&shapeValid, &generationGate); err != nil {
		return fmt.Errorf("inspect schema writer lease installation: %w", err)
	}
	if !shapeValid {
		return ErrSynchronizationUnavailable
	}
	instanceID, err := s.instance()
	if err != nil {
		return err
	}
	protocolColumn, protocolValue, protocolUpdate := "", "", ""
	if generationGate {
		protocolColumn, protocolValue = ", writer_protocol", ", 1"
		protocolUpdate = ", writer_protocol = excluded.writer_protocol"
	}
	_, err = tx.Exec(ctx, `insert into pgbase_meta.schema_writer_leases
		(instance_id, compiler_version, started_at, heartbeat_at, lease_expires_at`+protocolColumn+`)
		values ($1, $2, transaction_timestamp(), transaction_timestamp(),
			transaction_timestamp() + $3::interval`+protocolValue+`)
		on conflict (instance_id) do update set
			compiler_version = excluded.compiler_version,
			heartbeat_at = excluded.heartbeat_at,
			lease_expires_at = excluded.lease_expires_at`+protocolUpdate,
		instanceID, version, schemaWriterLeaseTTL.String())
	if err != nil {
		return fmt.Errorf("refresh schema writer lease: %w", err)
	}
	if generationGate {
		var admitted bool
		if err := tx.QueryRow(ctx, `select state = 'pending' or proven_compiler_version = $1
			from pgbase_meta.schema_companion_index_capability where singleton for share`, version).
			Scan(&admitted); err != nil {
			return fmt.Errorf("read schema writer generation fence: %w", err)
		}
		if !admitted {
			return ErrCompanionIndexFleet
		}
	}
	return nil
}

// demoteCompanionIndexCapability atomically closes the gate for catalog changes
// that can name a companion no maintenance run has built.
func demoteCompanionIndexCapability(ctx context.Context, tx pgx.Tx) error {
	installed, err := companionIndexInstalled(ctx, tx)
	if err != nil {
		return err
	}
	if !installed {
		return nil
	}
	if _, err := tx.Exec(ctx, `update pgbase_meta.schema_companion_index_capability
		set state = 'pending', catalog_version = null, building_at = null,
			ready_at = null, ready_index_count = null, proven_compiler_version = null,
			updated_at = transaction_timestamp()
		where singleton and state <> 'pending'`); err != nil {
		return fmt.Errorf("demote schema companion index capability: %w", err)
	}
	return nil
}

// ActivateCompanionIndexes opens a rollout against the locked catalog version.
// Only the operator CLI reaches this transition.
func (s *Service) ActivateCompanionIndexes(ctx context.Context, proof SchemaWriterFleetProof) (CompanionIndexReceipt, error) {
	if err := s.ready(); err != nil {
		return CompanionIndexReceipt{}, err
	}
	if s.compilerVersion < 3 {
		return CompanionIndexReceipt{}, ErrCompanionIndexUnavailable
	}
	var receipt CompanionIndexReceipt
	err := database.Retry(ctx, func() error {
		return s.pool.InTx(ctx, pgx.TxOptions{}, func(tx pgx.Tx) error {
			status, version, err := lockCompanionCapability(ctx, tx)
			if err != nil {
				return err
			}
			if status.State == CompanionIndexBuilding || status.State == CompanionIndexReady {
				// Already open at this catalog: report the standing rollout so a
				// retried operator command is not a second generation.
				if status.CatalogVersion == nil || *status.CatalogVersion != version {
					return ErrCompanionIndexState
				}
				receipt = companionReceipt(status)
				return nil
			}
			if err := verifySchemaWriterFleet(ctx, tx, proof); err != nil {
				return err
			}
			generation := status.Generation + 1
			// verifySchemaWriterFleet just proved every live writer at this binary's
			// generation, so the row records it. Nothing else may write that column.
			row := tx.QueryRow(ctx, `update pgbase_meta.schema_companion_index_capability
				set state = 'building_v3', generation = $1, catalog_version = $2,
					proven_compiler_version = $3,
					building_at = transaction_timestamp(), ready_at = null,
					ready_index_count = null, updated_at = transaction_timestamp()
				where singleton
				returning generation, catalog_version, building_at`,
				generation, version, schemaCompilerVersion)
			var buildingAt time.Time
			if err := row.Scan(&receipt.Generation, &receipt.CatalogVersion, &buildingAt); err != nil {
				return fmt.Errorf("open schema companion index rollout: %w", err)
			}
			receipt.State = CompanionIndexBuilding
			receipt.At = buildingAt
			return nil
		})
	}, nil)
	return receipt, err
}

// RollbackCompanionIndexes forces the capability off. It never drops an index:
// the rollout rules keep new physical objects in place after a downgrade so a
// forward fix does not have to rebuild them.
func (s *Service) RollbackCompanionIndexes(ctx context.Context) (CompanionIndexReceipt, error) {
	if err := s.ready(); err != nil {
		return CompanionIndexReceipt{}, err
	}
	var receipt CompanionIndexReceipt
	err := database.Retry(ctx, func() error {
		return s.pool.InTx(ctx, pgx.TxOptions{}, func(tx pgx.Tx) error {
			if _, _, err := lockCompanionCapability(ctx, tx); err != nil {
				return err
			}
			row := tx.QueryRow(ctx, `update pgbase_meta.schema_companion_index_capability
				set state = 'pending', catalog_version = null, building_at = null,
					ready_at = null, ready_index_count = null, proven_compiler_version = null,
					updated_at = transaction_timestamp()
				where singleton
				returning generation, updated_at`)
			if err := row.Scan(&receipt.Generation, &receipt.At); err != nil {
				return fmt.Errorf("roll back schema companion index rollout: %w", err)
			}
			receipt.State = CompanionIndexPending
			return nil
		})
	}, nil)
	return receipt, err
}

func companionReceipt(status CompanionIndexStatus) CompanionIndexReceipt {
	receipt := CompanionIndexReceipt{State: status.State, Generation: status.Generation, At: status.UpdatedAt}
	if status.CatalogVersion != nil {
		receipt.CatalogVersion = *status.CatalogVersion
	}
	if status.ReadyIndexCount != nil {
		receipt.IndexCount = *status.ReadyIndexCount
	}
	return receipt
}

// lockCompanionCapability takes the same lock order as a schema apply, so a
// rollout transition and a catalog change cannot interleave.
func lockCompanionCapability(ctx context.Context, tx pgx.Tx) (CompanionIndexStatus, int64, error) {
	if _, err := tx.Exec(ctx, "select pg_advisory_xact_lock($1)", schemaAdvisoryLockKey); err != nil {
		return CompanionIndexStatus{}, 0, fmt.Errorf("acquire schema advisory lock: %w", err)
	}
	installed, err := companionIndexInstalled(ctx, tx)
	if err != nil {
		return CompanionIndexStatus{}, 0, err
	}
	if !installed {
		return CompanionIndexStatus{}, 0, ErrCompanionIndexUnavailable
	}
	version, err := NewStore(tx).PinnedSchemaVersion(ctx)
	if err != nil {
		return CompanionIndexStatus{}, 0, err
	}
	status, err := scanCompanionIndexStatus(tx.QueryRow(ctx, companionCapabilitySelect+" for update"))
	if err != nil {
		return CompanionIndexStatus{}, 0, err
	}
	return status, version, nil
}

// verifySchemaWriterFleet requires the exact, nonempty lease set at this
// compiler generation. Sorting primary-key UUIDs makes duplicates and omissions
// fail the position-by-position comparison.
func verifySchemaWriterFleet(ctx context.Context, tx pgx.Tx, proof SchemaWriterFleetProof) error {
	live, err := readSchemaWriterLeases(ctx, tx)
	if err != nil {
		return err
	}
	if len(live) == 0 || len(live) != len(proof.Instances) {
		return ErrCompanionIndexFleet
	}
	witnesses := slices.Clone(proof.Instances)
	slices.SortFunc(witnesses, func(first, second uuid.UUID) int { return bytes.Compare(first[:], second[:]) })
	for index, lease := range live {
		if witnesses[index] != lease.InstanceID || lease.CompilerVersion != schemaCompilerVersion {
			return ErrCompanionIndexFleet
		}
	}
	return nil
}

// MaintainCompanionIndexes builds every missing companion with CREATE INDEX
// CONCURRENTLY and promotes the capability to ready once PostgreSQL's own
// catalog says all of them are valid.
//
// The PostgreSQL catalog is the crash checkpoint: an interrupted build leaves an
// invalid index behind, and a rerun drops that residue and rebuilds it. Nothing
// about progress is written to pgbase_meta, so a kill at any point costs at most
// the work of one index.
func (s *Service) MaintainCompanionIndexes(ctx context.Context) (CompanionIndexReceipt, error) {
	if err := s.ready(); err != nil {
		return CompanionIndexReceipt{}, err
	}
	if s.compilerVersion < 3 {
		return CompanionIndexReceipt{}, ErrCompanionIndexUnavailable
	}
	status, err := s.CompanionIndexStatus(ctx)
	if err != nil {
		return CompanionIndexReceipt{}, err
	}
	if !status.Installed {
		return CompanionIndexReceipt{}, ErrCompanionIndexUnavailable
	}
	if status.State == CompanionIndexPending || status.CatalogVersion == nil {
		return CompanionIndexReceipt{}, ErrCompanionIndexState
	}
	if status.ProvenCompilerVersion != s.compilerVersion {
		return CompanionIndexReceipt{}, ErrCompanionIndexFleet
	}
	catalog, err := s.Catalog(ctx)
	if err != nil {
		return CompanionIndexReceipt{}, err
	}
	if catalog.Version != *status.CatalogVersion {
		return CompanionIndexReceipt{}, ErrCompanionIndexCatalogMoved
	}
	manifest, err := ExpectedPhysicalSchemaForCatalog(catalog)
	if err != nil {
		return CompanionIndexReceipt{}, err
	}
	if err := s.buildCompanionIndexes(ctx, manifest.CompanionIndexes()); err != nil {
		return CompanionIndexReceipt{}, err
	}
	return s.promoteCompanionIndexes(ctx)
}

func (s *Service) buildCompanionIndexes(ctx context.Context, companions []IndexSpec) error {
	connection, err := s.pool.Acquire(ctx)
	if err != nil {
		return fmt.Errorf("acquire schema companion index maintenance connection: %w", err)
	}
	defer connection.Release()

	var acquired bool
	if err := connection.QueryRow(ctx,
		"select pg_try_advisory_lock($1)", companionIndexAdvisoryKey).Scan(&acquired); err != nil {
		return fmt.Errorf("acquire schema companion index maintenance lock: %w", err)
	}
	if !acquired {
		return ErrConflict
	}
	// A concurrent build is bounded by the table, not by the request timeout it
	// inherited, and it must not abandon an index half-built on a slow lock. The
	// pool applies its timeout per connection at session scope, so the previous
	// value has to be captured and put back by hand: RESET would fall through to
	// the server default and quietly leave the next borrower with no timeout.
	var inheritedTimeout string
	if err := connection.QueryRow(ctx, "show statement_timeout").Scan(&inheritedTimeout); err != nil {
		return fmt.Errorf("read statement timeout for companion index maintenance: %w", err)
	}
	defer func() {
		// Best effort: a lost connection drops both settings with the session.
		restore := context.WithoutCancel(ctx)
		_, _ = connection.Exec(restore,
			"select set_config('statement_timeout', $1, false)", inheritedTimeout)
		_, _ = connection.Exec(restore,
			"select pg_advisory_unlock($1)", companionIndexAdvisoryKey)
	}()
	if _, err := connection.Exec(ctx, "set statement_timeout = 0"); err != nil {
		return fmt.Errorf("disable statement timeout for companion index maintenance: %w", err)
	}

	observed, err := loadCompanionIndexes(ctx, connection, companions)
	if err != nil {
		return err
	}
	for _, companion := range companions {
		found, present := observed[companionKey(companion)]
		if present && found.valid && companionShapeMatches(companion, found) {
			continue
		}
		if present {
			// IF NOT EXISTS would silently accept invalid residue, so the drop is
			// unconditional once the catalog says this name is not usable.
			if _, err := connection.Exec(ctx, "drop index concurrently "+
				pgx.Identifier{companion.Namespace, companion.Name}.Sanitize()); err != nil {
				return fmt.Errorf("drop invalid companion index: %w", err)
			}
		}
		if _, err := connection.Exec(ctx, createCompanionIndexStatement(companion, true)); err != nil {
			return fmt.Errorf("build companion index: %w", err)
		}
	}
	return nil
}

// createCompanionIndexStatement renders the exact key order a nullable
// descending page needs: NULLS LAST on the column, as the public contract
// promises, and PostgreSQL's DESC default on the id tiebreaker. Maintenance builds
// concurrently against a live table; a v2 create plan builds the same index inside its
// own transaction, where CONCURRENTLY is illegal and the statement before it made it.
func createCompanionIndexStatement(companion IndexSpec, concurrently bool) string {
	create := "create index "
	if concurrently {
		create += "concurrently "
	}
	statement := create + pgx.Identifier{companion.Name}.Sanitize() +
		" on " + pgx.Identifier{companion.Namespace, companion.Table}.Sanitize() + " ("
	for index, key := range companion.Keys {
		if index > 0 {
			statement += ", "
		}
		statement += companionKeyShape(pgx.Identifier{key.Column}.Sanitize(), key)
	}
	return statement + ")"
}

// companionKeyShape renders one key the way loadCompanionIndexes renders the
// stored key, so the two are compared as one value and the DDL above is built
// from what the comparison accepts. An operator class precedes the direction
// because that is the order CREATE INDEX accepts; the physical side appends
// "collate" for a key that does not sort in its column's own collation, which
// no manifest key ever asks for.
func companionKeyShape(column string, key IndexKeySpec) string {
	direction, nulls := " asc", " nulls last"
	if key.OperatorClass != "" {
		column += " " + key.OperatorClass
	}
	if key.Descending {
		direction = " desc"
	}
	if key.NullsFirst {
		nulls = " nulls first"
	}
	return column + direction + nulls
}

func companionKey(companion IndexSpec) string {
	return companion.Namespace + "\x00" + companion.Table + "\x00" + companion.Name
}

// loadCompanionIndexes reads every companion in one bulk statement so a caller
// compares one consistent physical picture instead of racing per-index probes.
func loadCompanionIndexes(ctx context.Context, db database.DBTX, companions []IndexSpec) (map[string]companionIndexObserved, error) {
	observed := make(map[string]companionIndexObserved, len(companions))
	if len(companions) == 0 {
		return observed, nil
	}
	namespaces := make([]string, 0, len(companions))
	tables := make([]string, 0, len(companions))
	names := make([]string, 0, len(companions))
	for _, companion := range companions {
		namespaces = append(namespaces, companion.Namespace)
		tables = append(tables, companion.Table)
		names = append(names, companion.Name)
	}
	rows, err := db.Query(ctx, `select schemas.nspname, tables.relname, index_tables.relname,
		indexes.indisvalid and indexes.indisready and indexes.indislive,
		indexes.indnatts::integer,
		array(select columns.attname
			|| case when operator_classes.opcdefault then '' else ' ' || operator_classes.opcname end
			|| case when (keys.option & 1) = 1 then ' desc' else ' asc' end
			|| case when (keys.option & 2) = 2 then ' nulls first' else ' nulls last' end
			|| case when keys.key_collation = columns.attcollation then '' else ' collate' end
			from unnest(indexes.indkey::smallint[], indexes.indoption::smallint[], indexes.indclass::oid[], indexes.indcollation::oid[])
				with ordinality as keys(attnum, option, operator_class, key_collation, position)
			join pg_attribute as columns on columns.attrelid = indexes.indrelid and columns.attnum = keys.attnum
			join pg_opclass as operator_classes on operator_classes.oid = keys.operator_class
			order by keys.position)
		from pg_index as indexes
		join pg_class as index_tables on index_tables.oid = indexes.indexrelid
		join pg_class as tables on tables.oid = indexes.indrelid
		join pg_namespace as schemas on schemas.oid = tables.relnamespace
		join unnest($1::text[], $2::text[], $3::text[])
			as wanted(namespace, table_name, index_name)
			on wanted.namespace = schemas.nspname
			and wanted.table_name = tables.relname
			and wanted.index_name = index_tables.relname
		where index_tables.relam = (select oid from pg_am where amname = 'btree')
			and indexes.indpred is null and indexes.indexprs is null`,
		namespaces, tables, names)
	if err != nil {
		return nil, fmt.Errorf("read companion indexes: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var namespace, table, name string
		var found companionIndexObserved
		if err := rows.Scan(&namespace, &table, &name, &found.valid, &found.attributes, &found.keys); err != nil {
			return nil, fmt.Errorf("scan companion indexes: %w", err)
		}
		observed[namespace+"\x00"+table+"\x00"+name] = found
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate companion indexes: %w", err)
	}
	return observed, nil
}

// companionShapeMatches compares the stored keys against the manifest. A
// same-named index that differs in column order, direction, null placement,
// operator class or collation cannot serve the page, so it counts as absent
// rather than as satisfying the contract.
func companionShapeMatches(companion IndexSpec, observed companionIndexObserved) bool {
	if observed.attributes != len(companion.Keys) || len(observed.keys) != len(companion.Keys) {
		return false
	}
	for index, key := range companion.Keys {
		if observed.keys[index] != companionKeyShape(key.Column, key) {
			return false
		}
	}
	return true
}

// promoteCompanionIndexes moves building to ready only after re-reading the
// catalog under the schema lock and re-validating every companion against it, so
// a collection created during the build cannot be promoted over.
func (s *Service) promoteCompanionIndexes(ctx context.Context) (CompanionIndexReceipt, error) {
	var receipt CompanionIndexReceipt
	err := database.Retry(ctx, func() error {
		return s.pool.InTx(ctx, pgx.TxOptions{}, func(tx pgx.Tx) error {
			status, version, err := lockCompanionCapability(ctx, tx)
			if err != nil {
				return err
			}
			if status.State != CompanionIndexPending && status.ProvenCompilerVersion != s.compilerVersion {
				return ErrCompanionIndexFleet
			}
			if status.State == CompanionIndexReady {
				if status.CatalogVersion == nil || *status.CatalogVersion != version {
					return ErrCompanionIndexCatalogMoved
				}
				receipt = companionReceipt(status)
				return nil
			}
			if status.State != CompanionIndexBuilding ||
				status.CatalogVersion == nil || *status.CatalogVersion != version {
				return ErrCompanionIndexCatalogMoved
			}
			diagnostics, err := companionIndexDiagnostics(ctx, tx, status, version)
			if err != nil {
				return err
			}
			if diagnostics.Valid != diagnostics.Expected {
				return ErrCompanionIndexIncomplete
			}
			row := tx.QueryRow(ctx, `update pgbase_meta.schema_companion_index_capability
				set state = 'ready_v3', ready_at = transaction_timestamp(),
					ready_index_count = $1, updated_at = transaction_timestamp()
				where singleton
				returning generation, catalog_version, ready_index_count, ready_at`,
				int64(diagnostics.Expected))
			if err := row.Scan(&receipt.Generation, &receipt.CatalogVersion,
				&receipt.IndexCount, &receipt.At); err != nil {
				return fmt.Errorf("promote schema companion index rollout: %w", err)
			}
			receipt.State = CompanionIndexReady
			return nil
		})
	}, nil)
	return receipt, err
}

// ReadCompanionIndexDiagnostics compares the capability against the physical
// shape inside one caller-provided snapshot. Doctor uses it so its
// transitional-state report and its physical report cannot describe different
// instants.
func ReadCompanionIndexDiagnostics(ctx context.Context, db database.DBTX) (CompanionIndexDiagnostics, error) {
	status, err := ReadCompanionIndexStatus(ctx, db)
	if err != nil {
		return CompanionIndexDiagnostics{}, err
	}
	if !status.Installed {
		return CompanionIndexDiagnostics{Status: status}, nil
	}
	version, err := NewStore(db).SnapshotSchemaVersion(ctx)
	if err != nil {
		return CompanionIndexDiagnostics{}, err
	}
	diagnostics, err := companionIndexDiagnostics(ctx, db, status, version)
	if catalogShapeError(err) {
		// A caller that already reports the catalog fault must not also be told
		// the companion contract is broken; there is no contract to compare to.
		return CompanionIndexDiagnostics{Status: status, CatalogVersion: version}, nil
	}
	return diagnostics, err
}

func companionIndexDiagnostics(
	ctx context.Context, db database.DBTX, status CompanionIndexStatus, version int64,
) (CompanionIndexDiagnostics, error) {
	catalog, err := NewStore(db).ReadCatalog(ctx, version)
	if err != nil {
		return CompanionIndexDiagnostics{}, err
	}
	manifest, err := ExpectedPhysicalSchemaForCatalog(catalog)
	if err != nil {
		return CompanionIndexDiagnostics{}, err
	}
	return CompanionIndexDiagnosticsForManifest(ctx, db, status, version, manifest)
}

// CompanionIndexDiagnosticsForManifest reports the rollout against a manifest
// the caller has already compiled. Doctor holds one for its whole run, and
// compiling a second one would read the catalog twice inside a snapshot where
// it cannot have changed.
func CompanionIndexDiagnosticsForManifest(
	ctx context.Context,
	db database.DBTX,
	status CompanionIndexStatus,
	version int64,
	manifest ExpectedPhysicalSchema,
) (CompanionIndexDiagnostics, error) {
	diagnostics := CompanionIndexDiagnostics{Status: status, CatalogVersion: version, CatalogReadable: true}
	companions := manifest.CompanionIndexes()
	diagnostics.Expected = len(companions)
	observed, err := loadCompanionIndexes(ctx, db, companions)
	if err != nil {
		return CompanionIndexDiagnostics{}, err
	}
	for _, companion := range companions {
		found, present := observed[companionKey(companion)]
		switch {
		case !present:
			diagnostics.Missing++
		case !found.valid:
			diagnostics.Invalid++
		case !companionShapeMatches(companion, found):
			diagnostics.Mismatched++
		default:
			diagnostics.Valid++
		}
	}
	return diagnostics, nil
}
