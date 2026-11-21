//go:build integration

package realtime

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/yakisoba0728/PgBase/internal/accessrule"
	"github.com/yakisoba0728/PgBase/internal/identity"
)

type replayQueryTracer struct {
	mu         sync.Mutex
	statements []string
}

func (t *replayQueryTracer) TraceQueryStart(ctx context.Context, _ *pgx.Conn, data pgx.TraceQueryStartData) context.Context {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.statements = append(t.statements, strings.Join(strings.Fields(data.SQL), " "))
	return ctx
}

func (*replayQueryTracer) TraceQueryEnd(context.Context, *pgx.Conn, pgx.TraceQueryEndData) {}

func (t *replayQueryTracer) take() []string {
	t.mu.Lock()
	defer t.mu.Unlock()
	statements := t.statements
	t.statements = nil
	return statements
}

// tracedReplayPass runs one delivery pass exactly as WithReplaySnapshot does,
// on a traced connection, and returns every statement it sent including BEGIN
// and COMMIT. Each statement is one client/server round trip.
func tracedReplayPass(t *testing.T, s *Service, requests []ReadRequest) ([]string, [][]Event) {
	t.Helper()
	ctx := t.Context()
	pooled, err := s.pool.Acquire(ctx)
	if err != nil {
		t.Fatal(err)
	}
	config := pooled.Conn().Config().Copy()
	pooled.Release()
	tracer := &replayQueryTracer{}
	config.Tracer = tracer
	conn, err := pgx.ConnectConfig(ctx, config)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close(context.Background())
	tracer.take()
	tx, err := conn.BeginTx(ctx, pgx.TxOptions{BeginQuery: "begin isolation level repeatable read; set local synchronous_commit = off"})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback(context.Background()) }()
	results := make([][]Event, 0, len(requests))
	if err := s.replayInTx(ctx, tx, func(read ReplayLookup) error {
		for _, request := range requests {
			events, _, err := read(request)
			if err != nil {
				return err
			}
			results = append(results, events)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	return tracer.take(), results
}

func statementKinds(statements []string) []string {
	kinds := make([]string, 0, len(statements))
	for _, statement := range statements {
		lower := strings.ToLower(statement)
		switch {
		case strings.HasPrefix(lower, "begin"):
			kinds = append(kinds, "begin")
		case lower == "commit":
			kinds = append(kinds, "commit")
		case strings.Contains(lower, "from pgbase_meta.schema_state"):
			kinds = append(kinds, "schema")
		case strings.Contains(lower, "from pgbase_meta.realtime_retention"):
			kinds = append(kinds, "head")
		case strings.Contains(lower, "from pgbase_meta.collection_access"):
			kinds = append(kinds, "admission")
		case strings.Contains(lower, "from pgbase_meta.collection_rules"):
			kinds = append(kinds, "rules")
		default:
			kinds = append(kinds, statement)
		}
	}
	return kinds
}

// The idle poll of a delivery pass is the steady-state cost of every open SSE
// connection. Topics on one collection share the transaction's admission,
// rule and identity reads; each poll still takes them once, so a revocation or
// rule change is observed by the next poll exactly as before.
func TestIdleReplayPassRoundTripsAreBounded(t *testing.T) {
	s, user, collection := realtimeFixture(t)
	ctx := t.Context()
	seedEvent(t, s, user, collection)
	if err := s.Deliver(ctx); err != nil {
		t.Fatal(err)
	}
	principal := identity.Subject{Kind: identity.User, ID: user}
	_, head, err := s.ReadFor(ctx, ReadRequest{Principal: principal, Collection: "notes"})
	if err != nil {
		t.Fatal(err)
	}
	// Production publishes the catalog at startup; an idle pass then pins its
	// version with one statement instead of reading the whole catalog.
	if _, err := s.schemas.Catalog(ctx); err != nil {
		t.Fatal(err)
	}
	once := []string{"begin", "schema", "head", "admission", "rules", "commit"}
	record := uuid.NewString()
	for _, tc := range []struct {
		name     string
		requests []ReadRequest
	}{
		{"one topic", []ReadRequest{{Principal: principal, Collection: "notes", After: &head}}},
		{"three topics on one collection", []ReadRequest{
			{Principal: principal, Collection: "notes", After: &head},
			{Principal: principal, Collection: "notes", Filter: "id != '" + uuid.NewString() + "'", After: &head},
			{Principal: principal, Collection: "notes", PublicID: record, After: &head},
		}},
		// collectRealtimePages rereads a topic without its filter when the
		// filter needs the current record; the reread repeats these reads.
		{"repeated topic read", []ReadRequest{
			{Principal: principal, Collection: "notes", PublicID: record, After: &head},
			{Principal: principal, Collection: "notes", PublicID: record, After: &head},
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			statements, _ := tracedReplayPass(t, s, tc.requests)
			if kinds := statementKinds(statements); !slices.Equal(kinds, once) {
				t.Fatalf("idle pass sent %d statements, want %d:\n%s", len(kinds), len(once), strings.Join(statements, "\n"))
			}
		})
	}

	// The shared reads are per collection and per principal, never across them.
	other := uuid.New()
	if _, err := s.pool.Exec(ctx, `INSERT INTO pgbase_meta.users(id,email,password_hash,created_at,updated_at) VALUES($1,'other-idle@example.com','fixture',now(),now())`, other); err != nil {
		t.Fatal(err)
	}
	statements, _ := tracedReplayPass(t, s, []ReadRequest{
		{Principal: principal, Collection: "notes", After: &head},
		{Principal: identity.Subject{Kind: identity.User, ID: other}, Collection: "notes", After: &head},
	})
	want := []string{"begin", "schema", "head", "admission", "rules", "admission", "commit"}
	if kinds := statementKinds(statements); !slices.Equal(kinds, want) {
		t.Fatalf("two principals sent:\n%s", strings.Join(statements, "\n"))
	}
}

// Dropping the acl_record join is only valid when nothing reads acl_record.
// Every rule shape below must return the same events, cursor and order with
// and without the join, across live rows, deleted rows and owners.
func TestEventQueryJoinsCurrentRecordOnlyWhenReferenced(t *testing.T) {
	s, user, collection := realtimeFixture(t)
	ctx := t.Context()
	if _, err := s.pool.Exec(ctx, `ALTER TABLE app.notes ADD COLUMN title text`); err != nil {
		t.Fatal(err)
	}
	if _, err := s.pool.Exec(ctx, `INSERT INTO pgbase_meta.fields(id,collection_id,name,column_name,field_type,required,is_unique,is_indexed,has_default,ordinal,created_at,options_json,hidden)
 VALUES(gen_random_uuid(),$1,'title','title','text',false,false,false,false,0,now(),'{}',false)`, collection); err != nil {
		t.Fatal(err)
	}
	other := uuid.New()
	if _, err := s.pool.Exec(ctx, `INSERT INTO pgbase_meta.users(id,email,password_hash,created_at,updated_at) VALUES($1,'other-join@example.com','fixture',now(),now())`, other); err != nil {
		t.Fatal(err)
	}
	// live: current row decides; nil: the row is gone and the snapshot decides.
	live := func(title string) *string { return &title }
	for _, item := range []struct {
		owner    uuid.UUID
		snapshot string
		current  *string
	}{
		{user, "hidden", live("shown")},
		{user, "shown", live("hidden")},
		{user, "shown", nil},
		{user, "hidden", nil},
		{other, "shown", nil},
		{other, "hidden", live("shown")},
	} {
		seedTitledEvent(t, s, item.owner, collection, item.snapshot, item.current)
	}
	if err := s.Deliver(ctx); err != nil {
		t.Fatal(err)
	}
	setRules := func(rules *accessrule.Rules) {
		t.Helper()
		if rules == nil {
			if _, err := s.pool.Exec(ctx, `DELETE FROM pgbase_meta.collection_rules WHERE collection_id=$1`, collection); err != nil {
				t.Fatal(err)
			}
			return
		}
		raw, err := json.Marshal(rules)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := s.pool.Exec(ctx, `INSERT INTO pgbase_meta.collection_rules(collection_id,rules) VALUES($1,$2::jsonb)
 ON CONFLICT(collection_id) DO UPDATE SET rules=excluded.rules,revision=pgbase_meta.collection_rules.revision+1`, collection, string(raw)); err != nil {
			t.Fatal(err)
		}
	}
	shown, _ := json.Marshal("shown")
	titleRules := accessrule.OwnerRules()
	titleRules.List = accessrule.Rule{Op: "eq", Field: "title", Value: shown, Binding: accessrule.BindingCatalog}
	titleRules.View = titleRules.List
	ownerRules := accessrule.OwnerRules()
	allowRules := accessrule.OwnerRules()
	allowRules.List, allowRules.View = accessrule.Rule{Op: "allow"}, accessrule.Rule{Op: "allow"}
	admin := identity.Subject{Kind: identity.Administrator, ID: uuid.New()}
	member := identity.Subject{Kind: identity.User, ID: user}
	for _, tc := range []struct {
		name      string
		rules     *accessrule.Rules
		principal identity.Subject
		filter    string
		joins     bool
		matches   int
	}{
		{"administrator", nil, admin, "", false, 6},
		{"default owner policy", nil, member, "", false, 4},
		{"explicit owner rules", &ownerRules, member, "", false, 4},
		{"public rules", &allowRules, member, "", false, 6},
		{"record field rule", &titleRules, member, "", true, 4},
		{"administrator record filter", nil, admin, "title = 'shown'", true, 4},
		{"owner policy with record filter", nil, member, "title = 'hidden'", true, 2},
	} {
		t.Run(tc.name, func(t *testing.T) {
			setRules(tc.rules)
			zero := int64(0)
			request := ReadRequest{Principal: tc.principal, Collection: "notes", Filter: tc.filter, After: &zero}
			gated, gatedCursor, statement := readReplayDirect(t, s, request, false)
			joined, joinedCursor, original := readReplayDirect(t, s, request, true)
			if got := strings.Contains(statement, "CROSS JOIN LATERAL"); got != tc.joins {
				t.Fatalf("join=%t want %t:\n%s", got, tc.joins, statement)
			}
			if !strings.Contains(original, "CROSS JOIN LATERAL") {
				t.Fatalf("reference statement lost its join:\n%s", original)
			}
			if gatedCursor != joinedCursor || len(gated) != len(joined) || len(gated) != tc.matches {
				t.Fatalf("gated=%d@%d joined=%d@%d want %d events", len(gated), gatedCursor, len(joined), joinedCursor, tc.matches)
			}
			for index := range gated {
				if gated[index].EventID != joined[index].EventID || gated[index].Sequence != joined[index].Sequence ||
					fmt.Sprint(gated[index].Record) != fmt.Sprint(joined[index].Record) {
					t.Fatalf("event %d differs: gated=%+v joined=%+v", index, gated[index], joined[index])
				}
			}
		})
	}
}

func readReplayDirect(t *testing.T, s *Service, request ReadRequest, alwaysJoin bool) ([]Event, int64, string) {
	t.Helper()
	ctx := t.Context()
	var events []Event
	var cursor int64
	var statement string
	err := s.pool.InTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead}, func(tx pgx.Tx) error {
		catalog, err := s.schemas.EnsureCurrent(ctx, tx)
		if err != nil {
			return err
		}
		snapshot := &replaySnapshot{service: s, tx: tx, catalog: catalog, alwaysJoinRecord: alwaysJoin}
		if err := tx.QueryRow(ctx, `SELECT floor_sequence,GREATEST(floor_sequence,COALESCE((SELECT max(sequence) FROM pgbase_meta.realtime_events),0)) FROM pgbase_meta.realtime_retention WHERE singleton`).Scan(&snapshot.floor, &snapshot.head); err != nil {
			return err
		}
		statement, _, err = snapshot.eventQuery(ctx, request, catalog.Collections[request.Collection], *request.After)
		if err != nil {
			return err
		}
		events, cursor, err = snapshot.read(ctx, request)
		return err
	})
	if err != nil {
		t.Fatalf("%+v: %v", request, err)
	}
	return events, cursor, statement
}

func seedTitledEvent(t *testing.T, s *Service, owner, collection uuid.UUID, snapshotTitle string, currentTitle *string) {
	t.Helper()
	ctx := t.Context()
	event, record := uuid.New(), uuid.New()
	if err := s.pool.InTx(ctx, pgx.TxOptions{}, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `INSERT INTO pgbase_meta.record_audit_events(event_id,actor_kind,actor_id,request_id,collection_id,collection_name,
 record_id,operation,schema_version,schema_digest,new_version,new_etag,changed_fields,input_hash,compatibility,committed_at)
 VALUES($1,'user',$2,'realtime-test',$3,'notes',$4,'create',1,decode(repeat('aa',32),'hex'),1,'etag',ARRAY['title'],decode(repeat('bb',32),'hex'),'missing-idempotency-key',now())`, event, owner, collection, record); err != nil {
			return err
		}
		envelope, _ := json.Marshal(map[string]any{"version": 1, "eventId": event, "collectionId": collection, "collectionName": "notes", "recordId": record, "operation": "create", "recordVersion": 1, "changes": map[string]any{"title": snapshotTitle}})
		snapshot, _ := json.Marshal(map[string]any{"id": record, "title": snapshotTitle})
		if _, err := tx.Exec(ctx, `INSERT INTO pgbase_meta.record_outbox(event_id,topic,collection_id,collection_name,record_id,operation,record_version,envelope,created_at,owner_user_id,record_snapshot)
 VALUES($1,'record.mutation.v1',$2,'notes',$3,'create',1,$4::jsonb,now(),$5,$6::jsonb)`, event, collection, record, string(envelope), owner, string(snapshot)); err != nil {
			return err
		}
		if currentTitle != nil {
			if _, err := tx.Exec(ctx, `INSERT INTO app.notes(id,created_at,updated_at,title) VALUES($1,now(),now(),$2)`, record, *currentTitle); err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}
