//go:build integration

package recordops

import (
	"context"
	"encoding/json"
	"sort"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/webisnoteasy/PgBase/internal/recordartifact"
	"github.com/webisnoteasy/PgBase/internal/schema"
)

const r0CascadeRecordCount = 64

const (
	r0DeleteIsolation      = "isolation"
	r0DeleteAdvisory       = "advisory_lock"
	r0DeleteReverseRead    = "reverse_read"
	r0DeleteAffectedLock   = "affected_lock"
	r0DeleteBaseRead       = "base_read"
	r0DeleteRevisionRead   = "revision_read"
	r0DeleteStorageUpdate  = "storage_update"
	r0DeleteBaseDelete     = "base_delete"
	r0DeleteAuditInsert    = "audit_insert"
	r0DeleteRevisionDelete = "revision_delete"
	r0DeleteOutboxInsert   = "outbox_insert"
)

type r0DeleteTraceContextKey struct{}

type r0DeleteTraceMeasurement struct {
	statements map[string]int
	rows       map[string]int64
	total      int
	unknown    []string
}

type r0DeleteTrace struct {
	enabled bool
	r0DeleteTraceMeasurement
}

func (t *r0DeleteTrace) reset() {
	t.enabled = true
	t.r0DeleteTraceMeasurement = r0DeleteTraceMeasurement{
		statements: make(map[string]int),
		rows:       make(map[string]int64),
	}
}

func (t *r0DeleteTrace) stop() r0DeleteTraceMeasurement {
	t.enabled = false
	return t.r0DeleteTraceMeasurement
}

func (t *r0DeleteTrace) TraceQueryStart(ctx context.Context, _ *pgx.Conn, data pgx.TraceQueryStartData) context.Context {
	if !t.enabled {
		return ctx
	}
	statement := strings.Join(strings.Fields(strings.ToLower(data.SQL)), " ")
	category := r0DeleteStatementCategory(statement)
	t.total++
	if category == "" {
		t.unknown = append(t.unknown, statement)
		return ctx
	}
	t.statements[category]++
	return context.WithValue(ctx, r0DeleteTraceContextKey{}, category)
}

func (t *r0DeleteTrace) TraceQueryEnd(ctx context.Context, _ *pgx.Conn, data pgx.TraceQueryEndData) {
	category, _ := ctx.Value(r0DeleteTraceContextKey{}).(string)
	if category != "" {
		t.rows[category] += data.CommandTag.RowsAffected()
	}
}

func r0DeleteStatementCategory(statement string) string {
	switch {
	case statement == "show transaction_isolation":
		return r0DeleteIsolation
	case strings.HasPrefix(statement, "select pg_advisory_xact_lock("):
		return r0DeleteAdvisory
	case strings.HasPrefix(statement, `select id, "parent" from "app"."r0_cascade_scale" where "parent" = any`):
		return r0DeleteReverseRead
	case strings.HasPrefix(statement, `select id from "app"."r0_cascade_scale" where id = any`) && strings.HasSuffix(statement, "for update"):
		return r0DeleteAffectedLock
	case strings.HasPrefix(statement, `select "id", "created_at", "updated_at", "name", "parent" from "app"."r0_cascade_scale" where "id" = $1`):
		return r0DeleteBaseRead
	case strings.HasPrefix(statement, "select version, validator from pgbase_meta.record_revisions"):
		return r0DeleteRevisionRead
	case strings.HasPrefix(statement, `update "app"."r0_cascade_scale" set "parent" = null where id = any`):
		return r0DeleteStorageUpdate
	case strings.HasPrefix(statement, `delete from "app"."r0_cascade_scale" where "id" = $1`):
		return r0DeleteBaseDelete
	case strings.HasPrefix(statement, "insert into pgbase_meta.record_audit_events"):
		return r0DeleteAuditInsert
	case strings.HasPrefix(statement, "delete from pgbase_meta.record_revisions"):
		return r0DeleteRevisionDelete
	case strings.HasPrefix(statement, "insert into pgbase_meta.record_outbox"):
		return r0DeleteOutboxInsert
	default:
		return ""
	}
}

func TestCascadeDeleteCurrentlyExecutesSixStatementsPerRecordPlusSixFixed(t *testing.T) {
	databaseURL, _, schemas, records, principal := newRecordOperationsTestWithURL(t)
	ctx := context.Background()
	collection := applyDeleteCollection(t, ctx, schemas, principal.ID, schema.CreateCollectionInput{
		Name: "r0_cascade_scale",
		Fields: []schema.FieldInput{
			{Name: "name", Type: schema.TypeText, Required: true},
			{Name: "parent", Type: schema.TypeRelation, Relation: &schema.RelationInput{
				TargetCollection: "r0_cascade_scale", Cardinality: schema.RelationSingle,
				OnDelete: schema.RelationCascade,
			}},
		},
	})
	root := createDeleteRecord(t, ctx, records, principal, collection.Name, "r0-cascade-root", map[string]json.RawMessage{
		"name": json.RawMessage(`"root"`),
	})
	rootID := recordResultID(t, root)
	for index := 1; index < r0CascadeRecordCount; index++ {
		createDeleteRecord(t, ctx, records, principal, collection.Name, "r0-cascade-child-"+uuid.NewString(), map[string]json.RawMessage{
			"name":   json.RawMessage(`"child"`),
			"parent": relationUUIDJSON(rootID),
		})
	}

	trace := &r0DeleteTrace{}
	config, err := pgx.ParseConfig(databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	config.Tracer = trace
	conn, err := pgx.ConnectConfig(ctx, config)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := conn.Close(ctx); err != nil {
			t.Errorf("close traced connection: %v", err)
		}
	})
	tx, err := conn.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.ReadCommitted})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	catalog, err := schemas.EnsureCurrent(ctx, tx)
	if err != nil {
		t.Fatal(err)
	}
	membershipWriter, err := records.members.writer(ctx)
	if err != nil {
		t.Fatal(err)
	}
	writeFence, err := membershipWriter.Begin(ctx, tx)
	if err != nil {
		t.Fatal(err)
	}
	trace.reset()
	eventID, err := uuid.NewV7()
	if err != nil {
		t.Fatal(err)
	}
	request := DeleteRequest{
		Principal: principal, RequestID: "r0-cascade-delete", Collection: collection.Name, ID: rootID,
	}
	inputHash := recordartifact.Digest("pgbase-delete-request-v1", []byte(request.Collection), request.ID[:])
	result, err := records.deleteGraphInTransaction(
		ctx, tx, catalog, catalog.Collections[collection.Name], request,
		newDeleteArtifactIdentityCache(eventID), inputHash, CompatibilityLastWriteWins, writeFence,
	)
	if err != nil {
		t.Fatalf("deleteGraphInTransaction(): %v", err)
	}
	measurement := trace.stop()
	if err := tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	if result.EventID != eventID || result.Compatibility != CompatibilityLastWriteWins {
		t.Fatalf("delete result = %#v, want event %s and missing-if-match compatibility", result, eventID)
	}

	if len(measurement.unknown) != 0 {
		sort.Strings(measurement.unknown)
		t.Fatalf("unclassified delete statements (%d):\n%s", len(measurement.unknown), strings.Join(measurement.unknown, "\n"))
	}
	wantTotal := 6*r0CascadeRecordCount + 6
	if measurement.total != wantTotal {
		t.Fatalf("delete statements = %d, want 6N+6 = %d", measurement.total, wantTotal)
	}
	for _, category := range []string{
		r0DeleteBaseRead, r0DeleteRevisionRead, r0DeleteBaseDelete,
		r0DeleteAuditInsert, r0DeleteRevisionDelete, r0DeleteOutboxInsert,
	} {
		if measurement.statements[category] != r0CascadeRecordCount || measurement.rows[category] != r0CascadeRecordCount {
			t.Errorf("%s statements/rows = %d/%d, want %d/%d",
				category, measurement.statements[category], measurement.rows[category], r0CascadeRecordCount, r0CascadeRecordCount)
		}
	}
	wantFixed := map[string]int{
		r0DeleteIsolation:     1,
		r0DeleteAdvisory:      1,
		r0DeleteReverseRead:   2,
		r0DeleteAffectedLock:  1,
		r0DeleteStorageUpdate: 1,
	}
	for category, count := range wantFixed {
		if measurement.statements[category] != count {
			t.Errorf("fixed %s statements = %d, want %d", category, measurement.statements[category], count)
		}
	}
}
