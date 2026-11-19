package realtime

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

var errLeaseLost = errors.New("outbox lease lost")

const (
	deliveryLock         int64 = 704712092
	retentionBatchSize         = 1000
	retentionPassTimeout       = time.Second
)

// Both discovery and predecessor ordering are bounded by the pending delivery
// queue, never lifetime outbox history. The database enqueues at outbox commit;
// a dead or leased earlier version still blocks a later version of that record.
const claimPendingQuery = `WITH pending AS (
 SELECT d.event_id FROM pgbase_meta.outbox_delivery d
 WHERE d.delivered_at IS NULL AND d.dead_at IS NULL
 AND d.available_at<=clock_timestamp() AND (d.lease_until IS NULL OR d.lease_until<clock_timestamp())
 AND NOT EXISTS (SELECT 1 FROM pgbase_meta.outbox_delivery prior
  WHERE prior.collection_id=d.collection_id AND prior.record_id=d.record_id
  AND prior.record_version<d.record_version AND prior.delivered_at IS NULL)
 ORDER BY d.available_at,d.event_id FOR UPDATE OF d SKIP LOCKED LIMIT 100
) UPDATE pgbase_meta.outbox_delivery d SET lease_token=$1,lease_until=clock_timestamp()+interval '30 seconds',
 attempts=attempts+1 FROM pending WHERE d.event_id=pending.event_id RETURNING d.event_id`

type claim struct{ id, token uuid.UUID }

// Run retries transient database failures; each failed lease is eventually
// reclaimed after expiry. Payloads and database errors never enter logs.
func (s *Service) Run(ctx context.Context) error {
	ticker := time.NewTicker(250 * time.Millisecond)
	defer ticker.Stop()
	maintenance := time.NewTicker(time.Minute)
	defer maintenance.Stop()
	prunePending := true
	for {
		prunePending = s.runPass(ctx, prunePending)
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
		case <-maintenance.C:
			prunePending = true
		}
	}
}

// A backlog gets one bounded cleanup transaction per delivery poll. This keeps
// delivery moving during catch-up without limiting retention to one batch per
// minute or creating an unbounded maintenance loop. Errors wait for the next
// maintenance tick; a full batch continues on the next ordinary poll.
func (s *Service) runPass(ctx context.Context, prunePending bool) bool {
	if err := s.Deliver(ctx); err != nil && ctx.Err() == nil {
		s.logger.WarnContext(ctx, "outbox delivery deferred")
	}
	if !prunePending || ctx.Err() != nil {
		return false
	}
	deleted, err := s.pruneBatch(ctx, 24*time.Hour)
	if err != nil {
		if ctx.Err() == nil {
			s.logger.WarnContext(ctx, "realtime retention deferred")
		}
		return false
	}
	return deleted == retentionBatchSize
}

func (s *Service) Deliver(ctx context.Context) error {
	claims, err := s.claim(ctx)
	if err != nil {
		return err
	}
	if len(claims) > 1 {
		if err := s.publishBatch(ctx, claims); err == nil {
			return nil
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
		// A bad event should retain its own retry state without blocking the rest.
	}
	var result error
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
			result = errors.Join(result, err, retryErr)
		}
	}
	return result
}

func (s *Service) claim(ctx context.Context) ([]claim, error) {
	result := make([]claim, 0, batchSize)
	err := s.pool.InTx(ctx, pgx.TxOptions{}, func(tx pgx.Tx) error {
		token := uuid.New()
		rows, err := tx.Query(ctx, claimPendingQuery, token)
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

// publishBatch holds the same sequence lock through one durable commit for
// a claim pass. A claim contains at most one version of any record.
func (s *Service) publishBatch(ctx context.Context, claims []claim) error {
	ids := make([]uuid.UUID, len(claims))
	for i, item := range claims {
		ids[i] = item.id
	}
	return s.pool.InTx(ctx, pgx.TxOptions{}, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock($1)`, deliveryLock); err != nil {
			return err
		}
		_, err := tx.Exec(ctx, `WITH leased AS MATERIALIZED (
		 SELECT d.event_id, claimed.position
		 FROM unnest($1::uuid[]) WITH ORDINALITY AS claimed(event_id,position)
		 JOIN pgbase_meta.outbox_delivery d USING (event_id)
		 WHERE d.lease_token=$2 AND d.lease_until>clock_timestamp() AND d.delivered_at IS NULL
		 FOR UPDATE OF d
		), published AS (
		 INSERT INTO pgbase_meta.realtime_events(event_id,owner_user_id,collection_id,collection_name,record_id,operation,record_version,delivered_at)
		 SELECT o.event_id,o.owner_user_id,o.collection_id,o.collection_name,o.record_id,o.operation,o.record_version,clock_timestamp()
		 FROM leased l JOIN pgbase_meta.record_outbox o USING (event_id) ORDER BY l.position
		 ON CONFLICT (event_id) DO NOTHING
		)
		UPDATE pgbase_meta.outbox_delivery d SET delivered_at=clock_timestamp(),lease_token=NULL,lease_until=NULL
		FROM leased l WHERE d.event_id=l.event_id`, ids, claims[0].token)
		return err
	})
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
	_, err := s.pruneBatch(ctx, retention)
	return err
}

func (s *Service) pruneBatch(ctx context.Context, retention time.Duration) (int64, error) {
	if retention < time.Hour {
		return 0, ErrCursorInvalid
	}
	ctx, cancel := context.WithTimeout(ctx, retentionPassTimeout)
	defer cancel()
	var count int64
	err := s.pool.InTx(ctx, pgx.TxOptions{}, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock($1)`, deliveryLock); err != nil {
			return err
		}
		// Inspect only the leading bounded sequence window, even when every
		// retained event is fresh. A clock correction can make timestamps
		// non-monotonic; stop at the first unexpired sequence so the global
		// replay floor never crosses a retained event.
		return tx.QueryRow(ctx, `WITH candidates AS MATERIALIZED (
   SELECT sequence,delivered_at>=statement_timestamp()-make_interval(secs=>$1) AS keep
   FROM pgbase_meta.realtime_events ORDER BY sequence LIMIT $2
  ), boundary AS (SELECT min(sequence) FILTER (WHERE keep) AS first_retained FROM candidates),
  deleted AS (
   DELETE FROM pgbase_meta.realtime_events events USING candidates,boundary
   WHERE events.sequence=candidates.sequence AND NOT candidates.keep
   AND (boundary.first_retained IS NULL OR candidates.sequence<boundary.first_retained)
   RETURNING events.sequence
  ) UPDATE pgbase_meta.realtime_retention
  SET floor_sequence=GREATEST(floor_sequence,COALESCE((SELECT max(sequence) FROM deleted),0))
  WHERE singleton RETURNING (SELECT count(*) FROM deleted)`, retention.Seconds(), retentionBatchSize).Scan(&count)
	})
	return count, err
}
