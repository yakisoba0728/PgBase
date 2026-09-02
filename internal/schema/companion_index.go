package schema

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/webisnoteasy/PgBase/internal/database"
)

// schemaCompilerVersion is the plan compiler generation this binary would issue.
// It is durable state in pgbase_meta.schema_writer_leases, so an activation can
// prove no live schema writer predates the companion contract instead of
// trusting an operational drain.
const schemaCompilerVersion int16 = 2

// companionIndexAdvisoryKey serializes concurrent index maintenance. It is a
// session lock rather than a transaction lock because CREATE INDEX CONCURRENTLY
// cannot run inside a transaction block.
const companionIndexAdvisoryKey int64 = 0x7067626173650008

// schemaWriterLeaseTTL is the lease window. The v8 timeline check caps expiry at
// two minutes past the heartbeat, so this leaves room for one missed refresh.
const schemaWriterLeaseTTL = 90 * time.Second

// PostgreSQL's pg_index.indoption bits. A companion key is DESC NULLS LAST on
// the sorted column and the plain DESC default on the id tiebreaker.
const (
	indexOptionDescending = 1
	indexOptionNullsFirst = 2
)

// CompanionIndexState is the durable rollout state of the nullable-DESC
// companion indexes. Only ready lets a caller treat a companion as a required
// physical contract.
type CompanionIndexState string

const (
	CompanionIndexPending  CompanionIndexState = "pending"
	CompanionIndexBuilding CompanionIndexState = "building"
	CompanionIndexReady    CompanionIndexState = "ready"
)

var (
	// ErrCompanionIndexUnavailable reports that migration 8 is not applied, which
	// is the normal state of a preparatory artifact running on clean v7.
	ErrCompanionIndexUnavailable = errors.New("schema companion index capability is not installed")
	// ErrCompanionIndexState reports a transition the rollout does not allow.
	ErrCompanionIndexState = errors.New("invalid schema companion index capability state")
	// ErrCompanionIndexFleet reports a live schema writer that predates the
	// companion contract, or an operator proof that does not match the leases.
	ErrCompanionIndexFleet = errors.New("active schema writer fleet is too old")
	// ErrCompanionIndexIncomplete reports companions that are missing or invalid.
	ErrCompanionIndexIncomplete = errors.New("schema companion indexes are incomplete")
	// ErrCompanionIndexCatalogMoved reports a catalog change during a build. The
	// change already dropped the capability to pending, so the build cannot
	// promote the state it was started for.
	ErrCompanionIndexCatalogMoved = errors.New("schema catalog changed during companion index maintenance")
)

// CompanionIndexStatus is the capability row plus whether it exists at all.
// A binary running on the pre-migration history reports Installed false and
// pending, never an error, so the P0 artifact behaves identically on both
// declared histories.
type CompanionIndexStatus struct {
	Installed       bool
	State           CompanionIndexState
	Generation      int64
	CatalogVersion  *int64
	BuildingAt      *time.Time
	ReadyAt         *time.Time
	ReadyIndexCount *int64
	UpdatedAt       time.Time
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

// SchemaWriterFleetProof is the operator-confirmed set of live schema writers
// observed before activation. The transition re-reads the durable leases under
// its own locks and requires an exact match, so a stale proof cannot pass.
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
	// CatalogReadable is false when the catalog itself is inconsistent. The
	// companion contract is undefined then, and the catalog fault is the finding
	// a caller should act on.
	CatalogReadable bool
	CatalogVersion  int64
	Expected        int
	Valid           int
	// Invalid counts companions PostgreSQL left behind by an interrupted
	// CREATE INDEX CONCURRENTLY. They are recoverable by rerunning maintenance.
	Invalid int
	// Mismatched counts companions whose stored key order is not the exact
	// DESC NULLS LAST shape the read path needs.
	Mismatched int
	Missing    int
}

// catalogShapeError reports a failure that means the stored catalog is not a
// consistent snapshot, as opposed to a database or context failure.
func catalogShapeError(err error) bool {
	return err != nil && (errors.Is(err, ErrConflict) ||
		errors.Is(err, ErrCatalogInvalid) || errors.Is(err, errCatalogIntegrity))
}

// companionIndexObserved is one companion as PostgreSQL actually stores it.
type companionIndexObserved struct {
	valid   bool
	options []int16
	columns []string
}

func (s *Service) instance() (uuid.UUID, error) {
	if err := s.ready(); err != nil {
		return uuid.Nil, err
	}
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
			status, readErr = readCompanionIndexStatus(ctx, tx)
			return readErr
		})
	}, nil)
	return status, err
}

// SchemaWriterFleet lists the unexpired leases an operator must confirm before
// activation. It is a read: the transition re-reads the same rows under its own
// locks, so a fleet that changes in between fails the activation rather than
// slipping through.
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
			rows, err := tx.Query(ctx, `select instance_id, compiler_version, lease_expires_at
				from pgbase_meta.schema_writer_leases
				where lease_expires_at > transaction_timestamp()
				order by instance_id`)
			if err != nil {
				return fmt.Errorf("read schema writer leases: %w", err)
			}
			defer rows.Close()
			for rows.Next() {
				var lease SchemaWriterLease
				if err := rows.Scan(&lease.InstanceID, &lease.CompilerVersion, &lease.ExpiresAt); err != nil {
					return fmt.Errorf("scan schema writer leases: %w", err)
				}
				leases = append(leases, lease)
			}
			return rows.Err()
		})
	}, nil)
	return leases, err
}

func companionIndexInstalled(ctx context.Context, db database.DBTX) (bool, error) {
	var installed bool
	if err := db.QueryRow(ctx, `select
		to_regclass('pgbase_meta.schema_companion_index_capability') is not null
		and to_regclass('pgbase_meta.schema_writer_leases') is not null`).Scan(&installed); err != nil {
		return false, fmt.Errorf("inspect schema companion index installation: %w", err)
	}
	return installed, nil
}

const companionCapabilitySelect = `select state, generation, catalog_version,
	building_at, ready_at, ready_index_count, updated_at
	from pgbase_meta.schema_companion_index_capability where singleton`

func readCompanionIndexStatus(ctx context.Context, db database.DBTX) (CompanionIndexStatus, error) {
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
	if err := row.Scan(&state, &status.Generation, &status.CatalogVersion,
		&status.BuildingAt, &status.ReadyAt, &status.ReadyIndexCount, &status.UpdatedAt); err != nil {
		return CompanionIndexStatus{}, fmt.Errorf("read schema companion index capability: %w", err)
	}
	switch CompanionIndexState(state) {
	case CompanionIndexPending, CompanionIndexBuilding, CompanionIndexReady:
		status.State = CompanionIndexState(state)
	default:
		return CompanionIndexStatus{}, ErrCompanionIndexState
	}
	return status, nil
}

// refreshSchemaWriterLease records this process as a live schema writer at this
// binary's compiler version. It runs inside the caller's transaction so a schema
// write and its version claim commit together: a writer that never proves its
// version also never mutates the catalog.
func (s *Service) refreshSchemaWriterLease(ctx context.Context, tx pgx.Tx) error {
	installed, err := companionIndexInstalled(ctx, tx)
	if err != nil {
		return err
	}
	if !installed {
		return nil
	}
	instanceID, err := s.instance()
	if err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `insert into pgbase_meta.schema_writer_leases
		(instance_id, compiler_version, started_at, heartbeat_at, lease_expires_at)
		values ($1, $2, transaction_timestamp(), transaction_timestamp(),
			transaction_timestamp() + $3::interval)
		on conflict (instance_id) do update set
			compiler_version = excluded.compiler_version,
			heartbeat_at = excluded.heartbeat_at,
			lease_expires_at = excluded.lease_expires_at`,
		instanceID, schemaCompilerVersion, schemaWriterLeaseTTL.String())
	if err != nil {
		return fmt.Errorf("refresh schema writer lease: %w", err)
	}
	return nil
}

// demoteCompanionIndexCapability drops the capability to pending inside the
// transaction that changes the catalog. A catalog change can introduce a field
// whose companion does not exist yet, so leaving the state ready would make
// every consumer require an index nothing has built.
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
			ready_at = null, ready_index_count = null,
			updated_at = transaction_timestamp()
		where singleton and state <> 'pending'`); err != nil {
		return fmt.Errorf("demote schema companion index capability: %w", err)
	}
	return nil
}

// ActivateCompanionIndexes opens a rollout: pending becomes building against the
// catalog version observed under the schema lock. It is the narrow transition
// D2-2 reserves for the operator CLI; nothing on the HTTP surface, no
// environment variable and no public option reaches it.
func (s *Service) ActivateCompanionIndexes(ctx context.Context, proof SchemaWriterFleetProof) (CompanionIndexReceipt, error) {
	if err := s.ready(); err != nil {
		return CompanionIndexReceipt{}, err
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
			row := tx.QueryRow(ctx, `update pgbase_meta.schema_companion_index_capability
				set state = 'building', generation = $1, catalog_version = $2,
					building_at = transaction_timestamp(), ready_at = null,
					ready_index_count = null, updated_at = transaction_timestamp()
				where singleton
				returning generation, catalog_version, building_at`,
				generation, version)
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
					ready_at = null, ready_index_count = null,
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

// verifySchemaWriterFleet requires the durable leases to be exactly the operator
// proof and every one of them to be at this compiler version or newer. An empty
// fleet is refused: activation must observe at least one writer that has proven
// it understands the companion contract.
func verifySchemaWriterFleet(ctx context.Context, tx pgx.Tx, proof SchemaWriterFleetProof) error {
	rows, err := tx.Query(ctx, `select instance_id, compiler_version
		from pgbase_meta.schema_writer_leases
		where lease_expires_at > transaction_timestamp()
		order by instance_id`)
	if err != nil {
		return fmt.Errorf("read schema writer leases: %w", err)
	}
	defer rows.Close()
	live := make(map[uuid.UUID]int16)
	for rows.Next() {
		var instanceID uuid.UUID
		var version int16
		if err := rows.Scan(&instanceID, &version); err != nil {
			return fmt.Errorf("scan schema writer leases: %w", err)
		}
		live[instanceID] = version
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("iterate schema writer leases: %w", err)
	}
	if len(live) == 0 || len(live) != len(proof.Instances) {
		return ErrCompanionIndexFleet
	}
	for _, instanceID := range proof.Instances {
		version, ok := live[instanceID]
		if !ok || version < schemaCompilerVersion {
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

	for _, companion := range companions {
		observed, present, err := observeCompanionIndex(ctx, connection, companion)
		if err != nil {
			return err
		}
		if present && observed.valid && companionShapeMatches(companion, observed) {
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
		if _, err := connection.Exec(ctx, createCompanionIndexStatement(companion)); err != nil {
			return fmt.Errorf("build companion index: %w", err)
		}
	}
	return nil
}

// createCompanionIndexStatement renders the exact key order a nullable
// descending page needs. The column keeps NULLS LAST, which the public contract
// promises, and the id tiebreaker keeps PostgreSQL's DESC default.
func createCompanionIndexStatement(companion IndexSpec) string {
	statement := "create index concurrently " + pgx.Identifier{companion.Name}.Sanitize() +
		" on " + pgx.Identifier{companion.Namespace, companion.Table}.Sanitize() + " ("
	for index, key := range companion.Keys {
		if index > 0 {
			statement += ", "
		}
		statement += pgx.Identifier{key.Column}.Sanitize()
		if key.Descending {
			statement += " desc"
		} else {
			statement += " asc"
		}
		if key.NullsFirst {
			statement += " nulls first"
		} else {
			statement += " nulls last"
		}
	}
	return statement + ")"
}

func observeCompanionIndex(ctx context.Context, db database.DBTX, companion IndexSpec) (companionIndexObserved, bool, error) {
	observed, err := loadCompanionIndexes(ctx, db, []IndexSpec{companion})
	if err != nil {
		return companionIndexObserved{}, false, err
	}
	found, ok := observed[companionKey(companion)]
	return found, ok, nil
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
		indexes.indoption::smallint[],
		array(select columns.attname
			from unnest(indexes.indkey::smallint[]) with ordinality as keys(attnum, position)
			join pg_attribute as columns
				on columns.attrelid = indexes.indrelid and columns.attnum = keys.attnum
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
		if err := rows.Scan(&namespace, &table, &name, &found.valid, &found.options, &found.columns); err != nil {
			return nil, fmt.Errorf("scan companion indexes: %w", err)
		}
		observed[namespace+"\x00"+table+"\x00"+name] = found
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate companion indexes: %w", err)
	}
	return observed, nil
}

// companionShapeMatches compares the stored key order against the manifest. A
// same-named index with a different direction or null placement cannot serve the
// page, so it counts as absent rather than as satisfying the contract.
func companionShapeMatches(companion IndexSpec, observed companionIndexObserved) bool {
	if len(observed.columns) != len(companion.Keys) || len(observed.options) != len(companion.Keys) {
		return false
	}
	for index, key := range companion.Keys {
		if observed.columns[index] != key.Column {
			return false
		}
		want := int16(0)
		if key.Descending {
			want |= indexOptionDescending
		}
		if key.NullsFirst {
			want |= indexOptionNullsFirst
		}
		if observed.options[index] != want {
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
				set state = 'ready', ready_at = transaction_timestamp(),
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
	status, err := readCompanionIndexStatus(ctx, db)
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
	diagnostics := CompanionIndexDiagnostics{Status: status, CatalogVersion: version, CatalogReadable: true}
	catalog, err := NewStore(db).ReadCatalog(ctx, version)
	if err != nil {
		return CompanionIndexDiagnostics{}, err
	}
	manifest, err := ExpectedPhysicalSchemaForCatalog(catalog)
	if err != nil {
		return CompanionIndexDiagnostics{}, err
	}
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
