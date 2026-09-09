//go:build integration

package query

import (
	"context"
	"encoding/json"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/webisnoteasy/PgBase/internal/membership"
	"github.com/webisnoteasy/PgBase/internal/schema"
)

const (
	r0HydrationRootCount   = 200
	r0HydrationTargetCount = 1_000
	r0HydrationEdgeCount   = r0HydrationRootCount * r0HydrationTargetCount
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
		!strings.Contains(statement, "hydration_edges") {
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
			{Name: "payload", Type: schema.TypeJSONB},
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
	rootPayload, err := json.Marshal(strings.Repeat("x", 250<<10))
	if err != nil {
		t.Fatal(err)
	}
	if tag, err := pool.Exec(ctx, `INSERT INTO `+rootTable+` (id, created_at, updated_at, payload)
		SELECT md5('r0-hydration-root-' || value::text)::uuid,
			'2026-01-02 00:00:00+00'::timestamptz + value * interval '1 microsecond',
			'2026-01-02 00:00:00+00'::timestamptz + value * interval '1 microsecond', $2::jsonb
		FROM generate_series(1, $1::integer) AS generated(value)`, r0HydrationRootCount, rootPayload); err != nil {
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
	syncMembershipOrdinals(t, ctx, pool)
	operationTime := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)
	publicPage, err := records.listScalar(ctx, root.Name, "", r0HydrationRootCount, "", "")
	if err != nil {
		t.Fatalf("List(): %v", err)
	}
	emitted := len(publicPage.Items)
	if emitted == 0 || emitted >= r0HydrationRootCount || publicPage.NextCursor == "" {
		t.Fatalf("public List items/cursor = %d/%t, want a nonempty trimmed page",
			emitted, publicPage.NextCursor != "")
	}

	// Corrupt the first root beyond the hydrated lookahead. A batch reader may
	// prefetch its base row, but it must not materialize or validate that
	// speculative root's relation values before finalizing.
	if _, err := pool.Exec(ctx, `ALTER TABLE `+edgeTable+` DISABLE TRIGGER USER`); err != nil {
		t.Fatal(err)
	}
	if tag, err := pool.Exec(ctx, `DELETE FROM `+edgeTable+`
		WHERE source_id = md5('r0-hydration-root-' || $1::text)::uuid AND position = $2`,
		strconv.Itoa(emitted+2), r0HydrationTargetCount/2); err != nil {
		t.Fatal(err)
	} else if tag.RowsAffected() != 1 {
		t.Fatalf("corrupted speculative edges = %d, want 1", tag.RowsAffected())
	}
	if _, err := pool.Exec(ctx, `ALTER TABLE `+edgeTable+` ENABLE TRIGGER USER`); err != nil {
		t.Fatal(err)
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
	plan, err := scalarRootPlan(collection, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	// A v5 cursor binds the catalog version, the principal scope and the
	// durable traversal, so the traced kernel call has to be given the same
	// three the public reader resolved for itself.
	plan.catalogVersion, plan.principal = catalog.Version, testReadPrincipal
	traversal, err := membership.ResolveTraversal(ctx, tx, collection.ID)
	if err != nil {
		t.Fatal(err)
	}
	result, err := records.readRootPageInTx(ctx, tx, rootPageReadRequest{
		plan:  plan,
		limit: r0HydrationRootCount, operationTime: operationTime,
		traversal: traversal,
		hydrator:  newRootHydrator(tx, catalog, collection),
	})
	if err != nil {
		t.Fatalf("readRootPageInTx(): %v", err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}

	wantHydrated := emitted + 1
	wantPrefetched := ((wantHydrated + rootHydrationBatchSize - 1) / rootHydrationBatchSize) * rootHydrationBatchSize
	wantQueries := wantPrefetched / rootHydrationBatchSize
	wantRows := wantPrefetched // one replay result row per requested root
	if trace.queryCount != wantQueries || trace.rootCount != wantPrefetched ||
		trace.rowCount != int64(wantRows) {
		t.Fatalf("hydration trace queries/roots/rows = %d/%d/%d, want %d/%d/%d",
			trace.queryCount, trace.rootCount, trace.rowCount, wantQueries, wantPrefetched,
			wantRows)
	}
	if result.totalHydrated != wantHydrated {
		t.Fatalf("total sequential hydration = %d, want %d", result.totalHydrated, wantHydrated)
	}
	if result.maxRetainedRoots > len(result.page.Items)+1 {
		t.Fatalf("maximum retained root maps = %d, emitted = %d; want emitted + one lookahead",
			result.maxRetainedRoots, len(result.page.Items))
	}
	if len(result.page.Items) != emitted || result.page.NextCursor == "" {
		t.Fatalf("emitted page items/cursor = %d/%t, want %d/true",
			len(result.page.Items), result.page.NextCursor != "", emitted)
	}
	if !reflect.DeepEqual(recordIDs(publicPage.Items), recordIDs(result.page.Items)) {
		t.Fatalf("public List and traced root kernel disagree: public=%v/%q traced=%v/%q",
			recordIDs(publicPage.Items), publicPage.NextCursor,
			recordIDs(result.page.Items), result.page.NextCursor)
	}
	assertEquivalentCursorIgnoringExpiry(t, records, collection.ID,
		publicPage.NextCursor, result.page.NextCursor)
}

func TestReadKernelLargeScalarRootsRetainExactlyEmittedPlusOneMap(t *testing.T) {
	_, pool, schemas, records := newR0QueryServiceTest(t)
	ctx := context.Background()
	applyR0QueryCollection(t, schemas, schema.CreateCollectionInput{
		Name: "large_scalar_targets",
		Fields: []schema.FieldInput{
			{Name: "name", Type: schema.TypeText},
		},
	})
	maximum := 2
	root := applyR0QueryCollection(t, schemas, schema.CreateCollectionInput{
		Name: "large_scalar_roots",
		Fields: []schema.FieldInput{
			{Name: "payload", Type: schema.TypeEditor, Required: true},
			{Name: "links_a", Type: schema.TypeRelation, Relation: &schema.RelationInput{
				TargetCollection: "large_scalar_targets", Cardinality: schema.RelationMultiple,
				MaxSelect: &maximum, OnDelete: schema.RelationRestrict,
			}},
			{Name: "links_b", Type: schema.TypeRelation, Relation: &schema.RelationInput{
				TargetCollection: "large_scalar_targets", Cardinality: schema.RelationMultiple,
				MaxSelect: &maximum, OnDelete: schema.RelationRestrict,
			}},
		},
	})

	const rootCount = 8
	const payloadBytes = 7 << 20
	table := pgx.Identifier{"app", root.TableName}.Sanitize()
	if tag, err := pool.Exec(ctx, `INSERT INTO `+table+` (id, created_at, updated_at, payload)
		SELECT md5('large-scalar-root-' || value::text)::uuid,
			'2026-01-03 00:00:00+00'::timestamptz + value * interval '1 microsecond',
			'2026-01-03 00:00:00+00'::timestamptz + value * interval '1 microsecond',
			repeat('x', $2::integer)
		FROM generate_series(1, $1::integer) AS generated(value)`, rootCount, payloadBytes); err != nil {
		t.Fatal(err)
	} else if tag.RowsAffected() != rootCount {
		t.Fatalf("large scalar fixture rows = %d, want %d", tag.RowsAffected(), rootCount)
	}

	syncMembershipOrdinals(t, ctx, pool)
	operationTime := time.Date(2026, 9, 2, 3, 0, 0, 0, time.UTC)
	publicPage, err := records.listScalar(ctx, root.Name, "", rootCount, "", "")
	if err != nil {
		t.Fatalf("List(): %v", err)
	}
	emitted := len(publicPage.Items)
	if emitted == 0 || emitted >= rootCount || publicPage.NextCursor == "" {
		t.Fatalf("large scalar public page items/cursor = %d/%t, want a nonempty trimmed page",
			emitted, publicPage.NextCursor != "")
	}

	var result rootPageReadResult
	if err := pool.InTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead}, func(tx pgx.Tx) error {
		catalog, err := schemas.EnsureCurrent(ctx, tx)
		if err != nil {
			return err
		}
		collection := catalog.Collections[root.Name]
		plan, err := scalarRootPlan(collection, nil, nil)
		if err != nil {
			return err
		}
		plan.catalogVersion, plan.principal = catalog.Version, testReadPrincipal
		traversal, err := membership.ResolveTraversal(ctx, tx, collection.ID)
		if err != nil {
			return err
		}
		result, err = records.readRootPageInTx(ctx, tx, rootPageReadRequest{
			plan: plan, limit: rootCount, operationTime: operationTime,
			traversal: traversal,
			hydrator:  newRootHydrator(tx, catalog, collection),
		})
		return err
	}); err != nil {
		t.Fatalf("large scalar root kernel: %v", err)
	}
	if result.maxRetainedRoots != emitted+1 || result.totalHydrated != emitted+1 {
		t.Fatalf("large scalar retained/hydrated/emitted = %d/%d/%d, want emitted+1/emitted+1/emitted",
			result.maxRetainedRoots, result.totalHydrated, len(result.page.Items))
	}
	for index, item := range result.page.Items {
		payload, payloadOK := item["payload"].(string)
		if !payloadOK || len(payload) != payloadBytes || !reflect.DeepEqual(item["links_a"], []string{}) ||
			!reflect.DeepEqual(item["links_b"], []string{}) {
			t.Fatalf("large scalar replay item %d payload/links = %T:%d/%#v/%#v",
				index, item["payload"], len(payload), item["links_a"], item["links_b"])
		}
	}
	if !reflect.DeepEqual(recordIDs(publicPage.Items), recordIDs(result.page.Items)) {
		t.Fatalf("large scalar public and kernel pages disagree: public=%v/%q kernel=%v/%q",
			recordIDs(publicPage.Items), publicPage.NextCursor, recordIDs(result.page.Items), result.page.NextCursor)
	}
	assertEquivalentCursorIgnoringExpiry(t, records, root.ID,
		publicPage.NextCursor, result.page.NextCursor)
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
			{Name: "payload_a", Type: schema.TypeJSONB},
			{Name: "payload_b", Type: schema.TypeJSONB},
			{Name: "payload_c", Type: schema.TypeJSONB},
			{Name: "payload_d", Type: schema.TypeJSONB},
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
		strings.Repeat("x", 12<<20),
		strings.Repeat("x", 12<<20),
		strings.Repeat("x", 12<<20),
		strings.Repeat("x", 12<<20-8*1024),
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
			candidates[index] = pageCandidate{id: uuid.MustParse(item[systemFieldID].(string)), root: item, wire: item, cursorValues: values}
		}
		return candidates
	}
	assembler := pageAssembler{
		cursors: records.cursors, order: pageOrderForTest(root.ID, effectiveSortKeys(nil)),
		expiresAt: time.Date(2026, 9, 2, 2, 0, 0, 0, time.UTC), maximumBytes: maximumRecordPageBytes,
		cursorV5: pageDescriptorForTest(effectiveSortKeys(nil)),
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
	if adjustment <= 0 {
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
	databasePayloads := make([][]byte, len(payloads))
	for index := range payloads {
		databasePayloads[index], _ = json.Marshal(payloads[index])
	}
	if _, err := pool.Exec(ctx, `INSERT INTO `+table+`
		(id, created_at, updated_at, payload_a, payload_b, payload_c, payload_d)
		VALUES ($1, $2, $2, $3::jsonb, $4::jsonb, $5::jsonb, $6::jsonb),
			($7, $8, $8, '""'::jsonb, '""'::jsonb, '""'::jsonb, '""'::jsonb)`,
		firstID, firstTime, databasePayloads[0], databasePayloads[1], databasePayloads[2], databasePayloads[3], secondID, secondTime); err != nil {
		t.Fatal(err)
	}
	syncMembershipOrdinals(t, ctx, pool)
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
		plan, err := scalarRootPlan(collection, nil, nil)
		if err != nil {
			return err
		}
		plan.catalogVersion = catalog.Version
		plan.principal = Principal{Kind: PrincipalAdministrator, ID: uuid.MustParse(r0AdministratorID)}
		traversal, err := membership.ResolveTraversal(ctx, tx, collection.ID)
		if err != nil {
			return err
		}
		kernel, err = records.readRootPageInTx(ctx, tx, rootPageReadRequest{
			plan: plan, limit: 2,
			operationTime: assembler.expiresAt.Add(-cursorTTL),
			traversal:     traversal,
			hydrator:      newRootHydrator(tx, catalog, collection),
		})
		return err
	}); err != nil {
		t.Fatalf("traced root kernel: %v", err)
	}
	if !reflect.DeepEqual(kernel.page, page) || kernel.totalHydrated != 3 || kernel.maxRetainedRoots > 2 {
		t.Fatalf("terminal recovery kernel page/hydrated/retained = %#v/%d/%d, want public page/3/<=2",
			kernel.page, kernel.totalHydrated, kernel.maxRetainedRoots)
	}
}
