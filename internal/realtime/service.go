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
	"github.com/yakisoba0728/PgBase/internal/recordidentity"
	"github.com/yakisoba0728/PgBase/internal/schema"
)

var ErrCursorExpired = errors.New("realtime cursor expired")
var ErrCursorInvalid = errors.New("realtime cursor invalid")
var ErrRelationFilterRequiresCurrentRecord = errors.New("realtime relation filter requires current record")

const batchSize = 100

const publicIdentityLookup = `SELECT record_id FROM pgbase_meta.record_outbox WHERE collection_id=$1 AND envelope->'publicRecord'->>'id'=$2 ORDER BY created_at DESC LIMIT 1`

type Service struct {
	pool    *database.Pool
	logger  *slog.Logger
	schemas *schema.Service
	ids     *recordidentity.Service
}

func NewService(pool *database.Pool, logger *slog.Logger, schemas ...*schema.Service) *Service {
	if logger == nil {
		logger = slog.Default()
	}
	service := &Service{pool: pool, logger: logger, ids: recordidentity.New(nil)}
	if len(schemas) > 0 {
		service.schemas = schemas[0]
	}
	return service
}

type Event struct {
	Sequence   int64          `json:"-"`
	EventID    uuid.UUID      `json:"eventId"`
	Collection string         `json:"collection"`
	RecordID   uuid.UUID      `json:"-"`
	PublicID   string         `json:"recordId"`
	Operation  string         `json:"operation"`
	Version    int64          `json:"version"`
	Action     string         `json:"action,omitempty"`
	Record     map[string]any `json:"record,omitempty"`
}

type ReadRequest struct {
	Principal   identity.Subject
	Collection  string
	RecordID    uuid.UUID
	PublicID    string
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
	if !request.Principal.Valid() {
		return nil, 0, recordaccess.ErrDenied
	}
	var events []Event
	var cursor int64
	err := s.WithReplaySnapshot(ctx, func(read ReplayLookup) error {
		var err error
		events, cursor, err = read(request)
		return err
	})
	if err != nil {
		return nil, 0, err
	}
	return events, cursor, nil
}

type ReplayLookup func(ReadRequest) ([]Event, int64, error)

// WithReplaySnapshot shares catalog and cursor bounds for one delivery pass.
// The lookup must only be used synchronously during the callback.
func (s *Service) WithReplaySnapshot(ctx context.Context, visit func(ReplayLookup) error) error {
	if s == nil || s.schemas == nil {
		return recordaccess.ErrDenied
	}
	// Schema and policy row locks write lock WAL. This transaction changes no
	// data, so releasing it does not need to wait for the WAL flush.
	options := pgx.TxOptions{BeginQuery: "begin isolation level repeatable read; set local synchronous_commit = off"}
	return s.pool.InTx(ctx, options, func(tx pgx.Tx) error {
		return s.replayInTx(ctx, tx, visit)
	})
}

func (s *Service) replayInTx(ctx context.Context, tx pgx.Tx, visit func(ReplayLookup) error) error {
	catalog, err := s.schemas.EnsureCurrent(ctx, tx)
	if err != nil {
		return err
	}
	snapshot := &replaySnapshot{service: s, tx: tx, catalog: catalog}
	if err := tx.QueryRow(ctx, `SELECT floor_sequence,GREATEST(floor_sequence,COALESCE((SELECT max(sequence) FROM pgbase_meta.realtime_events),0)) FROM pgbase_meta.realtime_retention WHERE singleton`).Scan(&snapshot.floor, &snapshot.head); err != nil {
		return err
	}
	return visit(func(request ReadRequest) ([]Event, int64, error) {
		return snapshot.read(ctx, request)
	})
}

// replaySnapshot is one delivery pass's repeatable-read transaction. Topics
// of the same collection share their admission check, rules and identity
// resolution: the admission and rule rows are held FOR SHARE until commit and
// every other read uses the same snapshot, so a repeated lookup in this
// transaction can only return the result already observed. Only successful
// lookups are reused; every pass begins with empty caches.
type replaySnapshot struct {
	service     *Service
	tx          pgx.Tx
	catalog     schema.Catalog
	floor, head int64

	admitted  map[replayAdmissionKey]bool
	rules     map[uuid.UUID]replayRules
	recordIDs map[replayRecordKey]uuid.UUID

	// alwaysJoinRecord restores the unconditional acl_record join. Tests use it
	// to compare the gated statement with the original one.
	alwaysJoinRecord bool
}

type replayAdmissionKey struct {
	collection uuid.UUID
	principal  identity.Subject
}

type replayRecordKey struct {
	collection uuid.UUID
	public     string
}

type replayRules struct {
	rules    accessrule.Rules
	revision int64
}

func (r *replaySnapshot) authorize(ctx context.Context, collection uuid.UUID, principal identity.Subject) error {
	key := replayAdmissionKey{collection: collection, principal: principal}
	if r.admitted[key] {
		return nil
	}
	if err := recordaccess.AuthorizeSubject(ctx, r.tx, collection, principal, "list"); err != nil {
		return err
	}
	if r.admitted == nil {
		r.admitted = make(map[replayAdmissionKey]bool)
	}
	r.admitted[key] = true
	return nil
}

func (r *replaySnapshot) loadRules(ctx context.Context, collection uuid.UUID) (accessrule.Rules, int64, error) {
	if loaded, ok := r.rules[collection]; ok {
		return loaded.rules, loaded.revision, nil
	}
	rules, revision, err := recordaccess.LoadRules(ctx, r.tx, collection)
	if err != nil {
		return accessrule.Rules{}, 0, err
	}
	if r.rules == nil {
		r.rules = make(map[uuid.UUID]replayRules)
	}
	r.rules[collection] = replayRules{rules: rules, revision: revision}
	return rules, revision, nil
}

func (r *replaySnapshot) resolve(ctx context.Context, collection schema.Collection, public string) (uuid.UUID, error) {
	key := replayRecordKey{collection: collection.ID, public: public}
	if id, ok := r.recordIDs[key]; ok {
		return id, nil
	}
	id, err := r.service.ids.Resolve(ctx, r.tx, collection, public)
	if errors.Is(err, recordidentity.ErrNotFound) {
		err = r.tx.QueryRow(ctx, publicIdentityLookup, collection.ID, public).Scan(&id)
		if errors.Is(err, pgx.ErrNoRows) {
			err = recordidentity.ErrNotFound
		}
	}
	if err != nil {
		return uuid.Nil, err
	}
	if r.recordIDs == nil {
		r.recordIDs = make(map[replayRecordKey]uuid.UUID)
	}
	r.recordIDs[key] = id
	return id, nil
}

func (r *replaySnapshot) read(ctx context.Context, request ReadRequest) ([]Event, int64, error) {
	s, tx, catalog, floor, head := r.service, r.tx, r.catalog, r.floor, r.head
	if !request.Principal.Valid() {
		return nil, 0, recordaccess.ErrDenied
	}
	events := make([]Event, 0, batchSize)
	collection, ok := catalog.Collections[request.Collection]
	if !ok {
		return nil, 0, recordaccess.ErrDenied
	}
	if err := r.authorize(ctx, collection.ID, request.Principal); err != nil {
		return nil, 0, err
	}
	cursor := head
	if request.After != nil {
		cursor = *request.After
	}
	// The event query is prepared even when the cursor is already at the head:
	// its rule load, identity resolution and option validation are how an idle
	// subscription observes a schema or policy change on the next poll.
	statement, args, err := r.eventQuery(ctx, request, collection, cursor)
	if err != nil {
		return nil, 0, err
	}
	if request.After == nil {
		return events, cursor, nil
	}
	if cursor < floor {
		return nil, 0, ErrCursorExpired
	}
	if cursor > head || cursor < 0 {
		return nil, 0, ErrCursorInvalid
	}
	if cursor == head {
		return events, cursor, nil
	}
	rows, err := tx.Query(ctx, statement, args...)
	if err != nil {
		return nil, 0, err
	}
	type pendingEvent struct {
		event        Event
		snapshot     []byte
		publicRecord []byte
		relations    []byte
	}
	pending := make([]pendingEvent, 0, batchSize)
	for rows.Next() {
		var event Event
		var snapshot, publicRecord, relations []byte
		if err := rows.Scan(&event.Sequence, &event.EventID, &event.Collection, &event.RecordID, &event.Operation, &event.Version, &snapshot, &publicRecord, &relations); err != nil {
			rows.Close()
			return nil, 0, err
		}
		pending = append(pending, pendingEvent{event: event, snapshot: snapshot, publicRecord: publicRecord, relations: relations})
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, 0, err
	}
	rows.Close()
	relationSnapshots := make([][]byte, 0, len(pending))
	for _, item := range pending {
		event, snapshot := item.event, item.snapshot
		event.Action = event.Operation
		if len(item.publicRecord) > 0 && string(item.publicRecord) != "null" {
			event.Record, err = publicSnapshot(collection, item.publicRecord)
			if err == nil {
				event.PublicID, _ = event.Record["id"].(string)
			}
			if err == nil && event.PublicID == "" {
				err = errors.New("realtime public record identity missing")
			}
		} else {
			event.Record, err = publicSnapshot(collection, snapshot)
			if err == nil {
				err = s.ids.Project(ctx, tx, catalog, collection, event.Record)
			}
			if err == nil {
				event.PublicID, err = s.ids.Public(ctx, tx, collection, event.RecordID)
			}
		}
		if err != nil {
			return nil, 0, err
		}
		events = append(events, event)
		relationSnapshots = append(relationSnapshots, item.relations)
		cursor = event.Sequence
	}
	if err := filterEventRelations(ctx, tx, catalog, collection, request, events, relationSnapshots); err != nil {
		return nil, 0, err
	}
	if len(events) < batchSize {
		cursor = head
	}
	return events, cursor, nil
}

// AllowsApplicationBroadcast permits payloads without a record envelope only
// when the current collection list and view rules are explicitly public.
func (s *Service) AllowsApplicationBroadcast(ctx context.Context, request ReadRequest) (bool, error) {
	if s == nil || s.schemas == nil || !request.Principal.Valid() {
		return false, recordaccess.ErrDenied
	}
	allowed := false
	err := s.pool.InTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead}, func(tx pgx.Tx) error {
		catalog, err := s.schemas.EnsureCurrent(ctx, tx)
		if err != nil {
			return err
		}
		collection, ok := catalog.Collections[request.Collection]
		if !ok {
			return recordaccess.ErrDenied
		}
		if err := recordaccess.AuthorizeSubject(ctx, tx, collection.ID, request.Principal, "list"); err != nil {
			return err
		}
		rules, revision, err := recordaccess.LoadRules(ctx, tx, collection.ID)
		if err != nil {
			return err
		}
		allowed = revision != 0 && applicationBroadcastRulePublic(rules.List) && applicationBroadcastRulePublic(rules.View)
		return nil
	})
	return allowed, err
}

func applicationBroadcastRulePublic(rule accessrule.Rule) bool {
	return rule.Op == "allow"
}

func (r *replaySnapshot) eventQuery(ctx context.Context, request ReadRequest, collection schema.Collection, cursor int64) (string, []any, error) {
	catalog := r.catalog
	rules, revision, err := r.loadRules(ctx, collection.ID)
	if err != nil {
		return "", nil, err
	}
	if request.PublicID != "" {
		request.RecordID, err = r.resolve(ctx, collection, request.PublicID)
		if err != nil {
			return "", nil, err
		}
	}
	if err := validateEventFields(collection, request.Principal, request.Fields); err != nil {
		return "", nil, err
	}
	predicate, args := "false", []any{}
	if request.Principal.Kind == identity.Administrator {
		predicate = "true"
	} else if revision == 0 && request.Principal.Kind == identity.User {
		predicate, args = "event.owner_user_id=$3", []any{request.Principal.ID}
	} else if revision != 0 {
		rule := accessrule.Rule{Op: "all", Args: []accessrule.Rule{rules.List, rules.View}}
		predicate, args, err = accessrule.CompilePolicy(rule, accessrule.PolicyContext{
			Catalog: catalog, Collection: collection, Alias: aclRecordAlias, Principal: request.Principal,
			Request: request.RuleContext, ArgOffset: 2, EventAlias: "event",
		})
		if err != nil {
			return "", nil, err
		}
	}
	if request.Filter != "" {
		if err := query.ValidateFilterFields(catalog, collection, request.Principal, request.Filter); err != nil {
			return "", nil, err
		}
		filterSQL, filterArgs, compileErr := query.CompileRecordPredicate(collection, request.Filter, aclRecordAlias, 2+len(args))
		if compileErr != nil {
			if relationFilterUnsupported(compileErr) {
				return "", nil, ErrRelationFilterRequiresCurrentRecord
			}
			return "", nil, compileErr
		}
		predicate = "(" + predicate + ") AND (" + filterSQL + ")"
		args = append(args, filterArgs...)
	}
	statement := `SELECT event.sequence,event.event_id,event.collection_name,event.record_id,event.operation,event.record_version,outbox.record_snapshot,outbox.envelope->'publicRecord',outbox.envelope->'relations'
 FROM pgbase_meta.realtime_events event JOIN pgbase_meta.record_outbox outbox ON outbox.event_id=event.event_id`
	if r.alwaysJoinRecord || referencesACLRecord(predicate) {
		table := pgx.Identifier{"app", collection.TableName}.Sanitize()
		statement += `
 CROSS JOIN LATERAL jsonb_populate_record(NULL::` + table + `,COALESCE((SELECT to_jsonb(current_record) FROM ` + table + ` current_record WHERE current_record.id=event.record_id),outbox.record_snapshot)) ` + aclRecordAlias
	}
	statement += `
 WHERE event.collection_id=$1 AND event.sequence>$2`
	base := []any{collection.ID, cursor}
	if request.PublicID != "" {
		offset := len(base) + len(args) + 1
		statement += ` AND (outbox.envelope->'publicRecord'->>'id'=$` + fmt.Sprint(offset) + ` OR (NOT (outbox.envelope ? 'publicRecord') AND event.record_id=$` + fmt.Sprint(offset+1) + `))`
		args = append(args, request.PublicID, request.RecordID)
	} else if request.RecordID != uuid.Nil {
		statement += ` AND event.record_id=$` + fmt.Sprint(len(base)+len(args)+1)
		args = append(args, request.RecordID)
	}
	statement += ` AND (` + predicate + `) ORDER BY event.sequence LIMIT 100`
	return statement, append(base, args...), nil
}

// aclRecordAlias names the current row, or the event snapshot after deletion,
// as seen by compiled rules and filters.
const aclRecordAlias = "acl_record"

// referencesACLRecord reports whether compiled SQL may read the acl_record
// row. It is deliberately a plain substring test: any occurrence, quoted,
// qualified or not, keeps the join. Values are always bound as parameters, so
// user input cannot add or remove an occurrence; a false positive only costs
// the join that every event query used to perform. Without a reference the
// one-row-per-event join cannot change which events match or their order.
func referencesACLRecord(sql string) bool {
	return strings.Contains(sql, aclRecordAlias)
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
		if !field.Hidden && field.Type != schema.TypePassword {
			if value, ok := physical[field.ColumnName]; ok {
				result[field.Name] = value
			}
		}
	}
	return result, nil
}
