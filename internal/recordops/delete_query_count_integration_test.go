//go:build integration

package recordops

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"sort"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/webisnoteasy/PgBase/internal/recordartifact"
	"github.com/webisnoteasy/PgBase/internal/schema"
)

const (
	cascadeDeleteIsolation      = "isolation"
	cascadeDeleteAdvisory       = "advisory_lock"
	cascadeDeleteReverseRead    = "reverse_read"
	cascadeDeleteAffectedLock   = "affected_lock"
	cascadeDeleteBaseMeasure    = "base_measure"
	cascadeDeleteBaseRead       = "base_read"
	cascadeDeleteRevisionRead   = "revision_read"
	cascadeDeleteStorageUpdate  = "storage_update"
	cascadeDeleteBaseDelete     = "base_delete"
	cascadeDeleteAuditInsert    = "audit_insert"
	cascadeDeleteRevisionDelete = "revision_delete"
	cascadeDeleteOutboxInsert   = "outbox_insert"
	cascadeDeleteMemberTables   = "membership_tables"
	cascadeDeleteMemberBaseRead = "membership_base_read"
	cascadeDeleteMemberDelete   = "membership_ordinal_delete"
)

type cascadeDeleteTraceContextKey struct{}

type cascadeDeleteMeasurement struct {
	statements map[string]int
	rows       map[string]int64
	total      int
	unknown    []string
}

type cascadeDeleteTrace struct {
	enabled bool
	cascadeDeleteMeasurement
}

func (t *cascadeDeleteTrace) reset() {
	t.enabled = true
	t.cascadeDeleteMeasurement = cascadeDeleteMeasurement{
		statements: make(map[string]int),
		rows:       make(map[string]int64),
	}
}

func (t *cascadeDeleteTrace) stop() cascadeDeleteMeasurement {
	t.enabled = false
	return t.cascadeDeleteMeasurement
}

func (t *cascadeDeleteTrace) TraceQueryStart(ctx context.Context, _ *pgx.Conn, data pgx.TraceQueryStartData) context.Context {
	if !t.enabled {
		return ctx
	}
	statement := strings.Join(strings.Fields(strings.ToLower(data.SQL)), " ")
	category := cascadeDeleteStatementCategory(statement)
	t.total++
	if category == "" {
		t.unknown = append(t.unknown, statement)
		return ctx
	}
	t.statements[category]++
	return context.WithValue(ctx, cascadeDeleteTraceContextKey{}, category)
}

func (t *cascadeDeleteTrace) TraceQueryEnd(ctx context.Context, _ *pgx.Conn, data pgx.TraceQueryEndData) {
	category, _ := ctx.Value(cascadeDeleteTraceContextKey{}).(string)
	if category != "" {
		t.rows[category] += data.CommandTag.RowsAffected()
	}
}

func cascadeDeleteStatementCategory(statement string) string {
	switch {
	case statement == "show transaction_isolation":
		return cascadeDeleteIsolation
	case strings.HasPrefix(statement, "select pg_advisory_xact_lock("):
		return cascadeDeleteAdvisory
	case strings.HasPrefix(statement, `select id, "parent" from "app"."r0_cascade_scale" where "parent" = any`):
		return cascadeDeleteReverseRead
	case strings.HasPrefix(statement, `select id from "app"."r0_cascade_scale" where id = any`) && strings.HasSuffix(statement, "for update"):
		return cascadeDeleteAffectedLock
	case strings.HasPrefix(statement, "with recursive requested(ids) as"):
		return cascadeDeleteBaseMeasure
	case strings.HasPrefix(statement, `select "r0_cascade_scale"."id", `) && strings.Contains(statement, `from "app"."r0_cascade_scale"`):
		return cascadeDeleteBaseRead
	case strings.HasPrefix(statement, "select record_id, version, validator from pgbase_meta.record_revisions"):
		return cascadeDeleteRevisionRead
	case strings.HasPrefix(statement, `update "app"."r0_cascade_scale" set "parent" = null where id = any`):
		return cascadeDeleteStorageUpdate
	case strings.HasPrefix(statement, `delete from "app"."r0_cascade_scale" where id = any`):
		return cascadeDeleteBaseDelete
	case strings.HasPrefix(statement, "insert into pgbase_meta.record_audit_events"):
		return cascadeDeleteAuditInsert
	case strings.HasPrefix(statement, "delete from pgbase_meta.record_revisions"):
		return cascadeDeleteRevisionDelete
	case strings.HasPrefix(statement, "insert into pgbase_meta.record_outbox"):
		return cascadeDeleteOutboxInsert
	case strings.HasPrefix(statement, "select id, table_name from pgbase_meta.collections"):
		return cascadeDeleteMemberTables
	case strings.HasPrefix(statement, `select exists ( select 1 from "app"."r0_cascade_scale"`):
		return cascadeDeleteMemberBaseRead
	case strings.HasPrefix(statement, "delete from pgbase_meta.record_creation_ordinals"):
		return cascadeDeleteMemberDelete
	default:
		return ""
	}
}

// TestCascadeDeleteStatementCountIsBoundedByTheClosureShape pins the R6
// contract: every read and write of a cascade closure is set-based per
// collection, so the statement count is identical for a small and a large
// closure while the affected-row counts still equal the record count the
// per-record path reported.
func TestCascadeDeleteStatementCountIsBoundedByTheClosureShape(t *testing.T) {
	small := measureCascadeDelete(t, 8)
	large := measureCascadeDelete(t, 64)

	wantStatements := map[string]int{
		cascadeDeleteIsolation:      1,
		cascadeDeleteAdvisory:       1,
		cascadeDeleteReverseRead:    2,
		cascadeDeleteAffectedLock:   1,
		cascadeDeleteBaseMeasure:    1,
		cascadeDeleteBaseRead:       1,
		cascadeDeleteRevisionRead:   1,
		cascadeDeleteStorageUpdate:  1,
		cascadeDeleteBaseDelete:     1,
		cascadeDeleteAuditInsert:    1,
		cascadeDeleteRevisionDelete: 1,
		cascadeDeleteOutboxInsert:   1,
		// A fresh install starts with membership already retired, so the
		// sidecar removal is part of every cascade. It is set-based per
		// collection like the rest of the closure.
		cascadeDeleteMemberTables:   1,
		cascadeDeleteMemberBaseRead: 1,
		cascadeDeleteMemberDelete:   1,
	}
	wantTotal := 0
	for _, count := range wantStatements {
		wantTotal += count
	}
	for _, measured := range []struct {
		name        string
		recordCount int
		measurement cascadeDeleteMeasurement
	}{
		{name: "8 records", recordCount: 8, measurement: small},
		{name: "64 records", recordCount: 64, measurement: large},
	} {
		if !reflect.DeepEqual(measured.measurement.statements, wantStatements) {
			t.Errorf("%s statements = %#v, want %#v", measured.name, measured.measurement.statements, wantStatements)
		}
		if measured.measurement.total != wantTotal {
			t.Errorf("%s total statements = %d, want %d", measured.name, measured.measurement.total, wantTotal)
		}
		// The affected-row contract the per-record path published: one base
		// row, one revision, one audit event and one outbox event per record.
		for _, category := range []string{
			cascadeDeleteBaseRead, cascadeDeleteRevisionRead, cascadeDeleteBaseDelete,
			cascadeDeleteAuditInsert, cascadeDeleteRevisionDelete, cascadeDeleteOutboxInsert,
			cascadeDeleteMemberDelete,
		} {
			if got := measured.measurement.rows[category]; got != int64(measured.recordCount) {
				t.Errorf("%s %s rows = %d, want %d", measured.name, category, got, measured.recordCount)
			}
		}
	}
	if small.total != large.total {
		t.Fatalf("statement count grew with record count: 8 records = %d, 64 records = %d", small.total, large.total)
	}
}

func measureCascadeDelete(t *testing.T, recordCount int) cascadeDeleteMeasurement {
	t.Helper()
	databaseURL, pool, schemas, records, principal := newRecordOperationsTestWithURL(t)
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
	for index := 1; index < recordCount; index++ {
		createDeleteRecord(t, ctx, records, principal, collection.Name, "r0-cascade-child-"+uuid.NewString(), map[string]json.RawMessage{
			"name":   json.RawMessage(`"child"`),
			"parent": relationUUIDJSON(rootID),
		})
	}

	trace := &cascadeDeleteTrace{}
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
	requestID := fmt.Sprintf("r0-cascade-%06d", recordCount)
	request := DeleteRequest{
		Principal: principal, RequestID: requestID, Collection: collection.Name, ID: rootID,
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

	// The root event must stay first in the audit sequence and every derived
	// event must follow it exactly once, as the per-record path guaranteed.
	events := readDeleteRequestArtifacts(t, ctx, pool, requestID)
	if len(events) != recordCount {
		t.Fatalf("audit events = %d, want %d", len(events), recordCount)
	}
	for index, event := range events {
		if event.operation != "delete" || event.newVersion != event.previousVersion+1 {
			t.Fatalf("audit event %d = %#v", index, event)
		}
	}
	return measurement
}
