package migrate

import (
	"bytes"
	"context"
	"fmt"
)

// Status describes the embedded system schema migration state.
type Status struct {
	Current int64
	Latest  int64
	Dirty   bool
}

// Status returns the applied and latest embedded migration versions. Dirty is
// true when recorded migration history no longer matches this binary.
func (r *Runner) Status(ctx context.Context) (Status, error) {
	status := Status{}
	for _, migration := range r.migrations {
		if migration.Version > status.Latest {
			status.Latest = migration.Version
		}
	}
	if r.pool == nil {
		return Status{}, fmt.Errorf("migration pool is nil")
	}

	var historyTable *string
	if err := r.pool.QueryRow(ctx, "select to_regclass('pgbase_meta.system_migrations')::text").Scan(&historyTable); err != nil {
		return Status{}, fmt.Errorf("check system migration history: %w", err)
	}
	if historyTable == nil {
		return status, nil
	}

	applied, err := readApplied(ctx, r.pool)
	if err != nil {
		return Status{}, err
	}
	known := make(map[int64][32]byte, len(r.migrations))
	for _, migration := range r.migrations {
		known[migration.Version] = migration.SHA256
	}
	for version, checksum := range applied {
		if version > status.Current {
			status.Current = version
		}
		expected, ok := known[version]
		if !ok || !bytes.Equal(checksum, expected[:]) {
			status.Dirty = true
		}
	}
	return status, nil
}
