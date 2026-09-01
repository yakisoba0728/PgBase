package schema

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/webisnoteasy/PgBase/internal/jsonvalue"
)

func (s *Service) OperationStatus(ctx context.Context, ref OperationRef) (OperationReceipt, error) {
	if ref.actorID == uuid.Nil || ref.operationID == uuid.Nil {
		return OperationReceipt{}, ErrOperationNotFound
	}
	if err := s.ready(); err != nil {
		return OperationReceipt{}, err
	}
	row, err := s.readOwnedOperation(ctx, ref.actorID, ref.operationID)
	if err != nil {
		return OperationReceipt{}, err
	}
	return operationReceipt(row), nil
}

func (s *Service) readOwnedOperation(ctx context.Context, actorID, operationID uuid.UUID) (operationRow, error) {
	row, err := scanOperationRow(s.pool.QueryRow(ctx, `select `+operationRowColumns+`
		from pgbase_meta.schema_operations
		where actor_kind = 'administrator' and actor_id = $1 and operation_id = $2`, actorID, operationID))
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return operationRow{}, ErrOperationNotFound
		}
		return operationRow{}, fmt.Errorf("read schema operation receipt: %w", err)
	}
	return row, nil
}

func operationReceipt(row operationRow) OperationReceipt {
	receipt := OperationReceipt{
		OperationID: row.operationID, Operation: row.operation, CollectionID: row.resourceID,
		Status: row.status, BaseVersion: row.baseVersion,
		FailureCode: row.failureCode, CreatedAt: row.createdAt.UTC(), UpdatedAt: row.updatedAt.UTC(),
	}
	if row.committedVersion != nil {
		receipt.CommittedVersion = *row.committedVersion
	}
	if row.completedAt != nil {
		completedAt := row.completedAt.UTC()
		receipt.CompletedAt = &completedAt
	}
	return receipt
}

func (s *Service) ListOperations(ctx context.Context, query OperationQuery) (OperationPage, error) {
	if query.actorID == uuid.Nil || query.limit < 1 || query.limit > maximumOperationPageLimit {
		return OperationPage{}, ErrInvalidOperation
	}
	if err := s.ready(); err != nil {
		return OperationPage{}, err
	}
	var anchor *schemaOperationCursorAnchor
	if query.cursor != "" {
		decoded, err := s.operationCursors.decode(query.cursor, query.actorID, s.operationNow())
		if err != nil {
			return OperationPage{}, ErrInvalidOperation
		}
		anchor = &decoded
	}
	arguments := []any{query.actorID, query.limit + 1}
	anchorSQL := ""
	if anchor != nil {
		anchorSQL = " and (created_at, operation_id) < ($3, $4)"
		arguments = append(arguments, anchor.CreatedAt, anchor.OperationID)
	}
	rows, err := s.pool.Query(ctx, `select `+operationRowColumns+`
		from pgbase_meta.schema_operations
		where actor_kind = 'administrator' and actor_id = $1`+anchorSQL+`
		order by created_at desc, operation_id desc
		limit $2`, arguments...)
	if err != nil {
		return OperationPage{}, fmt.Errorf("list schema operation receipts: %w", err)
	}
	defer rows.Close()
	receipts := make([]OperationReceipt, 0, query.limit+1)
	for rows.Next() {
		row, err := scanOperationRow(rows)
		if err != nil {
			return OperationPage{}, fmt.Errorf("read schema operation history: %w", err)
		}
		receipts = append(receipts, operationReceipt(row))
	}
	if err := rows.Err(); err != nil {
		return OperationPage{}, fmt.Errorf("read schema operation history: %w", err)
	}
	hasMore := len(receipts) > query.limit
	if hasMore {
		receipts = receipts[:query.limit]
	}
	return s.boundedOperationPage(query.actorID, receipts, hasMore)
}

func (s *Service) boundedOperationPage(actorID uuid.UUID, receipts []OperationReceipt, hasMore bool) (OperationPage, error) {
	page := OperationPage{Items: make([]OperationReceipt, 0, len(receipts))}
	for _, receipt := range receipts {
		candidate := page
		candidate.Items = append(append([]OperationReceipt(nil), page.Items...), receipt)
		if operationPageSize(candidate) > maximumOperationPageBytes {
			hasMore = true
			break
		}
		page.Items = append(page.Items, receipt)
	}
	if hasMore && len(page.Items) != 0 {
		for len(page.Items) != 0 {
			last := page.Items[len(page.Items)-1]
			cursor, err := s.operationCursors.encode(actorID, schemaOperationCursorAnchor{
				CreatedAt: last.CreatedAt, OperationID: last.OperationID,
			}, s.operationNow())
			if err != nil {
				return OperationPage{}, ErrInvalidOperation
			}
			page.NextCursor = cursor
			if operationPageSize(page) <= maximumOperationPageBytes {
				return page, nil
			}
			page.Items = page.Items[:len(page.Items)-1]
		}
		return OperationPage{}, errors.New("schema operation page limit cannot fit one receipt")
	}
	if operationPageSize(page) > maximumOperationPageBytes {
		return OperationPage{}, errors.New("schema operation page exceeds serialized limit")
	}
	return page, nil
}

func operationPageSize(page OperationPage) int {
	encoded, err := json.Marshal(page)
	if err != nil {
		return maximumOperationPageBytes + 1
	}
	canonical, err := jsonvalue.Canonical(encoded)
	if err != nil {
		return maximumOperationPageBytes + 1
	}
	return len(canonical)
}

func (s *Service) operationNow() time.Time {
	if s.now == nil {
		return time.Now().UTC()
	}
	return s.now().UTC()
}
