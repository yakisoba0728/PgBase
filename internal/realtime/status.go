package realtime

import (
	"context"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
)

type Status struct {
	Pending       int64      `json:"pending"`
	Dead          int64      `json:"dead"`
	Delivered     int64      `json:"delivered"`
	OldestPending *time.Time `json:"oldestPending,omitempty"`
	CursorFloor   int64      `json:"cursorFloor"`
}

func (s *Service) Status(ctx context.Context) (Status, error) {
	var status Status
	err := s.pool.QueryRow(ctx, `SELECT
 count(*) FILTER(WHERE delivered_at IS NULL AND dead_at IS NULL),
 count(*) FILTER(WHERE dead_at IS NOT NULL),count(*) FILTER(WHERE delivered_at IS NOT NULL),
 min(available_at) FILTER(WHERE delivered_at IS NULL AND dead_at IS NULL),
 (SELECT floor_sequence FROM pgbase_meta.realtime_retention WHERE singleton)
 FROM pgbase_meta.outbox_delivery`).Scan(&status.Pending, &status.Dead, &status.Delivered, &status.OldestPending, &status.CursorFloor)
	return status, err
}

// DeadEvent is the operator view of a dead delivery. The envelope and record
// snapshot stay private; the event id is what Retry takes.
type DeadEvent struct {
	EventID        uuid.UUID `json:"eventId"`
	CollectionName string    `json:"collectionName"`
	RecordID       uuid.UUID `json:"recordId"`
	Operation      string    `json:"operation"`
	RecordVersion  int64     `json:"recordVersion"`
	Attempts       int       `json:"attempts"`
	CreatedAt      time.Time `json:"createdAt"`
	DeadAt         time.Time `json:"deadAt"`
}

type DeadPage struct {
	Items      []DeadEvent `json:"items"`
	NextBefore string      `json:"nextBefore,omitempty"`
}

// DeadEvents lists dead deliveries newest first. The opaque cursor carries the
// position itself, so retried rows leaving the set never invalidate it.
// shortcut: no index covers dead_at, so this scans outbox_delivery like Status
// does; add a partial dead_at index if delivered markers grow large.
func (s *Service) DeadEvents(ctx context.Context, before string, limit int) (DeadPage, error) {
	if limit <= 0 || limit > 100 {
		limit = 50
	}
	var at *time.Time
	after := uuid.Max
	if before != "" {
		micros, id, ok := strings.Cut(before, "_")
		value, err := strconv.ParseInt(micros, 10, 64)
		parsed, idErr := uuid.Parse(id)
		if !ok || err != nil || idErr != nil {
			return DeadPage{}, ErrCursorInvalid
		}
		cursor := time.UnixMicro(value).UTC()
		at, after = &cursor, parsed
	}
	rows, err := s.pool.Query(ctx, `SELECT d.event_id,o.collection_name,o.record_id,o.operation,o.record_version,d.attempts,o.created_at,d.dead_at
 FROM pgbase_meta.outbox_delivery d JOIN pgbase_meta.record_outbox o USING(event_id)
 WHERE d.dead_at IS NOT NULL AND d.delivered_at IS NULL AND ($1::timestamptz IS NULL OR (d.dead_at,d.event_id)<($1,$2))
 ORDER BY d.dead_at DESC,d.event_id DESC LIMIT $3`, at, after, limit+1)
	if err != nil {
		return DeadPage{}, err
	}
	defer rows.Close()
	page := DeadPage{Items: make([]DeadEvent, 0, limit)}
	for rows.Next() {
		var item DeadEvent
		if err := rows.Scan(&item.EventID, &item.CollectionName, &item.RecordID, &item.Operation, &item.RecordVersion, &item.Attempts, &item.CreatedAt, &item.DeadAt); err != nil {
			return DeadPage{}, err
		}
		if len(page.Items) == limit {
			last := page.Items[limit-1]
			page.NextBefore = strconv.FormatInt(last.DeadAt.UnixMicro(), 10) + "_" + last.EventID.String()
			break
		}
		page.Items = append(page.Items, item)
	}
	return page, rows.Err()
}

// Retry revives only a dead delivery. Already delivered mutations cannot be
// duplicated by an operator retry; a retry uses the original event identity.
func (s *Service) Retry(ctx context.Context, id uuid.UUID) error {
	if id == uuid.Nil {
		return ErrCursorInvalid
	}
	_, err := s.pool.Exec(ctx, `UPDATE pgbase_meta.outbox_delivery SET attempts=0,dead_at=NULL,lease_token=NULL,lease_until=NULL,available_at=clock_timestamp()
 WHERE event_id=$1 AND dead_at IS NOT NULL AND delivered_at IS NULL`, id)
	return err
}
