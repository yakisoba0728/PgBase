//go:build integration

package recordops

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/webisnoteasy/PgBase/internal/membership"
	"github.com/webisnoteasy/PgBase/internal/schema"
)

type membershipSQLTrace struct {
	mu         sync.Mutex
	enabled    bool
	statements []string
}

func (t *membershipSQLTrace) TraceQueryStart(
	ctx context.Context,
	_ *pgx.Conn,
	data pgx.TraceQueryStartData,
) context.Context {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.enabled {
		t.statements = append(t.statements, strings.Join(strings.Fields(strings.ToLower(data.SQL)), " "))
	}
	return ctx
}

func (*membershipSQLTrace) TraceQueryEnd(context.Context, *pgx.Conn, pgx.TraceQueryEndData) {}

func (t *membershipSQLTrace) start() {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.enabled = true
}

func (t *membershipSQLTrace) mark() int {
	t.mu.Lock()
	defer t.mu.Unlock()
	return len(t.statements)
}

func (t *membershipSQLTrace) statementsFrom(mark int) []string {
	t.mu.Lock()
	defer t.mu.Unlock()
	return append([]string(nil), t.statements[mark:]...)
}

type membershipTraceCount struct {
	total      int
	membership int
}

func TestReleaseAInjectedRuntimeMembershipQueryOverhead(t *testing.T) {
	tests := []struct {
		state  membership.State
		create membershipTraceCount
		update membershipTraceCount
		delete membershipTraceCount
	}{
		{state: membership.StateOff,
			create: membershipTraceCount{total: 3, membership: 1},
			update: membershipTraceCount{total: 4, membership: 2},
			delete: membershipTraceCount{total: 4, membership: 2}},
		{state: membership.StateDualWrite,
			create: membershipTraceCount{total: 5, membership: 3},
			update: membershipTraceCount{total: 4, membership: 2},
			delete: membershipTraceCount{total: 7, membership: 5}},
		{state: membership.StateReady,
			create: membershipTraceCount{total: 5, membership: 3},
			update: membershipTraceCount{total: 4, membership: 2},
			delete: membershipTraceCount{total: 7, membership: 5}},
	}
	for _, test := range tests {
		t.Run(string(test.state), func(t *testing.T) {
			databaseURL, pool, schemas, _, principal := newRecordOperationsTestWithURL(t)
			ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			defer cancel()
			runtime, err := membership.RegisterWriter(ctx, membership.New(pool), membership.WriterA)
			if err != nil {
				t.Fatal(err)
			}
			records := NewServiceWithMembershipRuntime(
				pool, schemas, []byte("0123456789abcdef0123456789abcdef"), nil, nil, runtime,
			)
			controller := membership.New(pool)
			switch test.state {
			case membership.StateOff:
			case membership.StateDualWrite, membership.StateReady:
				if _, err := controller.Cutover(ctx, membership.FleetProof{
					Members: []membership.FleetMember{runtime.FleetMember()},
				}); err != nil {
					t.Fatal(err)
				}
				if test.state == membership.StateReady {
					progress, err := controller.Backfill(ctx)
					if err != nil {
						t.Fatal(err)
					}
					if !progress.Done || !progress.Ready {
						t.Fatalf("zero-row readiness backfill = %#v", progress)
					}
					if _, err := controller.PromoteReady(ctx); err != nil {
						t.Fatal(err)
					}
				}
			default:
				t.Fatalf("unsupported trace state %q", test.state)
			}
			fixture, err := records.Create(ctx, CreateRequest{
				Principal: principal, RequestID: "membership-trace-fixture-" + string(test.state), Collection: "records",
				Input: map[string]json.RawMessage{"title": json.RawMessage(`"trace-fixture"`)},
			})
			if err != nil {
				t.Fatal(err)
			}
			fixtureID := recordResultID(t, fixture)
			catalog, err := schemas.Catalog(ctx)
			if err != nil {
				t.Fatal(err)
			}
			collection := catalog.Collections["records"]

			acquiresBefore := pool.Stats().AcquireCount
			writer, err := records.members.writer(ctx)
			if err != nil {
				t.Fatal(err)
			}
			if acquiresAfter := pool.Stats().AcquireCount; acquiresAfter != acquiresBefore {
				t.Fatalf("injected provider lookup acquired %d connections, want 0",
					acquiresAfter-acquiresBefore)
			}

			trace := &membershipSQLTrace{}
			config, err := pgx.ParseConfig(databaseURL)
			if err != nil {
				t.Fatal(err)
			}
			config.Tracer = trace
			connection, err := pgx.ConnectConfig(ctx, config)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() {
				if err := connection.Close(context.Background()); err != nil {
					t.Errorf("close membership trace connection: %v", err)
				}
			})

			createStatements := traceCreateMembership(
				t, ctx, trace, connection, schemas, writer, collection,
			)
			assertMembershipTraceCount(t, "create", createStatements, test.create)
			updateStatements := traceUpdateMembership(
				t, ctx, trace, connection, schemas, writer, collection, fixtureID,
			)
			assertMembershipTraceCount(t, "update", updateStatements, test.update)
			deleteStatements := traceDeleteMembership(
				t, ctx, trace, connection, schemas, writer, collection, fixtureID,
			)
			assertMembershipTraceCount(t, "delete", deleteStatements, test.delete)
		})
	}
}

func traceCreateMembership(
	t *testing.T,
	ctx context.Context,
	trace *membershipSQLTrace,
	connection *pgx.Conn,
	schemas *schema.Service,
	writer membership.Writer,
	collection schema.Collection,
) []string {
	t.Helper()
	tx, err := connection.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(context.Background())
	trace.start()
	mark := trace.mark()
	if _, err := schemas.EnsureCurrent(ctx, tx); err != nil {
		t.Fatal(err)
	}
	fence, err := writer.LockCreate(ctx, tx, collection.ID)
	if err != nil {
		t.Fatal(err)
	}
	if err := fence.Reserve(ctx); err != nil {
		t.Fatal(err)
	}
	recordID := uuid.New()
	if _, err := tx.Exec(ctx, `insert into app.records (id, title, created_at, updated_at)
		values ($1, $2, statement_timestamp(), statement_timestamp())`,
		recordID, "membership-trace-create-"+recordID.String()); err != nil {
		t.Fatal(err)
	}
	if err := fence.RecordCreated(ctx, recordID); err != nil {
		t.Fatal(err)
	}
	return trace.statementsFrom(mark)
}

func traceUpdateMembership(
	t *testing.T,
	ctx context.Context,
	trace *membershipSQLTrace,
	connection *pgx.Conn,
	schemas *schema.Service,
	writer membership.Writer,
	collection schema.Collection,
	recordID uuid.UUID,
) []string {
	t.Helper()
	tx, err := connection.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(context.Background())
	mark := trace.mark()
	if _, err := schemas.EnsureCurrent(ctx, tx); err != nil {
		t.Fatal(err)
	}
	if _, err := writer.Begin(ctx, tx); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(ctx, `update app.records set updated_at = statement_timestamp()
		where id = $1`, recordID); err != nil {
		t.Fatal(err)
	}
	return trace.statementsFrom(mark)
}

func traceDeleteMembership(
	t *testing.T,
	ctx context.Context,
	trace *membershipSQLTrace,
	connection *pgx.Conn,
	schemas *schema.Service,
	writer membership.Writer,
	collection schema.Collection,
	recordID uuid.UUID,
) []string {
	t.Helper()
	tx, err := connection.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(context.Background())
	mark := trace.mark()
	if _, err := schemas.EnsureCurrent(ctx, tx); err != nil {
		t.Fatal(err)
	}
	fence, err := writer.Begin(ctx, tx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(ctx, `delete from app.records where id = $1`, recordID); err != nil {
		t.Fatal(err)
	}
	if err := fence.Delete(ctx, []membership.Record{{
		CollectionID: collection.ID,
		RecordID:     recordID,
	}}); err != nil {
		t.Fatal(err)
	}
	return trace.statementsFrom(mark)
}

func assertMembershipTraceCount(
	t *testing.T,
	operation string,
	statements []string,
	want membershipTraceCount,
) {
	t.Helper()
	membershipStatements := 0
	for index, statement := range statements {
		if membershipTraceStatement(statement, index) {
			membershipStatements++
		}
	}
	got := membershipTraceCount{total: len(statements), membership: membershipStatements}
	if got != want {
		t.Fatalf("%s trace counts = %#v, want %#v\n%s", operation, got, want,
			formatMembershipTrace(statements))
	}
}

func membershipTraceStatement(statement string, index int) bool {
	if strings.HasPrefix(statement, "select version from pgbase_meta.schema_state where singleton") {
		// Every controlled operation begins with EnsureCurrent. The later
		// identical schema pin belongs to Writer.LockCreate/Begin.
		return index != 0
	}
	return strings.Contains(statement, "pgbase_meta.record_membership_capability") ||
		strings.Contains(statement, "pgbase_meta.collection_creation_counters") ||
		strings.Contains(statement, "pgbase_meta.record_creation_ordinals") ||
		strings.Contains(statement, "pgbase_meta.record_membership_backfills") ||
		strings.Contains(statement, "pgbase_meta.runtime_writer_leases") ||
		strings.Contains(statement, "pgbase_meta.system_migrations") ||
		strings.Contains(statement, "to_regclass('pgbase_meta.record_membership") ||
		strings.HasPrefix(statement, "select table_name from pgbase_meta.collections") ||
		strings.HasPrefix(statement, "select id, table_name from pgbase_meta.collections") ||
		strings.HasPrefix(statement, "select exists ( select 1 from \"app\".\"records\"")
}

func formatMembershipTrace(statements []string) string {
	lines := make([]string, len(statements))
	for index, statement := range statements {
		lines[index] = fmt.Sprintf("%02d %s", index, statement)
	}
	return strings.Join(lines, "\n")
}
