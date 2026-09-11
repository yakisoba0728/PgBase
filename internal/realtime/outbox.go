package realtime

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

var errLeaseLost = errors.New("outbox lease lost")

const deliveryLock int64 = 704712092

type claim struct{ id, token uuid.UUID }

// Run retries transient database failures; each failed lease is eventually
// reclaimed after expiry. Payloads and database errors never enter logs.
func (s *Service) Run(ctx context.Context) error {
	ticker := time.NewTicker(250 * time.Millisecond)
	defer ticker.Stop()
	maintenance := time.NewTicker(time.Minute)
	defer maintenance.Stop()
	for {
		if err := s.Deliver(ctx); err != nil && ctx.Err() == nil {
			s.logger.WarnContext(ctx, "outbox delivery deferred")
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
		case <-maintenance.C:
			if err := s.Prune(ctx, 24*time.Hour); err != nil && ctx.Err() == nil {
				s.logger.WarnContext(ctx, "realtime retention deferred")
			}
		}
	}
}

func (s *Service) Deliver(ctx context.Context) error {
	claims, err := s.claim(ctx)
	if err != nil {
		return err
	}
	for _, item := range claims {
		if err := s.publish(ctx, item); err != nil && !errors.Is(err, errLeaseLost) {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			_, retryErr := s.pool.Exec(ctx, `UPDATE pgbase_meta.outbox_delivery SET
    lease_token=NULL, lease_until=NULL,
    available_at=clock_timestamp()+make_interval(secs=>LEAST(300,power(2,LEAST(attempts,8))::integer)),
    dead_at=CASE WHEN attempts>=10 THEN clock_timestamp() ELSE NULL END
    WHERE event_id=$1 AND lease_token=$2 AND delivered_at IS NULL`, item.id, item.token)
			return errors.Join(err, retryErr)
		}
	}
	return nil
}

func (s *Service) claim(ctx context.Context) ([]claim, error) {
	result := make([]claim, 0, batchSize)
	err := s.pool.InTx(ctx, pgx.TxOptions{}, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `INSERT INTO pgbase_meta.outbox_delivery(event_id)
   SELECT o.event_id FROM pgbase_meta.record_outbox o WHERE NOT EXISTS
   (SELECT 1 FROM pgbase_meta.outbox_delivery d WHERE d.event_id=o.event_id)
   ORDER BY o.created_at,o.event_id LIMIT 100 ON CONFLICT DO NOTHING`); err != nil {
			return err
		}
		token := uuid.New()
		rows, err := tx.Query(ctx, `WITH pending AS (
			SELECT d.event_id FROM pgbase_meta.outbox_delivery d JOIN pgbase_meta.record_outbox o USING(event_id)
			WHERE d.delivered_at IS NULL AND d.dead_at IS NULL
			AND d.available_at<=clock_timestamp() AND (d.lease_until IS NULL OR d.lease_until<clock_timestamp())
			AND NOT EXISTS (SELECT 1 FROM pgbase_meta.record_outbox prior
			 LEFT JOIN pgbase_meta.outbox_delivery delivery ON delivery.event_id=prior.event_id
			 WHERE prior.collection_id=o.collection_id AND prior.record_id=o.record_id
			 AND prior.record_version<o.record_version AND delivery.delivered_at IS NULL)
			ORDER BY d.available_at,d.event_id FOR UPDATE OF d SKIP LOCKED LIMIT 100
  ) UPDATE pgbase_meta.outbox_delivery d SET lease_token=$1,lease_until=clock_timestamp()+interval '30 seconds',
   attempts=attempts+1 FROM pending WHERE d.event_id=pending.event_id RETURNING d.event_id`, token)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var id uuid.UUID
			if err := rows.Scan(&id); err != nil {
				return err
			}
			result = append(result, claim{id, token})
		}
		return rows.Err()
	})
	return result, err
}

func (s *Service) publish(ctx context.Context, item claim) error {
	return s.pool.InTx(ctx, pgx.TxOptions{}, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock($1)`, deliveryLock); err != nil {
			return err
		}
		var valid bool
		err := tx.QueryRow(ctx, `SELECT lease_token=$2 AND lease_until>clock_timestamp() AND delivered_at IS NULL
   FROM pgbase_meta.outbox_delivery WHERE event_id=$1 FOR UPDATE`, item.id, item.token).Scan(&valid)
		if err != nil {
			return err
		}
		if !valid {
			return errLeaseLost
		}
		if _, err := tx.Exec(ctx, `INSERT INTO pgbase_meta.realtime_events(event_id,owner_user_id,collection_id,
			collection_name,record_id,operation,record_version,delivered_at)
			SELECT event_id,owner_user_id,collection_id,collection_name,record_id,operation,record_version,clock_timestamp()
   FROM pgbase_meta.record_outbox WHERE event_id=$1 ON CONFLICT (event_id) DO NOTHING`, item.id); err != nil {
			return err
		}
		_, err = tx.Exec(ctx, `UPDATE pgbase_meta.outbox_delivery SET delivered_at=clock_timestamp(),lease_token=NULL,lease_until=NULL WHERE event_id=$1`, item.id)
		return err
	})
}

// Prune removes a bounded committed prefix older than the retention window.
func (s *Service) Prune(ctx context.Context, retention time.Duration) error {
	if retention < time.Hour {
		return ErrCursorInvalid
	}
	return s.pool.InTx(ctx, pgx.TxOptions{}, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock($1)`, deliveryLock); err != nil {
			return err
		}
		_, err := tx.Exec(ctx, `WITH doomed AS (
   SELECT sequence FROM pgbase_meta.realtime_events WHERE delivered_at<clock_timestamp()-make_interval(secs=>$1)
   ORDER BY sequence LIMIT 1000
  ), deleted AS (DELETE FROM pgbase_meta.realtime_events WHERE sequence IN (SELECT sequence FROM doomed) RETURNING sequence)
  UPDATE pgbase_meta.realtime_retention SET floor_sequence=GREATEST(floor_sequence,COALESCE((SELECT max(sequence) FROM deleted),0)) WHERE singleton`, retention.Seconds())
		return err
	})
}
