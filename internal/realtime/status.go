package realtime

import (
	"context"
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
