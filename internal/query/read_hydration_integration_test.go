//go:build integration

package query

import (
	"context"
	"encoding/json"
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
	if !strings.HasPrefix(statement, "with requested(source_id, source_ordinal)") ||
		!strings.Contains(statement, "hydration_rows") {
		return ctx
	}
	t.queryCount++
	if len(data.Args) == 1 {
		if ids, ok := data.Args[0].([]uuid.UUID); ok {
			t.rootCount += len(ids)
		}
	}
	return context.WithValue(ctx, r0HydrationTraceContextKey{}, true)
}

func (t *r0HydrationTrace) TraceQueryEnd(ctx context.Context, _ *pgx.Conn, data pgx.TraceQueryEndData) {
	if hydrated, _ := ctx.Value(r0HydrationTraceContextKey{}).(bool); hydrated {
		t.rowCount += data.CommandTag.RowsAffected()
	}
}

func TestReadKernelPlainListRetainsOnlyEmittedRootsAndOneLookahead(t *testing.T) {
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
	// Root 108 is the one hydrated lookahead that establishes the byte
	// boundary. Corruption in root 109 must remain outside this page: a batch
	// reader may prefetch and decode its base row, but it must not materialize or
	// validate that speculative root's relation values before finalizing.
	if _, err := pool.Exec(ctx, `ALTER TABLE `+edgeTable+` DISABLE TRIGGER USER`); err != nil {
		t.Fatal(err)
	}
	if tag, err := pool.Exec(ctx, `DELETE FROM `+edgeTable+`
		WHERE source_id = md5('r0-hydration-root-109')::uuid AND position = $1`,
		r0HydrationTargetCount/2); err != nil {
		t.Fatal(err)
	} else if tag.RowsAffected() != 1 {
		t.Fatalf("corrupted speculative edges = %d, want 1", tag.RowsAffected())
	}
	if _, err := pool.Exec(ctx, `ALTER TABLE `+edgeTable+` ENABLE TRIGGER USER`); err != nil {
		t.Fatal(err)
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
	result, err := records.readRootPageInTx(ctx, tx, rootPageReadRequest{
		plan:  scalarRootPlan(collection, nil, nil),
		limit: r0HydrationRootCount, operationTime: operationTime,
		hydrator: newRootHydrator(tx, catalog, collection),
	})
	if err != nil {
		t.Fatalf("readRootPageInTx(): %v", err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}

	wantHydrated := r0HydrationEmitted + 1
	wantPrefetched := ((wantHydrated + rootHydrationBatchSize - 1) / rootHydrationBatchSize) * rootHydrationBatchSize
	wantQueries := wantPrefetched / rootHydrationBatchSize
	wantRows := wantPrefetched*(r0HydrationTargetCount+1) - 1 // one sentinel per root, minus the drift fixture edge
	if trace.queryCount != wantQueries || trace.rootCount != wantPrefetched ||
		trace.rowCount != int64(wantRows) {
		t.Fatalf("hydration trace queries/roots/rows = %d/%d/%d, want %d/%d/%d",
			trace.queryCount, trace.rootCount, trace.rowCount, wantQueries, wantPrefetched,
			wantRows)
	}
	if result.totalHydrated != wantHydrated {
		t.Fatalf("total sequential hydration = %d, want %d", result.totalHydrated, wantHydrated)
	}
	if result.maxRetainedHydrated > len(result.page.Items)+1 {
		t.Fatalf("maximum retained hydrated roots = %d, emitted = %d; want emitted + one lookahead",
			result.maxRetainedHydrated, len(result.page.Items))
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

func TestReadKernelContinuesPastProvisionalCursorOverflowAtDatasetEnd(t *testing.T) {
	_, pool, schemas, records := newR0QueryServiceTest(t)
	ctx := context.Background()
	applyR0QueryCollection(t, schemas, schema.CreateCollectionInput{
		Name:   "cursor_recovery_targets",
		Fields: []schema.FieldInput{{Name: "name", Type: schema.TypeText}},
	})
	maximum := 2
	root := applyR0QueryCollection(t, schemas, schema.CreateCollectionInput{
		Name: "cursor_recovery_roots",
		Fields: []schema.FieldInput{
			{Name: "payload_a", Type: schema.TypeText},
			{Name: "payload_b", Type: schema.TypeText},
			{Name: "payload_c", Type: schema.TypeText},
			{Name: "payload_d", Type: schema.TypeText},
			{Name: "links", Type: schema.TypeRelation, Relation: &schema.RelationInput{
				TargetCollection: "cursor_recovery_targets", Cardinality: schema.RelationMultiple,
				MaxSelect: &maximum, OnDelete: schema.RelationRestrict,
			}},
		},
	})
	firstID := uuid.MustParse("10000000-0000-4000-8000-000000000001")
	secondID := uuid.MustParse("20000000-0000-4000-8000-000000000002")
	firstTime := time.Date(2026, 9, 2, 1, 0, 0, 0, time.UTC)
	secondTime := firstTime.Add(time.Microsecond)
	payloads := []string{
		strings.Repeat("x", maximumUnindexedTextBytes),
		strings.Repeat("x", maximumUnindexedTextBytes),
		strings.Repeat("x", maximumUnindexedTextBytes),
		strings.Repeat("x", maximumUnindexedTextBytes-8*1024),
	}
	buildCandidates := func() []pageCandidate {
		first := map[string]any{
			systemFieldID: firstID.String(), systemFieldCreatedAt: firstTime.Format(time.RFC3339Nano),
			systemFieldUpdatedAt: firstTime.Format(time.RFC3339Nano), "links": []string{},
			"payload_a": payloads[0], "payload_b": payloads[1], "payload_c": payloads[2], "payload_d": payloads[3],
		}
		second := map[string]any{
			systemFieldID: secondID.String(), systemFieldCreatedAt: secondTime.Format(time.RFC3339Nano),
			systemFieldUpdatedAt: secondTime.Format(time.RFC3339Nano), "links": []string{},
			"payload_a": "", "payload_b": "", "payload_c": "", "payload_d": "",
		}
		items := []map[string]any{first, second}
		candidates := make([]pageCandidate, len(items))
		for index, item := range items {
			values, err := recordSortValues(effectiveSortKeys(nil), item)
			if err != nil {
				t.Fatal(err)
			}
			candidates[index] = pageCandidate{root: item, wire: item, cursorValues: values}
		}
		return candidates
	}
	assembler := pageAssembler{
		cursors: records.cursors, collectionID: root.ID, sortKeys: effectiveSortKeys(nil),
		expiresAt: time.Date(2026, 9, 2, 2, 0, 0, 0, time.UTC), maximumBytes: maximumRecordPageBytes,
	}
	candidates := buildCandidates()
	firstBytes, err := candidates[0].measureWire()
	if err != nil {
		t.Fatal(err)
	}
	secondBytes, err := candidates[1].measureWire()
	if err != nil {
		t.Fatal(err)
	}
	_, terminalBytes, err := assembler.measureResponse(candidates, firstBytes+secondBytes, false)
	if err != nil {
		t.Fatal(err)
	}
	adjustment := maximumRecordPageBytes - terminalBytes
	if adjustment <= 0 || len(payloads[3])+adjustment > maximumUnindexedTextBytes {
		t.Fatalf("cursor recovery fixture adjustment = %d from terminal bytes %d", adjustment, terminalBytes)
	}
	payloads[3] += strings.Repeat("x", adjustment)
	candidates = buildCandidates()
	firstBytes, err = candidates[0].measureWire()
	if err != nil {
		t.Fatal(err)
	}
	secondBytes, err = candidates[1].measureWire()
	if err != nil {
		t.Fatal(err)
	}
	_, firstWithCursor, err := assembler.measureResponse(candidates[:1], firstBytes, true)
	if err != nil {
		t.Fatal(err)
	}
	_, terminalBytes, err = assembler.measureResponse(candidates, firstBytes+secondBytes, false)
	if err != nil {
		t.Fatal(err)
	}
	if firstWithCursor <= maximumRecordPageBytes || terminalBytes != maximumRecordPageBytes {
		t.Fatalf("cursor recovery fixture first/terminal bytes = %d/%d, want >%d/%d",
			firstWithCursor, terminalBytes, maximumRecordPageBytes, maximumRecordPageBytes)
	}

	table := pgx.Identifier{"app", root.TableName}.Sanitize()
	if _, err := pool.Exec(ctx, `INSERT INTO `+table+`
		(id, created_at, updated_at, payload_a, payload_b, payload_c, payload_d)
		VALUES ($1, $2, $2, $3, $4, $5, $6), ($7, $8, $8, '', '', '', '')`,
		firstID, firstTime, payloads[0], payloads[1], payloads[2], payloads[3], secondID, secondTime); err != nil {
		t.Fatal(err)
	}
	page, err := records.List(ctx, ListRequest{
		Principal:  Principal{Kind: PrincipalAdministrator, ID: uuid.MustParse(r0AdministratorID)},
		Collection: root.Name, Limit: 2,
	})
	if err != nil {
		t.Fatalf("List(): %v", err)
	}
	if len(page.Items) != 2 || page.NextCursor != "" {
		t.Fatalf("terminal recovery items/cursor = %d/%q, want 2/empty", len(page.Items), page.NextCursor)
	}
	encoded, err := json.Marshal(page)
	if err != nil {
		t.Fatal(err)
	}
	if len(encoded)+1 != maximumRecordPageBytes {
		t.Fatalf("terminal recovery response bytes = %d, want %d", len(encoded)+1, maximumRecordPageBytes)
	}
	var kernel rootPageReadResult
	if err := pool.InTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead}, func(tx pgx.Tx) error {
		catalog, err := schemas.EnsureCurrent(ctx, tx)
		if err != nil {
			return err
		}
		collection := catalog.Collections[root.Name]
		kernel, err = records.readRootPageInTx(ctx, tx, rootPageReadRequest{
			plan: scalarRootPlan(collection, nil, nil), limit: 2,
			operationTime: assembler.expiresAt.Add(-cursorTTL),
			hydrator:      newRootHydrator(tx, catalog, collection),
		})
		return err
	}); err != nil {
		t.Fatalf("traced root kernel: %v", err)
	}
	if !reflect.DeepEqual(kernel.page, page) || kernel.totalHydrated != 3 || kernel.maxRetainedHydrated > 2 {
		t.Fatalf("terminal recovery kernel page/hydrated/retained = %#v/%d/%d, want public page/3/<=2",
			kernel.page, kernel.totalHydrated, kernel.maxRetainedHydrated)
	}
}
