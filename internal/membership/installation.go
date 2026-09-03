package membership

import (
	"bytes"
	"context"
	"fmt"

	"github.com/webisnoteasy/PgBase/internal/database"
	"github.com/webisnoteasy/PgBase/migrations/system"
)

type installationState uint8

const (
	installationInvalid installationState = iota
	installationPreMigration
	installationComplete
)

func inspectInstallation(ctx context.Context, db database.DBTX) (installationState, error) {
	if db == nil {
		return installationInvalid, ErrUnavailable
	}
	var capability, counters, ordinals, backfills, leases bool
	if err := db.QueryRow(ctx, `select
		to_regclass('pgbase_meta.record_membership_capability') is not null,
		to_regclass('pgbase_meta.collection_creation_counters') is not null,
		to_regclass('pgbase_meta.record_creation_ordinals') is not null,
		to_regclass('pgbase_meta.record_membership_backfills') is not null,
		to_regclass('pgbase_meta.runtime_writer_leases') is not null`).Scan(
		&capability, &counters, &ordinals, &backfills, &leases,
	); err != nil {
		return installationInvalid, fmt.Errorf("inspect record membership schema: %w", err)
	}
	present := 0
	for _, exists := range []bool{capability, counters, ordinals, backfills, leases} {
		if exists {
			present++
		}
	}
	if present == 5 {
		if err := verifyExactMembershipHistory(ctx, db, len(system.Migrations())); err != nil {
			return installationInvalid, err
		}
		return installationComplete, nil
	}
	if present != 0 {
		return installationInvalid, ErrInvalidState
	}
	if err := verifyExactMembershipHistory(ctx, db, 6); err != nil {
		return installationInvalid, err
	}
	return installationPreMigration, nil
}

func requireCompleteInstallation(ctx context.Context, db database.DBTX) error {
	installation, err := inspectInstallation(ctx, db)
	if err != nil {
		return err
	}
	if installation != installationComplete {
		return ErrInvalidState
	}
	return nil
}

func verifyExactMembershipHistory(ctx context.Context, db database.DBTX, expected int) error {
	migrations := system.Migrations()
	rows, err := db.Query(ctx, `select version, checksum
		from pgbase_meta.system_migrations order by version limit $1`, len(migrations)+1)
	if err != nil {
		return fmt.Errorf("read pre-membership migration history: %w", err)
	}
	defer rows.Close()
	applied := make([]membershipMigrationHistory, 0, len(migrations)+1)
	for rows.Next() {
		var migration membershipMigrationHistory
		if err := rows.Scan(&migration.version, &migration.checksum); err != nil {
			return fmt.Errorf("scan pre-membership migration history: %w", err)
		}
		applied = append(applied, migration)
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("iterate pre-membership migration history: %w", err)
	}
	return validateMembershipHistoryPrefix(applied, migrations, expected)
}

type membershipMigrationHistory struct {
	version  int64
	checksum []byte
}

func validateMembershipHistoryPrefix(
	applied []membershipMigrationHistory,
	migrations []system.Migration,
	expected int,
) error {
	if len(migrations) < 7 || migrations[5].Version != 6 || migrations[6].Version != 7 {
		return ErrInvalidState
	}
	switch {
	case expected == 6 && len(migrations) == 7:
		if len(applied) != 6 {
			return ErrInvalidState
		}
	case expected == len(migrations):
		if len(applied) < 7 || len(applied) > len(migrations) {
			return ErrInvalidState
		}
	default:
		return ErrInvalidState
	}
	for index, migration := range applied {
		if migration.version != migrations[index].Version ||
			!bytes.Equal(migration.checksum, migrations[index].SHA256[:]) {
			return ErrInvalidState
		}
	}
	return nil
}
