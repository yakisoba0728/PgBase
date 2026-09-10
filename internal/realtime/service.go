// Package realtime delivers committed record mutations and replays them under
// the current collection policy. Cursors follow delivery commit order, never
// UUID or audit insertion order, which can precede a transaction's commit.
package realtime

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/yakisoba0728/PgBase/internal/accessrule"
	"github.com/yakisoba0728/PgBase/internal/database"
	"github.com/yakisoba0728/PgBase/internal/identity"
	"github.com/yakisoba0728/PgBase/internal/query"
	"github.com/yakisoba0728/PgBase/internal/recordaccess"
	"github.com/yakisoba0728/PgBase/internal/schema"
	"github.com/yakisoba0728/PgBase/internal/serviceauth"
)

var ErrCursorExpired = errors.New("realtime cursor expired")
var ErrCursorInvalid = errors.New("realtime cursor invalid")
var errLeaseLost = errors.New("outbox lease lost")

const deliveryLock int64 = 704712092
const batchSize = 100

type Service struct {
	pool    *database.Pool
	logger  *slog.Logger
	schemas *schema.Service
}

func NewService(pool *database.Pool, logger *slog.Logger, schemas ...*schema.Service) *Service {
	if logger == nil {
		logger = slog.Default()
	}
	service := &Service{pool: pool, logger: logger}
	if len(schemas) > 0 {
		service.schemas = schemas[0]
	}
	return service
}

type Event struct {
	Sequence   int64          `json:"-"`
	EventID    uuid.UUID      `json:"eventId"`
	Collection string         `json:"collection"`
	RecordID   uuid.UUID      `json:"recordId"`
	Operation  string         `json:"operation"`
	Version    int64          `json:"version"`
	Action     string         `json:"action"`
	Record     map[string]any `json:"record"`
}

type ReadRequest struct {
	Principal  identity.Subject
	Collection string
	RecordID   uuid.UUID
	Filter     string
	Fields     string
	After      *int64
}

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
			// Update only the lease we own. A slow worker cannot release a new claim.
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
		// Repeated discovery also finds old transactions that committed after a
		// previous scan. ON CONFLICT makes concurrent instances harmless.
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
		// Serialize allocation through commit. A replay cursor cannot jump past an
		// uncommitted lower sequence, even with many instances or late producers.
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

// Read checks current policy and active status for every batch. after=nil
// starts at the current head. An expired cursor fails explicitly: silently
// dropping events would leave clients with an incorrect local replica.
func (s *Service) Read(ctx context.Context, userID uuid.UUID, collection string, after *int64) ([]Event, int64, error) {
	return s.ReadFor(ctx, ReadRequest{Principal: identity.Subject{Kind: identity.User, ID: userID}, Collection: collection, After: after})
}

func (s *Service) ReadFor(ctx context.Context, request ReadRequest) ([]Event, int64, error) {
	events := make([]Event, 0, batchSize)
	var cursor int64
	err := s.pool.InTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead}, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `SELECT 1 FROM pgbase_meta.schema_state WHERE singleton FOR SHARE`); err != nil {
			return err
		}
		var collectionID uuid.UUID
		if err := tx.QueryRow(ctx, `SELECT id FROM pgbase_meta.collections WHERE name=$1`, request.Collection).Scan(&collectionID); err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return recordaccess.ErrDenied
			}
			return err
		}
		if request.Principal.Kind == identity.User {
			if err := recordaccess.AuthorizeUser(ctx, tx, collectionID, request.Principal.ID); err != nil {
				return err
			}
		} else if request.Principal.Kind == identity.Service {
			if err := serviceauth.Authorize(ctx, tx, request.Principal, collectionID, "read"); err != nil {
				return recordaccess.ErrDenied
			}
		}
		var floor, head int64
		if err := tx.QueryRow(ctx, `SELECT floor_sequence,GREATEST(floor_sequence,COALESCE((SELECT max(sequence) FROM pgbase_meta.realtime_events),0)) FROM pgbase_meta.realtime_retention WHERE singleton`).Scan(&floor, &head); err != nil {
			return err
		}
		cursor = head
		if request.After == nil {
			return nil
		}
		if *request.After < floor {
			return ErrCursorExpired
		}
		if *request.After > head || *request.After < 0 {
			return ErrCursorInvalid
		}
		cursor = *request.After
		statement, args, collection, err := s.eventQuery(ctx, tx, request, collectionID, cursor)
		if err != nil {
			return err
		}
		rows, err := tx.Query(ctx, statement, args...)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var event Event
			var snapshot []byte
			if err := rows.Scan(&event.Sequence, &event.EventID, &event.Collection, &event.RecordID, &event.Operation, &event.Version, &snapshot); err != nil {
				return err
			}
			event.Action = event.Operation
			event.Record, err = publicSnapshot(collection, snapshot)
			if err != nil {
				return err
			}
			events = append(events, event)
			cursor = event.Sequence
		}
		if len(events) < batchSize {
			cursor = head
		}
		return rows.Err()
	})
	return events, cursor, err
}

// Prune removes a bounded committed prefix older than the retention window.
// The floor persists even when every event is removed.
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

func (s *Service) eventQuery(ctx context.Context, tx pgx.Tx, request ReadRequest, collectionID uuid.UUID, cursor int64) (string, []any, schema.Collection, error) {
	rules, revision, err := recordaccess.LoadRules(ctx, tx, collectionID)
	if err != nil {
		return "", nil, schema.Collection{}, err
	}
	if s.schemas == nil {
		return "", nil, schema.Collection{}, recordaccess.ErrDenied
	}
	catalog, err := s.schemas.EnsureCurrent(ctx, tx)
	if err != nil {
		return "", nil, schema.Collection{}, err
	}
	collection, ok := catalog.Collections[request.Collection]
	if !ok {
		return "", nil, schema.Collection{}, recordaccess.ErrDenied
	}
	predicate, args := "false", []any{}
	if request.Principal.Kind == identity.Administrator {
		predicate = "true"
	} else if revision == 0 && request.Principal.Kind == identity.User {
		predicate, args = "event.owner_user_id=$3", []any{request.Principal.ID}
	} else if revision != 0 {
		rule := accessrule.Rule{Op: "all", Args: []accessrule.Rule{rules.List, rules.View}}
		predicate, args, err = accessrule.CompileEvent(rule, collection, "acl_record", request.Principal, 2, "event")
		if err != nil {
			return "", nil, schema.Collection{}, err
		}
	}
	if request.Filter != "" {
		filterSQL, filterArgs, compileErr := query.CompileRecordPredicate(collection, request.Filter, "acl_record", 2+len(args))
		if compileErr != nil {
			return "", nil, schema.Collection{}, compileErr
		}
		predicate = "(" + predicate + ") AND (" + filterSQL + ")"
		args = append(args, filterArgs...)
	}
	table := pgx.Identifier{"app", collection.TableName}.Sanitize()
	statement := `SELECT event.sequence,event.event_id,event.collection_name,event.record_id,event.operation,event.record_version,outbox.record_snapshot
 FROM pgbase_meta.realtime_events event JOIN pgbase_meta.record_outbox outbox ON outbox.event_id=event.event_id
 CROSS JOIN LATERAL jsonb_populate_record(NULL::` + table + `,COALESCE((SELECT to_jsonb(current_record) FROM ` + table + ` current_record WHERE current_record.id=event.record_id),outbox.record_snapshot)) acl_record
 WHERE event.collection_id=$1 AND event.sequence>$2`
	base := []any{collectionID, cursor}
	if request.RecordID != uuid.Nil {
		statement += ` AND event.record_id=$` + fmt.Sprint(len(base)+len(args)+1)
		args = append(args, request.RecordID)
	}
	statement += ` AND (` + predicate + `) ORDER BY event.sequence LIMIT 100`
	return statement, append(base, args...), collection, nil
}

func publicSnapshot(collection schema.Collection, raw []byte) (map[string]any, error) {
	physical := map[string]any{}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	if err := decoder.Decode(&physical); err != nil {
		return nil, err
	}
	result := map[string]any{"collectionId": collection.ID.String(), "collectionName": collection.Name}
	for _, name := range []string{"id", "created_at", "updated_at"} {
		if value, ok := physical[name]; ok {
			output := name
			if name == "created_at" {
				output = "created"
			}
			if name == "updated_at" {
				output = "updated"
			}
			result[output] = value
		}
	}
	for _, field := range collection.Fields {
		if !field.Hidden {
			if value, ok := physical[field.ColumnName]; ok {
				result[field.Name] = value
			}
		}
	}
	return result, nil
}
