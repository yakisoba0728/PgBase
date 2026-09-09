package usermigrate

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"regexp"
	"slices"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/webisnoteasy/PgBase/internal/database"
)

const (
	userMigrationLock int64 = 0x7067626173650004
	schemaWriterLock  int64 = 0x7067626173650002
)

var validID = regexp.MustCompile(`^[0-9]{4,14}_[a-z0-9]+(?:_[a-z0-9]+)*$`)

type Runner struct {
	pool        *database.Pool
	migrations  []Migration
	validate    Validator
	afterCommit AfterCommit
}

func New(pool *database.Pool, migrations []Migration, validate Validator, afterCommit AfterCommit) (*Runner, error) {
	manifest := append([]Migration(nil), migrations...)
	if err := validateManifest(manifest); err != nil {
		return nil, err
	}
	return &Runner{pool: pool, migrations: manifest, validate: validate, afterCommit: afterCommit}, nil
}

func (r *Runner) List(ctx context.Context) ([]Entry, error) {
	if r == nil || r.pool == nil {
		return nil, errors.New("user migration pool is nil")
	}
	applied, err := r.applied(ctx, r.pool)
	if err != nil {
		return nil, err
	}
	if err := inspect(r.migrations, applied); err != nil {
		return nil, err
	}
	entries := make([]Entry, len(r.migrations))
	for index, migration := range r.migrations {
		entries[index] = Entry{ID: migration.ID, Revision: migration.Revision}
		if stored, ok := applied[migration.ID]; ok {
			entries[index].Applied, entries[index].AppliedAt, entries[index].ExecutionMS = true, stored.appliedAt, stored.executionMS
		}
	}
	return entries, nil
}

func (r *Runner) Up(ctx context.Context) ([]Result, error) {
	results := []Result{}
	for {
		result, found, err := r.applyOne(ctx, true)
		if found {
			results = append(results, result)
		}
		if err != nil {
			return results, err
		}
		if !found {
			return results, nil
		}
	}
}

func (r *Runner) Down(ctx context.Context) (Result, error) {
	result, found, err := r.applyOne(ctx, false)
	if err != nil {
		return result, err
	}
	if !found {
		return Result{}, errors.New("no applied user migration to revert")
	}
	return result, nil
}

func (r *Runner) applyOne(ctx context.Context, up bool) (result Result, found bool, err error) {
	if r == nil || r.pool == nil {
		return result, false, errors.New("user migration pool is nil")
	}
	err = r.pool.InTx(ctx, pgx.TxOptions{}, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `set local lock_timeout='5s'; set local statement_timeout=0`); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `select pg_advisory_xact_lock($1),pg_advisory_xact_lock($2)`, userMigrationLock, schemaWriterLock); err != nil {
			return fmt.Errorf("acquire user migration locks: %w", err)
		}
		var schemaVersion int64
		if err := tx.QueryRow(ctx, `select version from pgbase_meta.schema_state where singleton for update`).Scan(&schemaVersion); err != nil {
			return fmt.Errorf("lock schema state: %w", err)
		}
		applied, err := r.applied(ctx, tx)
		if err != nil {
			return err
		}
		if err := inspect(r.migrations, applied); err != nil {
			return err
		}
		index := len(applied)
		if !up {
			index--
		}
		if index < 0 || index >= len(r.migrations) {
			return nil
		}
		migration := r.migrations[index]
		callback := migration.Up
		direction := "up"
		if !up {
			callback, direction = migration.Down, "down"
		}
		started := time.Now()
		if err := callback(ctx, tx); err != nil {
			return fmt.Errorf("user migration %s %s: %w", migration.ID, direction, err)
		}
		executionMS := time.Since(started).Milliseconds()
		if err := r.verifyControlState(ctx, tx, applied, schemaVersion); err != nil {
			return fmt.Errorf("validate user migration %s control state: %w", migration.ID, err)
		}
		if r.validate != nil {
			if err := r.validate(ctx, tx); err != nil {
				return fmt.Errorf("validate user migration %s: %w", migration.ID, err)
			}
		}
		if up {
			_, err = tx.Exec(ctx, `insert into pgbase_meta.user_migrations(id,revision,applied_at,execution_ms) values($1,$2,transaction_timestamp(),$3)`, migration.ID, migration.Revision[:], executionMS)
		} else {
			_, err = tx.Exec(ctx, `delete from pgbase_meta.user_migrations where id=$1 and revision=$2`, migration.ID, migration.Revision[:])
		}
		if err != nil {
			return fmt.Errorf("record user migration %s: %w", migration.ID, err)
		}
		if _, err := tx.Exec(ctx, `update pgbase_meta.schema_state set version=version+1,updated_at=transaction_timestamp() where singleton`); err != nil {
			return fmt.Errorf("advance schema state: %w", err)
		}
		result = Result{Migration: Entry{ID: migration.ID, Revision: migration.Revision, Applied: up, AppliedAt: time.Now(), ExecutionMS: executionMS}, Direction: direction, Applied: up}
		found = true
		return nil
	})
	if err != nil {
		return Result{}, false, err
	}
	if found && r.afterCommit != nil {
		if err := r.afterCommit(ctx); err != nil {
			return result, true, fmt.Errorf("refresh schema after user migration: %w", err)
		}
	}
	return result, found, nil
}

func (r *Runner) verifyControlState(ctx context.Context, tx pgx.Tx, before map[string]storedEntry, schemaVersion int64) error {
	after, err := r.applied(ctx, tx)
	if err != nil {
		return err
	}
	if len(after) != len(before) {
		return errors.New("user migration callback changed the managed migration ledger")
	}
	for id, expected := range before {
		actual, ok := after[id]
		if !ok || !bytes.Equal(actual.revision, expected.revision) ||
			!actual.appliedAt.Equal(expected.appliedAt) || actual.executionMS != expected.executionMS {
			return errors.New("user migration callback changed the managed migration ledger")
		}
	}
	var currentVersion int64
	if err := tx.QueryRow(ctx, `select version from pgbase_meta.schema_state where singleton`).Scan(&currentVersion); err != nil {
		return err
	}
	if currentVersion != schemaVersion {
		return errors.New("user migration callback changed the managed schema version")
	}
	return nil
}

type storedEntry struct {
	revision    []byte
	appliedAt   time.Time
	executionMS int64
}

func (r *Runner) applied(ctx context.Context, db database.DBTX) (map[string]storedEntry, error) {
	rows, err := db.Query(ctx, `select id,revision,applied_at,execution_ms from pgbase_meta.user_migrations order by id`)
	if err != nil {
		return nil, fmt.Errorf("read user migrations: %w", err)
	}
	defer rows.Close()
	result := map[string]storedEntry{}
	for rows.Next() {
		var id string
		var entry storedEntry
		if err := rows.Scan(&id, &entry.revision, &entry.appliedAt, &entry.executionMS); err != nil {
			return nil, err
		}
		result[id] = entry
	}
	return result, rows.Err()
}

func validateManifest(migrations []Migration) error {
	for index, migration := range migrations {
		if !validID.MatchString(migration.ID) || migration.Revision == ([32]byte{}) || migration.Up == nil || migration.Down == nil {
			return fmt.Errorf("invalid user migration %q", migration.ID)
		}
		if index > 0 && migrations[index-1].ID >= migration.ID {
			return errors.New("user migrations must be unique and strictly ordered")
		}
	}
	return nil
}

func inspect(manifest []Migration, applied map[string]storedEntry) error {
	if len(applied) > len(manifest) {
		return errors.New("user migration history contains an unknown migration")
	}
	ids := make([]string, 0, len(applied))
	for id := range applied {
		ids = append(ids, id)
	}
	slices.Sort(ids)
	for index, id := range ids {
		if manifest[index].ID != id {
			return fmt.Errorf("user migration history is not a manifest prefix at %q", id)
		}
		if !bytes.Equal(manifest[index].Revision[:], applied[id].revision) {
			return fmt.Errorf("user migration %s revision mismatch", id)
		}
	}
	return nil
}
