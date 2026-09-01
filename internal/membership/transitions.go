package membership

import (
	"context"
	"fmt"
	"math"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/webisnoteasy/PgBase/internal/database"
)

type cutoverCollection struct {
	id        uuid.UUID
	tableName string
}

func (s *Service) Cutover(ctx context.Context, proof FleetProof) (CutoverReceipt, error) {
	if err := s.ready(); err != nil {
		return CutoverReceipt{}, err
	}
	committed, err := s.beginCutover(ctx, proof)
	if err != nil {
		return CutoverReceipt{}, err
	}
	if committed != nil {
		return *committed, nil
	}
	return s.finishCutover(ctx)
}

func (s *Service) beginCutover(ctx context.Context, proof FleetProof) (*CutoverReceipt, error) {
	var committed *CutoverReceipt
	err := database.Retry(ctx, func() error {
		return s.pool.InTx(ctx, pgx.TxOptions{}, func(tx pgx.Tx) error {
			if err := pinSchemaExclusive(ctx, tx); err != nil {
				return err
			}
			if err := requireCompleteInstallation(ctx, tx); err != nil {
				return err
			}
			if _, err := lockCutoverCollections(ctx, tx); err != nil {
				return err
			}
			status, err := statusForUpdate(ctx, tx)
			if err != nil {
				return fmt.Errorf("lock record membership capability: %w", err)
			}
			switch status.State {
			case StateDualWrite, StateReady:
				receipt, err := readCutoverReceipt(status)
				if err != nil {
					return err
				}
				committed = &receipt
				return nil
			case StateCutover:
				if status.Generation < 1 || status.CutoverAt == nil {
					return ErrInvalidState
				}
				return nil
			case StateOff:
			default:
				return ErrInvalidState
			}
			if err := requireState(status, StateOff, 4); err != nil || status.Generation != 0 ||
				status.MinimumWriterVersion != WriterA0 || status.CutoverAt != nil || status.ReadyAt != nil ||
				status.V5EnabledAt != nil || status.DecodeV4Until != nil || status.V4RetiredAt != nil {
				return ErrInvalidState
			}
			cutoverAt, err := databaseTime(ctx, tx)
			if err != nil {
				return err
			}
			if err := verifyFleetProof(ctx, tx, proof, WriterA, cutoverAt); err != nil {
				return err
			}
			generation := status.Generation + 1
			tag, err := tx.Exec(ctx, `update pgbase_meta.record_membership_capability set
				state = 'cutover', generation = $1, minimum_writer_version = $2,
				cursor_issue_version = 4, cutover_at = $3, ready_at = null,
				v5_enabled_at = null, v4_decode_until = null, v4_retired_at = null,
				updated_at = $3 where singleton`, generation, WriterA, cutoverAt)
			return rowsAffectedOne(tag, err, "start record membership cutover barrier")
		})
	}, nil)
	if err != nil {
		return nil, err
	}
	return committed, nil
}

func (s *Service) finishCutover(ctx context.Context) (CutoverReceipt, error) {
	var receipt CutoverReceipt
	err := database.Retry(ctx, func() error {
		return s.pool.InTx(ctx, pgx.TxOptions{}, func(tx pgx.Tx) error {
			if err := pinSchemaShared(ctx, tx); err != nil {
				return err
			}
			if err := requireCompleteInstallation(ctx, tx); err != nil {
				return err
			}
			collections, err := lockCutoverCollections(ctx, tx)
			if err != nil {
				return err
			}
			status, err := scanStatus(tx.QueryRow(ctx, capabilitySelect))
			if err != nil {
				return err
			}
			if status.State == StateDualWrite || status.State == StateReady {
				var readErr error
				receipt, readErr = readCutoverReceipt(status)
				return readErr
			}
			if status.State != StateCutover || status.Generation < 1 || status.CutoverAt == nil ||
				status.MinimumWriterVersion != WriterA || status.CursorIssueVersion != 4 {
				return ErrInvalidState
			}
			var totalRecords int64
			for _, collection := range collections {
				count, upperCreatedAt, upperID, err := cutoverSnapshot(ctx, tx, collection.tableName)
				if err != nil {
					return err
				}
				if count > math.MaxInt64-totalRecords {
					return ErrInvalidState
				}
				totalRecords += count
				tag, err := tx.Exec(ctx, `update pgbase_meta.collection_creation_counters
					set generation = $2, last_ordinal = $3, updated_at = $4
					where collection_id = $1`, collection.id, status.Generation, count, *status.CutoverAt)
				if err := rowsAffectedOne(tag, err, "initialize record creation counter"); err != nil {
					return err
				}
				tag, err = tx.Exec(ctx, `update pgbase_meta.record_membership_backfills set
					generation = $2,
					state = 'backfilling',
					cutover_count = $3,
					cutover_last_created_at = $4,
					cutover_last_record_id = $5,
					checkpoint_created_at = null,
					checkpoint_record_id = null,
					last_assigned_ordinal = 0,
					validated_base_count = null,
					validated_sidecar_count = null,
					updated_at = $6,
					completed_at = null
					where collection_id = $1`,
					collection.id, status.Generation, count, upperCreatedAt, upperID, *status.CutoverAt)
				if err := rowsAffectedOne(tag, err, "initialize record membership backfill"); err != nil {
					return err
				}
			}
			locked, err := statusForUpdate(ctx, tx)
			if err != nil {
				return err
			}
			if locked.State != StateCutover || locked.Generation != status.Generation ||
				locked.CutoverAt == nil || !locked.CutoverAt.Equal(*status.CutoverAt) {
				return ErrInvalidState
			}
			completedAt, err := databaseTime(ctx, tx)
			if err != nil {
				return err
			}
			if _, err := tx.Exec(ctx, `update pgbase_meta.collection_creation_counters
				set updated_at = $1 where generation = $2`, completedAt, status.Generation); err != nil {
				return fmt.Errorf("stamp record creation counters at cutover: %w", err)
			}
			if _, err := tx.Exec(ctx, `update pgbase_meta.record_membership_backfills
				set updated_at = $1 where generation = $2`, completedAt, status.Generation); err != nil {
				return fmt.Errorf("stamp record membership backfills at cutover: %w", err)
			}
			tag, err := tx.Exec(ctx, `update pgbase_meta.record_membership_capability set
				state = 'dual_write', cutover_collection_count = $2,
				cutover_record_count = $3, updated_at = $1 where singleton`,
				completedAt, len(collections), totalRecords)
			if err := rowsAffectedOne(tag, err, "enable dual-write record membership"); err != nil {
				return err
			}
			receipt = CutoverReceipt{
				Generation: status.Generation, Collections: len(collections), Records: totalRecords,
				CutoverAt: *status.CutoverAt,
			}
			return nil
		})
	}, nil)
	if err != nil {
		return CutoverReceipt{}, err
	}
	return receipt, nil
}

func readCutoverReceipt(status Status) (CutoverReceipt, error) {
	if status.Generation < 1 || status.CutoverAt == nil ||
		status.cutoverCollections == nil || status.cutoverRecords == nil ||
		(status.State != StateDualWrite && status.State != StateReady) {
		return CutoverReceipt{}, ErrInvalidState
	}
	collections := int(*status.cutoverCollections)
	if collections < 0 || int64(collections) != *status.cutoverCollections || *status.cutoverRecords < 0 {
		return CutoverReceipt{}, ErrInvalidState
	}
	return CutoverReceipt{
		Generation: status.Generation, Collections: collections, Records: *status.cutoverRecords,
		CutoverAt: *status.CutoverAt,
	}, nil
}

func lockCutoverCollections(ctx context.Context, tx pgx.Tx) ([]cutoverCollection, error) {
	rows, err := tx.Query(ctx, `select counters.collection_id, collections.table_name
		from pgbase_meta.collection_creation_counters as counters
		join pgbase_meta.collections as collections on collections.id = counters.collection_id
		order by counters.collection_id for update of counters`)
	if err != nil {
		return nil, fmt.Errorf("lock record creation counters: %w", err)
	}
	defer rows.Close()
	collections := make([]cutoverCollection, 0)
	for rows.Next() {
		var collection cutoverCollection
		if err := rows.Scan(&collection.id, &collection.tableName); err != nil {
			return nil, fmt.Errorf("scan record creation counter: %w", err)
		}
		if collection.id == uuid.Nil || collection.tableName == "" {
			return nil, ErrInvalidState
		}
		collections = append(collections, collection)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate record creation counters: %w", err)
	}
	rows.Close()
	var collectionCount int
	if err := tx.QueryRow(ctx, `select count(*) from pgbase_meta.collections`).Scan(&collectionCount); err != nil {
		return nil, fmt.Errorf("count collections for membership cutover: %w", err)
	}
	if collectionCount != len(collections) {
		return nil, ErrMembershipDrift
	}
	return collections, nil
}

func cutoverSnapshot(ctx context.Context, tx pgx.Tx, tableName string) (int64, *time.Time, *uuid.UUID, error) {
	table := pgx.Identifier{"app", tableName}.Sanitize()
	var count int64
	if err := tx.QueryRow(ctx, `select count(*) from `+table).Scan(&count); err != nil {
		return 0, nil, nil, fmt.Errorf("count cutover records: %w", err)
	}
	if count == 0 {
		return 0, nil, nil, nil
	}
	var createdAt time.Time
	var recordID uuid.UUID
	if err := tx.QueryRow(ctx, `select created_at, id from `+table+
		` order by created_at desc, id desc limit 1`).Scan(&createdAt, &recordID); err != nil {
		return 0, nil, nil, fmt.Errorf("read cutover upper record: %w", err)
	}
	createdAt = createdAt.UTC()
	return count, &createdAt, &recordID, nil
}

func (s *Service) PromoteReady(ctx context.Context) (ReadyReceipt, error) {
	if err := s.ready(); err != nil {
		return ReadyReceipt{}, err
	}
	var receipt ReadyReceipt
	err := database.Retry(ctx, func() error {
		return s.pool.InTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead}, func(tx pgx.Tx) error {
			if err := pinSchemaShared(ctx, tx); err != nil {
				return err
			}
			if err := requireCompleteInstallation(ctx, tx); err != nil {
				return err
			}
			status, err := statusForUpdate(ctx, tx)
			if err != nil {
				return err
			}
			if status.State == StateReady && status.ReadyAt != nil {
				if status.readyCollections == nil {
					return ErrInvalidState
				}
				collections := int(*status.readyCollections)
				if collections < 0 || int64(collections) != *status.readyCollections {
					return ErrInvalidState
				}
				receipt = ReadyReceipt{
					Generation: status.Generation, Collections: collections, ReadyAt: *status.ReadyAt,
				}
				return nil
			}
			if err := requireState(status, StateDualWrite, 4); err != nil || status.Generation < 1 ||
				status.MinimumWriterVersion != WriterA {
				return ErrInvalidState
			}
			now, err := databaseTime(ctx, tx)
			if err != nil {
				return err
			}
			var collections, incomplete int
			if err := tx.QueryRow(ctx, `select count(*), count(*) filter (
				where state <> 'ready' or generation <> $1 or completed_at is null or
				validated_base_count is null or validated_sidecar_count is null or
				validated_base_count <> validated_sidecar_count)
				from pgbase_meta.record_membership_backfills`, status.Generation).Scan(&collections, &incomplete); err != nil {
				return fmt.Errorf("validate record membership backfill readiness: %w", err)
			}
			var catalogCollections int
			if err := tx.QueryRow(ctx, `select count(*) from pgbase_meta.collections`).Scan(&catalogCollections); err != nil {
				return err
			}
			if incomplete != 0 || collections != catalogCollections {
				return ErrBackfillIncomplete
			}
			tag, err := tx.Exec(ctx, `update pgbase_meta.record_membership_capability
				set state = 'ready', ready_at = $1, ready_collection_count = $2,
				updated_at = $1 where singleton`, now, collections)
			if err := rowsAffectedOne(tag, err, "promote record membership ready"); err != nil {
				return err
			}
			receipt = ReadyReceipt{Generation: status.Generation, Collections: collections, ReadyAt: now}
			return nil
		})
	}, nil)
	if err != nil {
		return ReadyReceipt{}, err
	}
	return receipt, nil
}

func (s *Service) EnableV5(ctx context.Context, proof FleetProof) (ActivationReceipt, error) {
	if err := s.ready(); err != nil {
		return ActivationReceipt{}, err
	}
	var receipt ActivationReceipt
	err := database.Retry(ctx, func() error {
		return s.pool.InTx(ctx, pgx.TxOptions{}, func(tx pgx.Tx) error {
			if err := pinSchemaShared(ctx, tx); err != nil {
				return err
			}
			if err := requireCompleteInstallation(ctx, tx); err != nil {
				return err
			}
			status, err := statusForUpdate(ctx, tx)
			if err != nil {
				return err
			}
			if status.State == StateReady && status.CursorIssueVersion == 5 &&
				status.V5EnabledAt != nil && status.DecodeV4Until != nil {
				receipt = ActivationReceipt{
					Generation: status.Generation, MinimumWriterVersion: status.MinimumWriterVersion,
					CursorIssueVersion: 5, EnabledAt: *status.V5EnabledAt,
					DecodeV4Until: *status.DecodeV4Until,
				}
				return nil
			}
			if err := requireState(status, StateReady, 4); err != nil || status.V4RetiredAt != nil {
				return ErrInvalidState
			}
			now, err := databaseTime(ctx, tx)
			if err != nil {
				return err
			}
			if err := verifyFleetProof(ctx, tx, proof, WriterB, now); err != nil {
				return err
			}
			decodeUntil := now.Add(CursorTTL)
			tag, err := tx.Exec(ctx, `update pgbase_meta.record_membership_capability set
				minimum_writer_version = $1, cursor_issue_version = 5,
				v5_enabled_at = $2, v4_decode_until = $3, updated_at = $2
				where singleton`, WriterB, now, decodeUntil)
			if err := rowsAffectedOne(tag, err, "enable cursor v5"); err != nil {
				return err
			}
			receipt = ActivationReceipt{
				Generation: status.Generation, MinimumWriterVersion: WriterB,
				CursorIssueVersion: 5, EnabledAt: now, DecodeV4Until: decodeUntil,
			}
			return nil
		})
	}, nil)
	if err != nil {
		return ActivationReceipt{}, err
	}
	return receipt, nil
}

func (s *Service) DisableV5(ctx context.Context) (RollbackReceipt, error) {
	if err := s.ready(); err != nil {
		return RollbackReceipt{}, err
	}
	var receipt RollbackReceipt
	err := database.Retry(ctx, func() error {
		return s.pool.InTx(ctx, pgx.TxOptions{}, func(tx pgx.Tx) error {
			if err := pinSchemaShared(ctx, tx); err != nil {
				return err
			}
			if err := requireCompleteInstallation(ctx, tx); err != nil {
				return err
			}
			status, err := statusForUpdate(ctx, tx)
			if err != nil {
				return err
			}
			if status.State == StateReady && status.CursorIssueVersion == 4 &&
				status.V5EnabledAt == nil && status.DecodeV4Until == nil && status.V4RetiredAt == nil {
				receipt = RollbackReceipt{
					Generation: status.Generation, MinimumWriterVersion: status.MinimumWriterVersion,
					CursorIssueVersion: 4, RolledBackAt: status.UpdatedAt,
				}
				return nil
			}
			if err := requireState(status, StateReady, 5); err != nil || status.V4RetiredAt != nil {
				return ErrInvalidState
			}
			now, err := databaseTime(ctx, tx)
			if err != nil {
				return err
			}
			tag, err := tx.Exec(ctx, `update pgbase_meta.record_membership_capability set
				minimum_writer_version = $1, cursor_issue_version = 4,
				v5_enabled_at = null, v4_decode_until = null, updated_at = $2
				where singleton`, WriterA, now)
			if err := rowsAffectedOne(tag, err, "disable cursor v5"); err != nil {
				return err
			}
			receipt = RollbackReceipt{
				Generation: status.Generation, MinimumWriterVersion: WriterA,
				CursorIssueVersion: 4, RolledBackAt: now,
			}
			return nil
		})
	}, nil)
	if err != nil {
		return RollbackReceipt{}, err
	}
	return receipt, nil
}

func (s *Service) RetireV4(ctx context.Context, proof FleetProof) (RetirementReceipt, error) {
	if err := s.ready(); err != nil {
		return RetirementReceipt{}, err
	}
	var receipt RetirementReceipt
	err := database.Retry(ctx, func() error {
		return s.pool.InTx(ctx, pgx.TxOptions{}, func(tx pgx.Tx) error {
			if err := pinSchemaShared(ctx, tx); err != nil {
				return err
			}
			if err := requireCompleteInstallation(ctx, tx); err != nil {
				return err
			}
			status, err := statusForUpdate(ctx, tx)
			if err != nil {
				return err
			}
			if status.V4RetiredAt != nil && status.State == StateReady && status.CursorIssueVersion == 5 {
				receipt = RetirementReceipt{Generation: status.Generation, RetiredAt: *status.V4RetiredAt}
				return nil
			}
			if err := requireState(status, StateReady, 5); err != nil || status.DecodeV4Until == nil || status.V4RetiredAt != nil {
				return ErrInvalidState
			}
			now, err := databaseTime(ctx, tx)
			if err != nil {
				return err
			}
			if now.Before(*status.DecodeV4Until) {
				return ErrCursorDrainPending
			}
			if err := verifyFleetProof(ctx, tx, proof, WriterB, now); err != nil {
				return err
			}
			tag, err := tx.Exec(ctx, `update pgbase_meta.record_membership_capability
				set v4_retired_at = $1, updated_at = $1 where singleton`, now)
			if err := rowsAffectedOne(tag, err, "retire cursor v4"); err != nil {
				return err
			}
			receipt = RetirementReceipt{Generation: status.Generation, RetiredAt: now}
			return nil
		})
	}, nil)
	if err != nil {
		return RetirementReceipt{}, err
	}
	return receipt, nil
}
