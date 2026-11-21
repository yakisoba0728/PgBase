//go:build integration

package recordops

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/yakisoba0728/PgBase/internal/membership"
	"github.com/yakisoba0728/PgBase/internal/recordaccess"
)

// writeKernelTrace counts network round trips (one per traced query, one per
// pipelined batch) and every statement, including those inside a batch.
type writeKernelTrace struct {
	mu         sync.Mutex
	enabled    bool
	roundTrips int
	statements []string
	batches    [][]string
	current    []string
}

func (t *writeKernelTrace) TraceQueryStart(ctx context.Context, _ *pgx.Conn, data pgx.TraceQueryStartData) context.Context {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.enabled {
		t.roundTrips++
		t.statements = append(t.statements, normalizeTraceSQL(data.SQL))
	}
	return ctx
}

func (*writeKernelTrace) TraceQueryEnd(context.Context, *pgx.Conn, pgx.TraceQueryEndData) {}

func (t *writeKernelTrace) TraceBatchStart(ctx context.Context, _ *pgx.Conn, _ pgx.TraceBatchStartData) context.Context {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.enabled {
		t.roundTrips++
		t.current = nil
	}
	return ctx
}

func (t *writeKernelTrace) TraceBatchQuery(_ context.Context, _ *pgx.Conn, data pgx.TraceBatchQueryData) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.enabled {
		statement := normalizeTraceSQL(data.SQL)
		t.statements = append(t.statements, statement)
		t.current = append(t.current, statement)
	}
}

func (t *writeKernelTrace) TraceBatchEnd(context.Context, *pgx.Conn, pgx.TraceBatchEndData) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.enabled {
		t.batches = append(t.batches, t.current)
		t.current = nil
	}
}

func (t *writeKernelTrace) start() {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.enabled, t.roundTrips, t.statements, t.batches, t.current = true, 0, nil, nil, nil
}

type writeKernelMeasurement struct {
	roundTrips int
	statements []string
	batches    [][]string
}

func (t *writeKernelTrace) stop() writeKernelMeasurement {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.enabled = false
	return writeKernelMeasurement{roundTrips: t.roundTrips, statements: t.statements, batches: t.batches}
}

func normalizeTraceSQL(sql string) string {
	return strings.Join(strings.Fields(strings.ToLower(sql)), " ")
}

func (m writeKernelMeasurement) count(prefix string) int {
	count := 0
	for _, statement := range m.statements {
		if strings.HasPrefix(statement, prefix) {
			count++
		}
	}
	return count
}

func (m writeKernelMeasurement) String() string {
	var b strings.Builder
	fmt.Fprintf(&b, "round trips %d, statements %d\n", m.roundTrips, len(m.statements))
	for index, statement := range m.statements {
		if len(statement) > 140 {
			statement = statement[:140]
		}
		fmt.Fprintf(&b, "%02d %s\n", index, statement)
	}
	return b.String()
}

const (
	traceAuthorizeUser = "select a.enabled,u.active from pgbase_meta.collection_access a join pgbase_meta.users u"
	traceLoadRules     = "select rules,revision from pgbase_meta.collection_rules"
	traceAuditInsert   = "insert into pgbase_meta.record_audit_events"
	traceRevisionWrite = "insert into pgbase_meta.record_revisions"
	traceRevisionDrop  = "delete from pgbase_meta.record_revisions"
	traceOutboxInsert  = "insert into pgbase_meta.record_outbox"
	traceOwnerInsert   = "insert into pgbase_meta.record_owners"
)

// TestWriteKernelRoundTripsForUserMutations pins the user write kernel's
// round trips: one admission check and one rule load per collection and
// operation, and the artifact inserts pipelined after the last read. The
// operations run on a borrowed Read Committed transaction so a tracer sees
// every statement; owned transactions run the same kernel.
func TestWriteKernelRoundTripsForUserMutations(t *testing.T) {
	databaseURL, pool, schemas, _, _ := newRecordOperationsTestWithURL(t)
	ctx := context.Background()
	runtime, err := membership.RegisterWriter(ctx, membership.New(pool), membership.WriterC)
	if err != nil {
		t.Fatal(err)
	}
	records := NewService(pool, schemas, []byte("0123456789abcdef0123456789abcdef"), nil, nil, runtime)
	user := Principal{Kind: PrincipalUser, ID: uuid.New()}
	if _, err := pool.Exec(ctx, `INSERT INTO pgbase_meta.users(id,email,password_hash,active,created_at,updated_at)
		VALUES($1,'roundtrip@example.test','test',true,transaction_timestamp(),transaction_timestamp())`, user.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := recordaccess.NewService(pool).Set(ctx, "records", true); err != nil {
		t.Fatal(err)
	}

	trace := &writeKernelTrace{}
	config, err := pgx.ParseConfig(databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	config.Tracer = trace
	connection, err := pgx.ConnectConfig(ctx, config)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = connection.Close(context.Background()) })

	title := func(value string) map[string]json.RawMessage {
		return map[string]json.RawMessage{"title": json.RawMessage(`"` + value + `"`)}
	}
	run := func(name string, operation func(context.Context) error) writeKernelMeasurement {
		t.Helper()
		tx, err := connection.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.ReadCommitted})
		if err != nil {
			t.Fatal(err)
		}
		trace.start()
		operationErr := operation(WithTransaction(ctx, tx))
		measured := trace.stop()
		if operationErr != nil {
			_ = tx.Rollback(ctx)
			t.Fatalf("%s: %v\n%s", name, operationErr, measured)
		}
		if err := tx.Commit(ctx); err != nil {
			t.Fatal(err)
		}
		return measured
	}
	var recordID uuid.UUID
	create := func(requestID string) func(context.Context) error {
		return func(ctx context.Context) error {
			result, err := records.Create(ctx, CreateRequest{Principal: user, RequestID: requestID, Collection: "records", Input: title(requestID)})
			if err == nil {
				recordID = result.InternalID
			}
			return err
		}
	}
	// The first create warms the connection's statement cache and membership.
	run("warm create", create("roundtrip-warm"))

	measuredCreate := run("create", create("roundtrip-create"))
	measuredUpdate := run("update", func(ctx context.Context) error {
		_, err := records.Update(ctx, UpdateRequest{Principal: user, RequestID: "roundtrip-update", Collection: "records", ID: recordID, Input: title("roundtrip-updated")})
		return err
	})
	measuredDelete := run("delete", func(ctx context.Context) error {
		_, err := records.Delete(ctx, DeleteRequest{Principal: user, RequestID: "roundtrip-delete", Collection: "records", ID: recordID})
		return err
	})

	for _, test := range []struct {
		name       string
		measured   writeKernelMeasurement
		roundTrips int
		artifacts  []string
	}{
		{"create", measuredCreate, writeKernelCreateRoundTrips, []string{traceAuditInsert, traceRevisionWrite, traceOutboxInsert, traceOwnerInsert}},
		{"update", measuredUpdate, writeKernelUpdateRoundTrips, []string{traceAuditInsert, traceRevisionWrite, traceOutboxInsert}},
		{"delete", measuredDelete, writeKernelDeleteRoundTrips, []string{traceAuditInsert, traceRevisionDrop, traceOutboxInsert}},
	} {
		if got := test.measured.count(traceAuthorizeUser); got != 1 {
			t.Errorf("%s user admission checks = %d, want 1\n%s", test.name, got, test.measured)
		}
		if got := test.measured.count(traceLoadRules); got != 1 {
			t.Errorf("%s rule loads = %d, want 1\n%s", test.name, got, test.measured)
		}
		found := false
		for _, batch := range test.measured.batches {
			if len(batch) != len(test.artifacts) {
				continue
			}
			matches := true
			for index, prefix := range test.artifacts {
				matches = matches && strings.HasPrefix(batch[index], prefix)
			}
			found = found || matches
		}
		if !found {
			t.Errorf("%s artifact writes were not one ordered pipeline %v\n%s", test.name, test.measured.batches, test.measured)
		}
		if test.measured.roundTrips != test.roundTrips {
			t.Errorf("%s round trips = %d, want %d\n%s", test.name, test.measured.roundTrips, test.roundTrips, test.measured)
		}
	}
}

// Round trips of one user mutation on a borrowed transaction, including the
// operation's savepoint. Before the admission/rule memo and the artifact
// pipeline they were 19, 19 and 28. Raise them only with a deliberate kernel
// change. A borrowed create keeps its second create-rule check; only an owned
// Repeatable Read create on a relation-free collection skips it.
const (
	writeKernelCreateRoundTrips = 14
	writeKernelUpdateRoundTrips = 16
	writeKernelDeleteRoundTrips = 24
)
