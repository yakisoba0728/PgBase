package membership

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/webisnoteasy/PgBase/internal/database"
)

const (
	defaultBackfillBatch = 128
)

type Service struct {
	pool          *database.Pool
	backfillBatch int
}

func New(pool *database.Pool) *Service {
	return &Service{pool: pool, backfillBatch: defaultBackfillBatch}
}

func (s *Service) ready() error {
	if s == nil || s.pool == nil || s.backfillBatch < 1 {
		return ErrUnavailable
	}
	return nil
}

func (s *Service) Status(ctx context.Context) (Status, error) {
	if err := s.ready(); err != nil {
		return Status{}, err
	}
	var status Status
	err := database.Retry(ctx, func() error {
		return s.pool.InTx(ctx, pgx.TxOptions{}, func(tx pgx.Tx) error {
			if err := pinSchemaShared(ctx, tx); err != nil {
				return err
			}
			if err := requireCompleteInstallation(ctx, tx); err != nil {
				return err
			}
			var err error
			status, err = scanStatus(tx.QueryRow(ctx, capabilitySelect))
			return err
		})
	}, nil)
	if err != nil {
		return Status{}, fmt.Errorf("read record membership status: %w", err)
	}
	return status, nil
}

const capabilityColumns = `state, generation, minimum_writer_version,
	cursor_issue_version, cutover_at, cutover_collection_count, cutover_record_count,
	ready_at, ready_collection_count, v5_enabled_at, v4_decode_until, v4_retired_at, updated_at`

const capabilitySelect = `select ` + capabilityColumns + `
	from pgbase_meta.record_membership_capability where singleton`

func scanStatus(row pgx.Row) (Status, error) {
	return scanStatusWithTrailing(row)
}

func scanStatusWithTrailing(row pgx.Row, trailing ...any) (Status, error) {
	var status Status
	var cutoverAt, readyAt, v5EnabledAt, decodeV4Until, v4RetiredAt pgtype.Timestamptz
	var cutoverCollections, cutoverRecords, readyCollections pgtype.Int8
	targets := []any{
		&status.State,
		&status.Generation,
		&status.MinimumWriterVersion,
		&status.CursorIssueVersion,
		&cutoverAt,
		&cutoverCollections,
		&cutoverRecords,
		&readyAt,
		&readyCollections,
		&v5EnabledAt,
		&decodeV4Until,
		&v4RetiredAt,
		&status.UpdatedAt,
	}
	targets = append(targets, trailing...)
	if err := row.Scan(targets...); err != nil {
		return Status{}, err
	}
	if status.Generation < 0 || status.MinimumWriterVersion < WriterA0 ||
		status.MinimumWriterVersion > WriterC ||
		(status.CursorIssueVersion != 4 && status.CursorIssueVersion != 5) {
		return Status{}, ErrInvalidState
	}
	switch status.State {
	case StateOff, StateCutover, StateDualWrite, StateReady:
	default:
		return Status{}, ErrInvalidState
	}
	status.CutoverAt = nullableTime(cutoverAt)
	status.cutoverCollections = nullableInt64(cutoverCollections)
	status.cutoverRecords = nullableInt64(cutoverRecords)
	status.ReadyAt = nullableTime(readyAt)
	status.readyCollections = nullableInt64(readyCollections)
	status.V5EnabledAt = nullableTime(v5EnabledAt)
	status.DecodeV4Until = nullableTime(decodeV4Until)
	status.V4RetiredAt = nullableTime(v4RetiredAt)
	status.UpdatedAt = status.UpdatedAt.UTC()
	return status, nil
}

func nullableInt64(value pgtype.Int8) *int64 {
	if !value.Valid {
		return nil
	}
	result := value.Int64
	return &result
}

func nullableTime(value pgtype.Timestamptz) *time.Time {
	if !value.Valid {
		return nil
	}
	timestamp := value.Time.UTC()
	return &timestamp
}

func (s *Service) refreshWriter(
	ctx context.Context,
	instanceID uuid.UUID,
	writerVersion WriterVersion,
) error {
	if err := s.ready(); err != nil {
		return err
	}
	if instanceID == uuid.Nil || writerVersion < WriterA0 || writerVersion > WriterC {
		return ErrInvalidState
	}
	// Deliberately no schema pin. A lease is meta state no collection DDL
	// touches, and a schema apply holds the exclusive side of that pin for the
	// whole of its DDL, so pinning would queue every renewal behind an ordinary
	// large ALTER and expire the lease it exists to keep alive. The fence does
	// not rely on it: Writer.Begin and Writer.LockCreate pin the schema
	// themselves and re-read the lease and minimum version where they write.
	return database.Retry(ctx, func() error {
		return s.pool.InTx(ctx, pgx.TxOptions{}, func(tx pgx.Tx) error {
			if err := requireCompleteInstallation(ctx, tx); err != nil {
				return err
			}
			var minimum WriterVersion
			if err := tx.QueryRow(ctx, `select minimum_writer_version
				from pgbase_meta.record_membership_capability where singleton for share`).Scan(&minimum); err != nil {
				return fmt.Errorf("read minimum record writer version: %w", err)
			}
			if writerVersion < minimum {
				return ErrWriterTooOld
			}
			now, err := databaseTime(ctx, tx)
			if err != nil {
				return err
			}
			tag, err := tx.Exec(ctx, `insert into pgbase_meta.runtime_writer_leases
				(instance_id, writer_version, started_at, heartbeat_at, lease_expires_at)
				values ($1, $2, $3, $3, $4)
				on conflict (instance_id) do update set
					heartbeat_at = greatest(
						pgbase_meta.runtime_writer_leases.heartbeat_at,
						excluded.heartbeat_at),
					lease_expires_at = greatest(
						pgbase_meta.runtime_writer_leases.lease_expires_at,
						excluded.lease_expires_at)
				where pgbase_meta.runtime_writer_leases.writer_version = excluded.writer_version`,
				instanceID, writerVersion, now, now.Add(writerLeaseTTL))
			if err != nil {
				return fmt.Errorf("refresh record writer lease: %w", err)
			}
			if tag.RowsAffected() != 1 {
				return ErrInvalidState
			}
			return nil
		})
	}, nil)
}

func pinSchemaShared(ctx context.Context, tx pgx.Tx) error {
	var version int64
	if err := tx.QueryRow(ctx, `select version
		from pgbase_meta.schema_state where singleton for share`).Scan(&version); err != nil {
		return fmt.Errorf("pin schema for record membership: %w", err)
	}
	if version < 0 {
		return ErrInvalidState
	}
	return nil
}

func pinSchemaExclusive(ctx context.Context, tx pgx.Tx) error {
	var version int64
	if err := tx.QueryRow(ctx, `select version
		from pgbase_meta.schema_state where singleton for update`).Scan(&version); err != nil {
		return fmt.Errorf("barrier schema for record membership cutover: %w", err)
	}
	if version < 0 {
		return ErrInvalidState
	}
	return nil
}

func activeWriterFleet(ctx context.Context, tx pgx.Tx, now time.Time) ([]FleetMember, error) {
	rows, err := tx.Query(ctx, `select instance_id, writer_version
		from pgbase_meta.runtime_writer_leases
		where lease_expires_at > $1 order by instance_id`, now)
	if err != nil {
		return nil, fmt.Errorf("read active record writer leases: %w", err)
	}
	defer rows.Close()
	members := make([]FleetMember, 0)
	for rows.Next() {
		var member FleetMember
		if err := rows.Scan(&member.InstanceID, &member.Version); err != nil {
			return nil, fmt.Errorf("scan active record writer lease: %w", err)
		}
		if member.InstanceID == uuid.Nil || member.Version < WriterA0 || member.Version > WriterC {
			return nil, ErrInvalidState
		}
		members = append(members, member)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate active record writer leases: %w", err)
	}
	if len(members) == 0 {
		return nil, ErrWriterFleetTooOld
	}
	return members, nil
}

func verifyFleetProof(
	ctx context.Context,
	tx pgx.Tx,
	proof FleetProof,
	required WriterVersion,
	now time.Time,
) error {
	if len(proof.Members) == 0 || now.IsZero() {
		return ErrWriterFleetTooOld
	}
	for index, member := range proof.Members {
		if member.InstanceID == uuid.Nil || member.Version < required || member.Version > WriterC ||
			(index > 0 && bytes.Compare(proof.Members[index-1].InstanceID[:], member.InstanceID[:]) >= 0) {
			return ErrWriterFleetTooOld
		}
	}
	active, err := activeWriterFleet(ctx, tx, now)
	if err != nil {
		return err
	}
	if len(active) != len(proof.Members) {
		return ErrWriterFleetTooOld
	}
	for index, member := range active {
		if member != proof.Members[index] || member.Version < required {
			return ErrWriterFleetTooOld
		}
	}
	return nil
}

func databaseTime(ctx context.Context, tx pgx.Tx) (time.Time, error) {
	var now time.Time
	if err := tx.QueryRow(ctx, `select clock_timestamp()`).Scan(&now); err != nil {
		return time.Time{}, fmt.Errorf("read PostgreSQL wall time: %w", err)
	}
	return now.UTC(), nil
}

func statusForUpdate(ctx context.Context, tx pgx.Tx) (Status, error) {
	return scanStatus(tx.QueryRow(ctx, capabilitySelect+` for update`))
}

func requireState(status Status, state State, issueVersion int16) error {
	if status.State != state || status.CursorIssueVersion != issueVersion {
		return ErrInvalidState
	}
	return nil
}

func rowsAffectedOne(tag pgconn.CommandTag, err error, operation string) error {
	if err != nil {
		return fmt.Errorf("%s: %w", operation, err)
	}
	if tag.RowsAffected() != 1 {
		return errors.New(operation + ": unexpected affected rows")
	}
	return nil
}
