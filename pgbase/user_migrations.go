package pgbase

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"sort"
	"sync"

	"github.com/jackc/pgx/v5"
	"github.com/yakisoba0728/PgBase/internal/database"
	"github.com/yakisoba0728/PgBase/internal/doctor"
	"github.com/yakisoba0728/PgBase/internal/schema"
	"github.com/yakisoba0728/PgBase/internal/usermigrate"
)

// UserMigration is application Go code executed transactionally in manifest order.
// Source must contain the exact migration .go file bytes, normally supplied by
// the generator's go:embed registry, so an applied file edit changes its checksum.
type UserMigration struct {
	ID                 string
	Source             []byte
	BeforeSnapshot     []byte
	AfterSnapshot      []byte
	IrreversibleReason string
	Up                 func(context.Context, pgx.Tx) error
	Down               func(context.Context, pgx.Tx) error
}

type UserMigrationEntry = usermigrate.Entry
type UserMigrationResult = usermigrate.Result
type UserMigrationHistoryEntry = usermigrate.HistoryEntry
type UserMigrationHistoryCleanup = usermigrate.HistoryCleanup
type UserMigrationHistoryState = usermigrate.HistoryState
type UserMigrationSourceStatus = usermigrate.SourceStatus

const (
	UserMigrationHistoryApplied          = usermigrate.HistoryApplied
	UserMigrationHistoryPending          = usermigrate.HistoryPending
	UserMigrationHistoryChecksumMismatch = usermigrate.HistoryChecksumMismatch
	UserMigrationHistoryOnly             = usermigrate.HistoryOnly
	UserMigrationHistorySourceGap        = usermigrate.HistorySourceGap
)

type IrreversibleMigrationError struct{ Reason string }

func (e *IrreversibleMigrationError) Error() string {
	if e == nil || e.Reason == "" {
		return "user migration Down is irreversible"
	}
	return "user migration Down is irreversible: " + e.Reason
}

var registeredUserMigrations struct {
	sync.Mutex
	items []UserMigration
}

// RegisterUserMigration adds one migration to the process manifest.
func RegisterUserMigration(migration UserMigration) {
	registeredUserMigrations.Lock()
	defer registeredUserMigrations.Unlock()
	migration.Source = append([]byte(nil), migration.Source...)
	migration.BeforeSnapshot = append([]byte(nil), migration.BeforeSnapshot...)
	migration.AfterSnapshot = append([]byte(nil), migration.AfterSnapshot...)
	registeredUserMigrations.items = append(registeredUserMigrations.items, migration)
}

// RegisteredUserMigrations returns an isolated snapshot of the process manifest.
func RegisteredUserMigrations() []UserMigration {
	registeredUserMigrations.Lock()
	defer registeredUserMigrations.Unlock()
	result := append([]UserMigration(nil), registeredUserMigrations.items...)
	for index := range result {
		result[index].Source = append([]byte(nil), result[index].Source...)
		result[index].BeforeSnapshot = append([]byte(nil), result[index].BeforeSnapshot...)
		result[index].AfterSnapshot = append([]byte(nil), result[index].AfterSnapshot...)
	}
	return result
}

type UserMigrationRunner struct{ runner *usermigrate.Runner }

// NewUserMigrationRunner binds a compiled manifest to an inactive or running App.
func NewUserMigrationRunner(app *App, migrations []UserMigration) (*UserMigrationRunner, error) {
	if app == nil || app.pool == nil {
		return nil, errors.New("pgbase app is nil")
	}
	internal := make([]usermigrate.Migration, len(migrations))
	for index, migration := range migrations {
		if len(migration.Source) == 0 {
			return nil, errors.New("user migration source is empty")
		}
		internal[index] = usermigrate.Migration{
			ID: migration.ID, Revision: sha256.Sum256(migration.Source),
			BeforeSnapshot: migration.BeforeSnapshot, AfterSnapshot: migration.AfterSnapshot,
			IrreversibleReason: migration.IrreversibleReason, Up: migration.Up, Down: migration.Down,
		}
	}
	sort.Slice(internal, func(i, j int) bool { return internal[i].ID < internal[j].ID })
	refresh := func(ctx context.Context) error {
		if app.schemaCache == nil {
			return nil
		}
		return schema.PreflightCatalog(ctx, app.pool, app.schemaCache)
	}
	validate := func(ctx context.Context, tx pgx.Tx) error {
		manifest, err := schema.ExternalMigrationManifest(ctx, tx)
		if err != nil {
			return err
		}
		return doctor.ValidateRequiredPhysicalSchema(ctx, tx, manifest)
	}
	snapshot := func(ctx context.Context, db database.DBTX) ([]byte, error) {
		tx, ok := db.(pgx.Tx)
		if !ok {
			return nil, errors.New("user migration snapshot requires a transaction")
		}
		return schema.ExportMigrationSnapshot(ctx, tx)
	}
	runner, err := usermigrate.NewWithSnapshotReader(app.pool, internal, validate, refresh, snapshot)
	if err != nil {
		return nil, err
	}
	return &UserMigrationRunner{runner: runner}, nil
}

func (r *UserMigrationRunner) History(ctx context.Context) ([]UserMigrationHistoryEntry, error) {
	if r == nil || r.runner == nil {
		return nil, errors.New("user migration runner is nil")
	}
	return r.runner.History(ctx)
}

func (r *UserMigrationRunner) HistorySync(ctx context.Context, id string) (UserMigrationEntry, error) {
	if r == nil || r.runner == nil {
		return UserMigrationEntry{}, errors.New("user migration runner is nil")
	}
	return r.runner.HistorySync(ctx, id)
}

// CleanupHistory explicitly removes applied history whose source is absent.
// It preserves schema and checksum invariants; use dryRun to preview first.
func (r *UserMigrationRunner) CleanupHistory(ctx context.Context, dryRun bool) (UserMigrationHistoryCleanup, error) {
	if r == nil || r.runner == nil {
		return UserMigrationHistoryCleanup{}, errors.New("user migration runner is nil")
	}
	return r.runner.CleanupHistory(ctx, dryRun)
}

// UserMigrationSnapshot returns the canonical source-generation snapshot for
// the app's current logical collection schema.
func (a *App) UserMigrationSnapshot(ctx context.Context) ([]byte, error) {
	if a == nil {
		return nil, errors.New("pgbase app is nil")
	}
	service, ok := a.schemaOperations.(*schema.Service)
	if !ok || service == nil {
		return nil, errors.New("schema service is unavailable")
	}
	return service.ExportMigrationSnapshot(ctx)
}

// ApplyUserMigrationSnapshot executes a generated transition in the supplied
// user migration transaction.
func ApplyUserMigrationSnapshot(ctx context.Context, tx pgx.Tx, before, after []byte) error {
	if tx == nil {
		return fmt.Errorf("apply user migration snapshot: transaction is nil")
	}
	return schema.ApplyMigrationSnapshot(ctx, tx, before, after)
}

func (a *App) UserMigrationSourceStatus(ctx context.Context) ([]UserMigrationSourceStatus, error) {
	if a == nil || a.userMigrationRecorder == nil {
		return nil, errors.New("automatic user migration recording is not enabled")
	}
	return a.userMigrationRecorder.Status(ctx)
}

func (a *App) RecoverUserMigrationSources(ctx context.Context) error {
	if a == nil || a.userMigrationRecorder == nil {
		return errors.New("automatic user migration recording is not enabled")
	}
	return a.userMigrationRecorder.Recover(ctx)
}

func (r *UserMigrationRunner) List(ctx context.Context) ([]UserMigrationEntry, error) {
	if r == nil || r.runner == nil {
		return nil, errors.New("user migration runner is nil")
	}
	return r.runner.List(ctx)
}

func (r *UserMigrationRunner) Up(ctx context.Context) ([]UserMigrationResult, error) {
	if r == nil || r.runner == nil {
		return nil, errors.New("user migration runner is nil")
	}
	return r.runner.Up(ctx)
}

func (r *UserMigrationRunner) Down(ctx context.Context) (UserMigrationResult, error) {
	if r == nil || r.runner == nil {
		return UserMigrationResult{}, errors.New("user migration runner is nil")
	}
	return r.runner.Down(ctx)
}

// DownN reverts at most count applied migrations in reverse order. Pass a
// negative count to revert all migrations or zero to perform no work.
func (r *UserMigrationRunner) DownN(ctx context.Context, count int) ([]UserMigrationResult, error) {
	if r == nil || r.runner == nil {
		return nil, errors.New("user migration runner is nil")
	}
	return r.runner.DownN(ctx, count)
}
