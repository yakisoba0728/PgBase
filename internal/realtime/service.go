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
	"strings"

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
var ErrRelationFilterRequiresCurrentRecord = errors.New("realtime relation filter requires current record")

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
	Action     string         `json:"action,omitempty"`
	Record     map[string]any `json:"record,omitempty"`
}

type ReadRequest struct {
	Principal   identity.Subject
	Collection  string
	RecordID    uuid.UUID
	Filter      string
	Fields      string
	After       *int64
	RuleContext *accessrule.RequestContext
}

// Read checks current policy and active status for every batch. after=nil
// starts at the current head. An expired cursor fails explicitly: silently
// dropping events would leave clients with an incorrect local replica.
func (s *Service) Read(ctx context.Context, userID uuid.UUID, collection string, after *int64) ([]Event, int64, error) {
	events, cursor, err := s.ReadFor(ctx, ReadRequest{Principal: identity.Subject{Kind: identity.User, ID: userID}, Collection: collection, After: after})
	for index := range events {
		events[index].Action = ""
		events[index].Record = nil
	}
	return events, cursor, err
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
		statement, args, collection, err := s.eventQuery(ctx, tx, request, collectionID, head)
		if err != nil {
			return err
		}
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
		statement, args, collection, err = s.eventQuery(ctx, tx, request, collectionID, cursor)
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

// AllowsApplicationBroadcast permits payloads without a record envelope only
// when the current collection list and view rules are explicitly public.
func (s *Service) AllowsApplicationBroadcast(ctx context.Context, request ReadRequest) (bool, error) {
	allowed := false
	err := s.pool.InTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead}, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `SELECT 1 FROM pgbase_meta.schema_state WHERE singleton FOR SHARE`); err != nil {
			return err
		}
		var collectionID uuid.UUID
		if err := tx.QueryRow(ctx, `SELECT id FROM pgbase_meta.collections WHERE name=$1`, request.Collection).Scan(&collectionID); err != nil {
			return err
		}
		rules, revision, err := recordaccess.LoadRules(ctx, tx, collectionID)
		if err != nil {
			return err
		}
		allowed = revision != 0 && applicationBroadcastRulePublic(rules.List) && applicationBroadcastRulePublic(rules.View)
		return nil
	})
	return allowed, err
}

func applicationBroadcastRulePublic(rule accessrule.Rule) bool {
	if rule.Op == "allow" {
		return true
	}
	return rule.PocketBase != nil && strings.TrimSpace(*rule.PocketBase) == ""
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
	if err := validateEventFields(collection, request.Principal, request.Fields); err != nil {
		return "", nil, schema.Collection{}, err
	}
	predicate, args := "false", []any{}
	if request.Principal.Kind == identity.Administrator {
		predicate = "true"
	} else if revision == 0 && request.Principal.Kind == identity.User {
		predicate, args = "event.owner_user_id=$3", []any{request.Principal.ID}
	} else if revision != 0 {
		rule := accessrule.Rule{Op: "all", Args: []accessrule.Rule{rules.List, rules.View}}
		if rules.List.PocketBase != nil || rules.View.PocketBase != nil {
			resolver, resolveErr := accessrule.NewCatalogResolver(catalog, collection, "acl_record")
			if resolveErr != nil {
				return "", nil, schema.Collection{}, resolveErr
			}
			predicate, args, err = accessrule.CompilePocketBase(rule, accessrule.PBCompileContext{Resolver: resolver, Principal: request.Principal, Request: request.RuleContext, ArgOffset: 2})
		} else {
			predicate, args, err = accessrule.CompileEvent(rule, collection, "acl_record", request.Principal, 2, "event")
		}
		if err != nil {
			return "", nil, schema.Collection{}, err
		}
	}
	if request.Filter != "" {
		filterSQL, filterArgs, compileErr := query.CompileRecordPredicate(collection, request.Filter, "acl_record", 2+len(args))
		if compileErr != nil {
			if relationFilterUnsupported(compileErr) {
				return "", nil, schema.Collection{}, ErrRelationFilterRequiresCurrentRecord
			}
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

func validateEventFields(collection schema.Collection, principal identity.Subject, requested string) error {
	if requested == "" {
		return nil
	}
	projection, err := query.CompileProjection(requested)
	if err != nil {
		return err
	}
	available := make(map[string]schema.Field, len(collection.Fields))
	for _, field := range collection.Fields {
		available[field.Name] = field
	}
	for _, root := range projection.RootFields() {
		switch root {
		case "*", "expand", "id", "collectionId", "collectionName", "created", "updated", "created_at", "updated_at":
			continue
		}
		field, ok := available[root]
		if !ok {
			return queryFieldValidation("unknown_field")
		}
		if field.Hidden && principal.Kind != identity.Administrator {
			return queryFieldValidation("hidden_field")
		}
	}
	return nil
}

func queryFieldValidation(code string) error {
	return &query.ValidationError{Details: []query.ValidationDetail{{Field: "fields", Code: code}}}
}

func relationFilterUnsupported(err error) bool {
	var validationError *query.ValidationError
	if !errors.As(err, &validationError) {
		return false
	}
	for _, detail := range validationError.Details {
		if detail.Field == "filter" && detail.Code == "relation_filter_unsupported" {
			return true
		}
	}
	return false
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
