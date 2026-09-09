package pgbase

import (
	"context"
	"crypto/sha256"
	"errors"
	"sort"
	"sync"

	"github.com/jackc/pgx/v5"
	"github.com/webisnoteasy/PgBase/internal/doctor"
	"github.com/webisnoteasy/PgBase/internal/schema"
	"github.com/webisnoteasy/PgBase/internal/usermigrate"
)

// UserMigration is application Go code executed transactionally in manifest order.
// Source must contain the exact migration .go file bytes, normally supplied by
// the generator's go:embed registry, so an applied file edit changes its checksum.
type UserMigration struct {
	ID     string
	Source []byte
	Up     func(context.Context, pgx.Tx) error
	Down   func(context.Context, pgx.Tx) error
}

type UserMigrationEntry = usermigrate.Entry
type UserMigrationResult = usermigrate.Result

var registeredUserMigrations struct {
	sync.Mutex
	items []UserMigration
}

// RegisterUserMigration adds one migration to the process manifest.
func RegisterUserMigration(migration UserMigration) {
	registeredUserMigrations.Lock()
	defer registeredUserMigrations.Unlock()
	migration.Source = append([]byte(nil), migration.Source...)
	registeredUserMigrations.items = append(registeredUserMigrations.items, migration)
}

// RegisteredUserMigrations returns an isolated snapshot of the process manifest.
func RegisteredUserMigrations() []UserMigration {
	registeredUserMigrations.Lock()
	defer registeredUserMigrations.Unlock()
	result := append([]UserMigration(nil), registeredUserMigrations.items...)
	for index := range result {
		result[index].Source = append([]byte(nil), result[index].Source...)
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
		internal[index] = usermigrate.Migration{ID: migration.ID, Revision: sha256.Sum256(migration.Source), Up: migration.Up, Down: migration.Down}
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
	runner, err := usermigrate.New(app.pool, internal, validate, refresh)
	if err != nil {
		return nil, err
	}
	return &UserMigrationRunner{runner: runner}, nil
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
