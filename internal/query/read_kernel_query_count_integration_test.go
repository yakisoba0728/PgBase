//go:build integration

package query

import (
	"context"
	"fmt"
	"reflect"
	"strings"
	"sync"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/webisnoteasy/PgBase/internal/database"
	"github.com/webisnoteasy/PgBase/internal/schema"
)

const (
	queryCountScalarCeiling                 = 2
	queryCountOneBatchTwoMultiFieldsCeiling = 4
	queryCountTwoBatchTwoMultiFieldsCeiling = 7
	queryCountShallowExpandCeiling          = 5
	queryCountNestedExpandCeiling           = 6
)

type readKernelQueryTracer struct {
	mu         sync.Mutex
	enabled    bool
	statements []string
}

func (t *readKernelQueryTracer) TraceQueryStart(
	ctx context.Context,
	_ *pgx.Conn,
	data pgx.TraceQueryStartData,
) context.Context {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.enabled {
		t.statements = append(t.statements, strings.Join(strings.Fields(data.SQL), " "))
	}
	return ctx
}

func (*readKernelQueryTracer) TraceQueryEnd(context.Context, *pgx.Conn, pgx.TraceQueryEndData) {}

func (t *readKernelQueryTracer) start() {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.statements = nil
	t.enabled = true
}

func (t *readKernelQueryTracer) stop() []string {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.enabled = false
	return append([]string(nil), t.statements...)
}

func TestReadKernelQueryCountIsBoundedByPlanShapeInsteadOfRootCount(t *testing.T) {
	databaseURL, pool, schemas, records := newR0QueryServiceTest(t)
	ctx := context.Background()
	principal := Principal{Kind: PrincipalAdministrator, ID: uuid.MustParse(r0AdministratorID)}

	seedQueryCountScalarFixture(t, ctx, pool, schemas, 64)
	seedQueryCountMultiFixture(t, ctx, pool, schemas, 64)
	createExpandGraph(t, ctx, pool, schemas, records, 40)
	syncMembershipOrdinals(t, ctx, pool)

	t.Run("plain scalar stays at one root query plus traversal resolution", func(t *testing.T) {
		one := measureUnifiedListQueries(t, databaseURL, schemas, records, ListRequest{
			Principal: principal, Collection: "query_count_scalar", Limit: 1,
		})
		wide := measureUnifiedListQueries(t, databaseURL, schemas, records, ListRequest{
			Principal: principal, Collection: "query_count_scalar", Limit: 32,
		})
		assertQueryCountCeiling(t, "one scalar root", one, queryCountScalarCeiling)
		assertQueryCountCeiling(t, "32 scalar roots", wide, queryCountScalarCeiling)
		if len(one.page.Items) != 1 || len(wide.page.Items) != 32 {
			t.Fatalf("scalar emitted roots = %d/%d, want 1/32", len(one.page.Items), len(wide.page.Items))
		}
		if len(one.statements) != len(wide.statements) {
			t.Fatalf("scalar query count scaled with emitted roots: one=%d wide=%d\none: %s\nwide: %s",
				len(one.statements), len(wide.statements), formatQueryTrace(one.statements), formatQueryTrace(wide.statements))
		}
	})

	t.Run("multiple relation hydration grows only by fixed batches and fields", func(t *testing.T) {
		one := measureUnifiedListQueries(t, databaseURL, schemas, records, ListRequest{
			Principal: principal, Collection: "query_count_multi_roots", Limit: 1,
		})
		wide := measureUnifiedListQueries(t, databaseURL, schemas, records, ListRequest{
			Principal: principal, Collection: "query_count_multi_roots", Limit: 32,
		})
		assertQueryCountCeiling(t, "one root with two multiple fields", one, queryCountOneBatchTwoMultiFieldsCeiling)
		assertQueryCountCeiling(t, "32 roots with two multiple fields", wide, queryCountTwoBatchTwoMultiFieldsCeiling)
		if len(one.page.Items) != 1 || len(wide.page.Items) != 32 {
			t.Fatalf("multiple-relation emitted roots = %d/%d, want 1/32", len(one.page.Items), len(wide.page.Items))
		}
		if len(wide.statements) > 2*len(one.statements) {
			t.Fatalf("two hydration batches exceeded twice the one-batch query count: one=%d wide=%d\none: %s\nwide: %s",
				len(one.statements), len(wide.statements), formatQueryTrace(one.statements), formatQueryTrace(wide.statements))
		}
	})

	t.Run("nested expansion grows by nodes rather than roots", func(t *testing.T) {
		shallowOne := measureUnifiedListQueries(t, databaseURL, schemas, records, ListRequest{
			Principal: principal, Collection: "expand_posts", Limit: 1, Expand: "owner",
		})
		shallowWide := measureUnifiedListQueries(t, databaseURL, schemas, records, ListRequest{
			Principal: principal, Collection: "expand_posts", Limit: 32, Expand: "owner",
		})
		nestedOne := measureUnifiedListQueries(t, databaseURL, schemas, records, ListRequest{
			Principal: principal, Collection: "expand_posts", Limit: 1, Expand: "owner.labels",
		})
		nestedWide := measureUnifiedListQueries(t, databaseURL, schemas, records, ListRequest{
			Principal: principal, Collection: "expand_posts", Limit: 32, Expand: "owner.labels",
		})

		assertQueryCountCeiling(t, "shallow expand of one root", shallowOne, queryCountShallowExpandCeiling)
		assertQueryCountCeiling(t, "shallow expand of 32 roots", shallowWide, queryCountShallowExpandCeiling)
		assertQueryCountCeiling(t, "nested expand of one root", nestedOne, queryCountNestedExpandCeiling)
		assertQueryCountCeiling(t, "nested expand of 32 roots", nestedWide, queryCountNestedExpandCeiling)
		if len(shallowOne.page.Items) != 1 || len(shallowWide.page.Items) != 32 ||
			len(nestedOne.page.Items) != 1 || len(nestedWide.page.Items) != 32 {
			t.Fatalf("expanded emitted roots shallow=%d/%d nested=%d/%d, want 1/32 each",
				len(shallowOne.page.Items), len(shallowWide.page.Items),
				len(nestedOne.page.Items), len(nestedWide.page.Items))
		}
		if len(shallowOne.statements) != len(shallowWide.statements) ||
			len(nestedOne.statements) != len(nestedWide.statements) {
			t.Fatalf("expand query count scaled with emitted roots: shallow=%d/%d nested=%d/%d",
				len(shallowOne.statements), len(shallowWide.statements),
				len(nestedOne.statements), len(nestedWide.statements))
		}
		if len(nestedWide.statements) > len(shallowWide.statements)+1 {
			t.Fatalf("one nested expand node added more than one query: shallow=%d nested=%d\nshallow: %s\nnested: %s",
				len(shallowWide.statements), len(nestedWide.statements),
				formatQueryTrace(shallowWide.statements), formatQueryTrace(nestedWide.statements))
		}
	})
}

type measuredUnifiedList struct {
	page       RecordPage
	statements []string
}

func measureUnifiedListQueries(
	t *testing.T,
	databaseURL string,
	schemas *schema.Service,
	records *Service,
	request ListRequest,
) measuredUnifiedList {
	t.Helper()
	ctx := context.Background()
	publicPage, err := records.List(ctx, request)
	if err != nil {
		t.Fatalf("List(%s, limit=%d, expand=%q): %v", request.Collection, request.Limit, request.Expand, err)
	}

	tracer := &readKernelQueryTracer{}
	config, err := pgx.ParseConfig(databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	config.Tracer = tracer
	conn, err := pgx.ConnectConfig(ctx, config)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := conn.Close(ctx); err != nil {
			t.Errorf("close query-count connection: %v", err)
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
	collection, ok := catalog.Collections[request.Collection]
	if !ok {
		t.Fatalf("collection %q not found", request.Collection)
	}
	filterNode, err := parseFilterRequest(request.Filter)
	if err != nil {
		t.Fatal(err)
	}
	requestedSort, err := parseSort(request.Sort)
	if err != nil {
		t.Fatal(err)
	}

	tracer.start()
	traced, _, err := records.readListInTx(ctx, tx, catalog, collection, request, filterNode, requestedSort)
	if err != nil {
		tracer.stop()
		t.Fatalf("traced read kernel %s: %v", request.Collection, err)
	}
	statements := tracer.stop()
	if err := tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(traced.page.Items, publicPage.Items) {
		t.Fatalf("traced kernel page differs from public List\ntraced: %#v\npublic: %#v", traced.page, publicPage)
	}
	assertEquivalentCursorIgnoringExpiry(t, records, collection.ID,
		traced.page.NextCursor, publicPage.NextCursor)
	return measuredUnifiedList{page: publicPage, statements: statements}
}

func assertQueryCountCeiling(t *testing.T, name string, measured measuredUnifiedList, ceiling int) {
	t.Helper()
	count := len(measured.statements)
	if count == 0 || count > ceiling {
		t.Fatalf("%s query count = %d, want 1..%d\n%s",
			name, count, ceiling, formatQueryTrace(measured.statements))
	}
	resolverQueries := 0
	for _, statement := range measured.statements {
		if strings.HasPrefix(statement, "with pinned_schema as materialized") &&
			strings.Contains(statement, "transaction_timestamp()") {
			resolverQueries++
		}
	}
	if resolverQueries != 1 {
		t.Fatalf("%s traversal-resolution query count = %d, want exactly 1\n%s",
			name, resolverQueries, formatQueryTrace(measured.statements))
	}
}

func formatQueryTrace(statements []string) string {
	if len(statements) == 0 {
		return "(no traced statements)"
	}
	lines := make([]string, len(statements))
	for index, statement := range statements {
		lines[index] = fmt.Sprintf("%d. %s", index+1, statement)
	}
	return strings.Join(lines, "\n")
}

func seedQueryCountScalarFixture(
	t *testing.T,
	ctx context.Context,
	pool *database.Pool,
	schemas *schema.Service,
	rootCount int,
) {
	t.Helper()
	collection := applyR0QueryCollection(t, schemas, schema.CreateCollectionInput{
		Name: "query_count_scalar",
		Fields: []schema.FieldInput{
			{Name: "name", Type: schema.TypeText, Required: true},
		},
	})
	table := pgx.Identifier{"app", collection.TableName}.Sanitize()
	if tag, err := pool.Exec(ctx, `insert into `+table+` (id, created_at, updated_at, name)
		select md5('query-count-scalar-' || value::text)::uuid,
			'2026-09-02 00:00:00+00'::timestamptz + value * interval '1 microsecond',
			'2026-09-02 00:00:00+00'::timestamptz + value * interval '1 microsecond',
			'root-' || lpad(value::text, 4, '0')
		from generate_series(1, $1::integer) as generated(value)`, rootCount); err != nil {
		t.Fatal(err)
	} else if tag.RowsAffected() != int64(rootCount) {
		t.Fatalf("scalar fixture rows = %d, want %d", tag.RowsAffected(), rootCount)
	}
}

func seedQueryCountMultiFixture(
	t *testing.T,
	ctx context.Context,
	pool *database.Pool,
	schemas *schema.Service,
	rootCount int,
) {
	t.Helper()
	target := applyR0QueryCollection(t, schemas, schema.CreateCollectionInput{
		Name: "query_count_multi_targets",
		Fields: []schema.FieldInput{
			{Name: "name", Type: schema.TypeText, Required: true},
		},
	})
	maximum := 4
	root := applyR0QueryCollection(t, schemas, schema.CreateCollectionInput{
		Name: "query_count_multi_roots",
		Fields: []schema.FieldInput{
			{Name: "primary_links", Type: schema.TypeRelation, Relation: &schema.RelationInput{
				TargetCollection: target.Name, Cardinality: schema.RelationMultiple,
				MaxSelect: &maximum, OnDelete: schema.RelationRestrict,
			}},
			{Name: "secondary_links", Type: schema.TypeRelation, Relation: &schema.RelationInput{
				TargetCollection: target.Name, Cardinality: schema.RelationMultiple,
				MaxSelect: &maximum, OnDelete: schema.RelationRestrict,
			}},
		},
	})
	targetTable := pgx.Identifier{"app", target.TableName}.Sanitize()
	if _, err := pool.Exec(ctx, `insert into `+targetTable+` (id, created_at, updated_at, name) values
		(md5('query-count-target-1')::uuid, '2026-09-01 00:00:00+00', '2026-09-01 00:00:00+00', 'first'),
		(md5('query-count-target-2')::uuid, '2026-09-01 00:00:01+00', '2026-09-01 00:00:01+00', 'second')`); err != nil {
		t.Fatal(err)
	}
	rootTable := pgx.Identifier{"app", root.TableName}.Sanitize()
	if tag, err := pool.Exec(ctx, `insert into `+rootTable+` (id, created_at, updated_at)
		select md5('query-count-multi-root-' || value::text)::uuid,
			'2026-09-02 01:00:00+00'::timestamptz + value * interval '1 microsecond',
			'2026-09-02 01:00:00+00'::timestamptz + value * interval '1 microsecond'
		from generate_series(1, $1::integer) as generated(value)`, rootCount); err != nil {
		t.Fatal(err)
	} else if tag.RowsAffected() != int64(rootCount) {
		t.Fatalf("multiple-relation fixture rows = %d, want %d", tag.RowsAffected(), rootCount)
	}
	for index, field := range []string{"primary_links", "secondary_links"} {
		edgeTable := pgx.Identifier{
			"pgbase_relation", schema.NewRelationPhysicalNames(root.TableName, field).MultiTable,
		}.Sanitize()
		if tag, err := pool.Exec(ctx, `insert into `+edgeTable+` (source_id, target_id, position)
			select roots.id, md5($1)::uuid, 0 from `+rootTable+` as roots`,
			fmt.Sprintf("query-count-target-%d", index+1)); err != nil {
			t.Fatal(err)
		} else if tag.RowsAffected() != int64(rootCount) {
			t.Fatalf("%s fixture edges = %d, want %d", field, tag.RowsAffected(), rootCount)
		}
	}
}
