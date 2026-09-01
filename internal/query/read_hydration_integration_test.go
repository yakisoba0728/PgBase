//go:build integration

package query

import (
	"context"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/webisnoteasy/PgBase/internal/schema"
)

const (
	r0HydrationRootCount   = 200
	r0HydrationTargetCount = 1_000
	r0HydrationEdgeCount   = r0HydrationRootCount * r0HydrationTargetCount
	r0HydrationEmitted     = 107
)

type r0HydrationTraceContextKey struct{}

type r0HydrationTrace struct {
	queryCount int
	rootCount  int
	rowCount   int64
}

func (t *r0HydrationTrace) TraceQueryStart(ctx context.Context, _ *pgx.Conn, data pgx.TraceQueryStartData) context.Context {
	statement := strings.ToLower(strings.TrimSpace(data.SQL))
	if !strings.HasPrefix(statement, "select source_id, target_id, position from \"pgbase_relation\"") {
		return ctx
	}
	t.queryCount++
	if len(data.Args) == 1 {
		if ids, ok := data.Args[0].([]uuid.UUID); ok {
			t.rootCount = len(ids)
		}
	}
	return context.WithValue(ctx, r0HydrationTraceContextKey{}, true)
}

func (t *r0HydrationTrace) TraceQueryEnd(ctx context.Context, _ *pgx.Conn, data pgx.TraceQueryEndData) {
	if hydrated, _ := ctx.Value(r0HydrationTraceContextKey{}).(bool); hydrated {
		t.rowCount += data.CommandTag.RowsAffected()
	}
}

func TestPlainListCurrentlyHydratesEveryCandidateBeforeFinalByteTrim(t *testing.T) {
	databaseURL, pool, schemas, records := newR0QueryServiceTest(t)
	ctx := context.Background()
	applyR0QueryCollection(t, schemas, schema.CreateCollectionInput{
		Name: "r0_hydration_targets",
		Fields: []schema.FieldInput{
			{Name: "name", Type: schema.TypeText, Required: true},
		},
	})
	maximum := r0HydrationTargetCount
	root := applyR0QueryCollection(t, schemas, schema.CreateCollectionInput{
		Name: "r0_hydration_roots",
		Fields: []schema.FieldInput{
			{Name: "links", Type: schema.TypeRelation, Relation: &schema.RelationInput{
				TargetCollection: "r0_hydration_targets", Cardinality: schema.RelationMultiple,
				MaxSelect: &maximum, OnDelete: schema.RelationRestrict,
			}},
		},
	})

	targetTable := pgx.Identifier{"app", "r0_hydration_targets"}.Sanitize()
	rootTable := pgx.Identifier{"app", root.TableName}.Sanitize()
	edgeTable := pgx.Identifier{"pgbase_relation", schema.NewRelationPhysicalNames(root.TableName, "links").MultiTable}.Sanitize()
	if tag, err := pool.Exec(ctx, `INSERT INTO `+targetTable+` (id, created_at, updated_at, name)
		SELECT md5('r0-hydration-target-' || value::text)::uuid,
			'2026-01-01 00:00:00+00'::timestamptz,
			'2026-01-01 00:00:00+00'::timestamptz,
			'r0-target-' || value::text
		FROM generate_series(1, $1::integer) AS generated(value)`, r0HydrationTargetCount); err != nil {
		t.Fatal(err)
	} else if tag.RowsAffected() != r0HydrationTargetCount {
		t.Fatalf("target rows = %d, want %d", tag.RowsAffected(), r0HydrationTargetCount)
	}
	if tag, err := pool.Exec(ctx, `INSERT INTO `+rootTable+` (id, created_at, updated_at)
		SELECT md5('r0-hydration-root-' || value::text)::uuid,
			'2026-01-02 00:00:00+00'::timestamptz + value * interval '1 microsecond',
			'2026-01-02 00:00:00+00'::timestamptz + value * interval '1 microsecond'
		FROM generate_series(1, $1::integer) AS generated(value)`, r0HydrationRootCount); err != nil {
		t.Fatal(err)
	} else if tag.RowsAffected() != r0HydrationRootCount {
		t.Fatalf("root rows = %d, want %d", tag.RowsAffected(), r0HydrationRootCount)
	}
	if tag, err := pool.Exec(ctx, `INSERT INTO `+edgeTable+` (source_id, target_id, position)
		SELECT roots.id, targets.id,
			(row_number() OVER (PARTITION BY roots.id ORDER BY targets.id) - 1)::integer
		FROM `+rootTable+` AS roots CROSS JOIN `+targetTable+` AS targets`); err != nil {
		t.Fatal(err)
	} else if tag.RowsAffected() != r0HydrationEdgeCount {
		t.Fatalf("edge fixture rows = %d, want %d", tag.RowsAffected(), r0HydrationEdgeCount)
	}
	operationTime := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)
	records.now = func() time.Time { return operationTime }
	publicPage, err := records.listScalar(ctx, root.Name, "", r0HydrationRootCount, "", "")
	if err != nil {
		t.Fatalf("List(): %v", err)
	}
	if len(publicPage.Items) != r0HydrationEmitted || publicPage.NextCursor == "" {
		t.Fatalf("public List items/cursor = %d/%t, want %d/true",
			len(publicPage.Items), publicPage.NextCursor != "", r0HydrationEmitted)
	}

	trace := &r0HydrationTrace{}
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
	tx, err := conn.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	catalog, err := schemas.EnsureCurrent(ctx, tx)
	if err != nil {
		t.Fatal(err)
	}
	collection := catalog.Collections[root.Name]
	var hydratedCandidates []map[string]any
	result, err := records.readRootPageInTx(ctx, tx, rootPageReadRequest{
		plan:  scalarRootPlan(collection, nil, nil),
		limit: r0HydrationRootCount, operationTime: operationTime,
		hydrate: func(items []map[string]any) error {
			hydratedCandidates = items
			return hydrateRelationRecords(ctx, tx, catalog, collection, items)
		},
	})
	if err != nil {
		t.Fatalf("readRootPageInTx(): %v", err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}

	if trace.queryCount != 1 || trace.rootCount != r0HydrationRootCount || trace.rowCount != r0HydrationEdgeCount {
		t.Fatalf("hydration trace queries/roots/rows = %d/%d/%d, want 1/%d/%d",
			trace.queryCount, trace.rootCount, trace.rowCount, r0HydrationRootCount, r0HydrationEdgeCount)
	}
	if len(hydratedCandidates) != r0HydrationRootCount {
		t.Fatalf("hydrated candidates = %d, want all %d pre-trim roots", len(hydratedCandidates), r0HydrationRootCount)
	}
	if links, ok := hydratedCandidates[len(hydratedCandidates)-1]["links"].([]string); !ok || len(links) != r0HydrationTargetCount {
		t.Fatalf("last discarded candidate links = %T/%d, want []string/%d",
			hydratedCandidates[len(hydratedCandidates)-1]["links"], len(links), r0HydrationTargetCount)
	}
	if len(result.page.Items) != r0HydrationEmitted || result.page.NextCursor == "" {
		t.Fatalf("emitted page items/cursor = %d/%t, want %d/true",
			len(result.page.Items), result.page.NextCursor != "", r0HydrationEmitted)
	}
	if !reflect.DeepEqual(recordIDs(publicPage.Items), recordIDs(result.page.Items)) ||
		publicPage.NextCursor != result.page.NextCursor {
		t.Fatalf("public List and traced root kernel disagree: public=%v/%q traced=%v/%q",
			recordIDs(publicPage.Items), publicPage.NextCursor,
			recordIDs(result.page.Items), result.page.NextCursor)
	}
}
