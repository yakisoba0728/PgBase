//go:build integration

package query

import (
	"context"
	"sort"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/yakisoba0728/PgBase/internal/database"
	"github.com/yakisoba0728/PgBase/internal/schema"
)

// tracedList runs the public List inside one traced repeatable-read
// transaction and returns the page with the statements it issued.
func tracedList(t *testing.T, databaseURL string, records *Service, request ListRequest) (RecordPage, []string) {
	t.Helper()
	ctx := context.Background()
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
	defer conn.Close(ctx)
	tx, err := conn.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	tracer.start()
	page, err := records.List(database.WithTransaction(ctx, tx), request)
	statements := tracer.stop()
	if err != nil {
		t.Fatalf("List(%+v): %v", request, err)
	}
	return page, statements
}

func countStatements(statements []string, prefix string) int {
	count := 0
	for _, statement := range statements {
		if strings.HasPrefix(strings.ToLower(statement), strings.ToLower(prefix)) {
			count++
		}
	}
	return count
}

func TestListTotalSkipsCountOnlyWhenThePageIsTheWholeResult(t *testing.T) {
	databaseURL, pool, schemas, records := newR0QueryServiceTest(t)
	ctx := context.Background()
	seedQueryCountScalarFixture(t, ctx, pool, schemas, 64)
	syncMembershipOrdinals(t, ctx, pool)
	list := func(request ListRequest) ListRequest {
		request.Principal, request.Collection, request.Total = testReadPrincipal, "query_count_scalar", true
		return request
	}

	first, _ := tracedList(t, databaseURL, records, list(ListRequest{Limit: 32}))
	for _, test := range []struct {
		name    string
		request ListRequest
		total   int64
		counted bool
	}{
		{"sparse first page", list(ListRequest{Limit: 200}), 64, false},
		{"exactly the page size", list(ListRequest{Limit: 64}), 64, false},
		{"one short of the page size", list(ListRequest{Limit: 63}), 64, true},
		{"filtered sparse page", list(ListRequest{Limit: 200, Filter: "name ~ '-004'"}), 10, false},
		{"empty first page", list(ListRequest{Limit: 200, Filter: "name = 'none'"}), 0, false},
		{"continuation page", list(ListRequest{Limit: 200, Cursor: first.NextCursor}), 64, true},
		{"random sort", list(ListRequest{Limit: 200, Sort: "@random"}), 64, true},
	} {
		page, statements := tracedList(t, databaseURL, records, test.request)
		if page.TotalItems == nil || *page.TotalItems != test.total || page.TotalCapped {
			t.Fatalf("%s: total = %v capped=%v, want %d", test.name, page.TotalItems, page.TotalCapped, test.total)
		}
		if counted := countStatements(statements, "SELECT COUNT(") == 1; counted != test.counted {
			t.Fatalf("%s: count statement ran = %v, want %v\n%s", test.name, counted, test.counted, formatQueryTrace(statements))
		}
	}

	counted := false
	records.countPageHook = func() { counted = true }
	page, err := ListPocketBase(ctx, records, PocketBaseListRequest{
		ListRequest: ListRequest{Principal: testReadPrincipal, Collection: "query_count_scalar"}, Page: 1, PerPage: 100,
	})
	if err != nil || counted || page.TotalItems != 64 || page.TotalPages != 1 || len(page.Items) != 64 {
		t.Fatalf("compat sparse page total=%d pages=%d items=%d counted=%v err=%v", page.TotalItems, page.TotalPages, len(page.Items), counted, err)
	}
	page, err = ListPocketBase(ctx, records, PocketBaseListRequest{
		ListRequest: ListRequest{Principal: testReadPrincipal, Collection: "query_count_scalar"}, Page: 3, PerPage: 30,
	})
	if err != nil || counted || page.TotalItems != 64 || page.TotalPages != 3 || len(page.Items) != 4 {
		t.Fatalf("compat last page total=%d pages=%d items=%d counted=%v err=%v", page.TotalItems, page.TotalPages, len(page.Items), counted, err)
	}
	page, err = ListPocketBase(ctx, records, PocketBaseListRequest{
		ListRequest: ListRequest{Principal: testReadPrincipal, Collection: "query_count_scalar"}, Page: 1, PerPage: 10,
	})
	if err != nil || !counted || page.TotalItems != 64 || page.TotalPages != 7 || len(page.Items) != 10 {
		t.Fatalf("compat full page total=%d pages=%d items=%d counted=%v err=%v", page.TotalItems, page.TotalPages, len(page.Items), counted, err)
	}
	counted = false
	page, err = ListPocketBase(ctx, records, PocketBaseListRequest{
		ListRequest: ListRequest{Principal: testReadPrincipal, Collection: "query_count_scalar"}, Page: 9, PerPage: 30,
	})
	if err != nil || !counted || page.TotalItems != 64 || len(page.Items) != 0 {
		t.Fatalf("compat page past the end total=%d items=%d counted=%v err=%v", page.TotalItems, len(page.Items), counted, err)
	}
}

func TestSequentialHydrationTakesTheRestOfASmallRowPageInOneBatch(t *testing.T) {
	databaseURL, pool, schemas, records := newR0QueryServiceTest(t)
	ctx := context.Background()
	seedQueryCountMultiFixture(t, ctx, pool, schemas, 200)
	syncMembershipOrdinals(t, ctx, pool)
	principal := Principal{Kind: PrincipalAdministrator, ID: uuid.MustParse(r0AdministratorID)}
	measured := measureUnifiedListQueries(t, databaseURL, schemas, records, ListRequest{
		Principal: principal, Collection: "query_count_multi_roots", Limit: 200,
	})
	// One 16-row probe, then the remaining 184 in one batch: two root reads and
	// two hydration reads where 16-row batches took thirteen of each.
	hydrations := countStatements(measured.statements, "with requested(source_id, source_ordinal)")
	if hydrations != 2 || len(measured.page.Items) != 200 || measured.page.NextCursor != "" {
		t.Fatalf("hydration statements = %d, items = %d, cursor = %t; want 2/200/false\n%s",
			hydrations, len(measured.page.Items), measured.page.NextCursor != "", formatQueryTrace(measured.statements))
	}
	t.Logf("200-row multi-relation page: %d statements in the read kernel", len(measured.statements))
}

func TestSequentialHydrationWidenedBatchStopsAtTheByteBudget(t *testing.T) {
	databaseURL, pool, schemas, records := newR0QueryServiceTest(t)
	ctx := context.Background()
	applyR0QueryCollection(t, schemas, schema.CreateCollectionInput{
		Name: "byte_bound_targets", Fields: []schema.FieldInput{{Name: "name", Type: schema.TypeText}},
	})
	maximum := 2
	root := applyR0QueryCollection(t, schemas, schema.CreateCollectionInput{
		Name: "byte_bound_roots",
		Fields: []schema.FieldInput{
			{Name: "payload", Type: schema.TypeEditor},
			{Name: "links", Type: schema.TypeRelation, Relation: &schema.RelationInput{
				TargetCollection: "byte_bound_targets", Cardinality: schema.RelationMultiple,
				MaxSelect: &maximum, OnDelete: schema.RelationRestrict,
			}},
		},
	})
	// Twenty small rows let the probe widen the next batch to the rest of the
	// page; the forty 2 MB rows behind them exceed the 48 MB page budget.
	const small, large, largeBytes = 20, 40, 2 << 20
	table := pgx.Identifier{"app", root.TableName}.Sanitize()
	if _, err := pool.Exec(ctx, `INSERT INTO `+table+` (id, created_at, updated_at, payload)
		SELECT md5('byte-bound-root-' || value::text)::uuid,
			'2026-01-03 00:00:00+00'::timestamptz + value * interval '1 microsecond',
			'2026-01-03 00:00:00+00'::timestamptz + value * interval '1 microsecond',
			CASE WHEN value <= $1 THEN 'small' ELSE repeat('x', $3::integer) END
		FROM generate_series(1, $1::integer + $2::integer) AS generated(value)`, small, large, largeBytes); err != nil {
		t.Fatal(err)
	}
	syncMembershipOrdinals(t, ctx, pool)

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
	defer conn.Close(ctx)
	tx, err := conn.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	principal := Principal{Kind: PrincipalAdministrator, ID: uuid.MustParse(r0AdministratorID)}
	first, err := records.List(database.WithTransaction(ctx, tx), ListRequest{Principal: principal, Collection: root.Name, Limit: 200})
	if err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	// Unbounded, the widened batch would request all 44 rows after the probe.
	budgetRows := small - 16 + (48<<20)/largeBytes + 1
	if trace.queryCount != 2 || trace.rootCount > 16+budgetRows {
		t.Fatalf("hydration queries/requested roots = %d/%d, want 2/<=%d", trace.queryCount, trace.rootCount, 16+budgetRows)
	}
	if first.NextCursor == "" || len(first.Items) <= small || len(first.Items) >= small+large {
		t.Fatalf("first page items/cursor = %d/%t, want a byte-trimmed page", len(first.Items), first.NextCursor != "")
	}
	seen := recordIDs(first.Items)
	for cursor := first.NextCursor; cursor != ""; {
		page, err := records.List(ctx, ListRequest{Principal: principal, Collection: root.Name, Limit: 200, Cursor: cursor})
		if err != nil {
			t.Fatal(err)
		}
		seen = append(seen, recordIDs(page.Items)...)
		cursor = page.NextCursor
	}
	rows, err := pool.Query(ctx, `SELECT id::text FROM `+table+` ORDER BY created_at, id`)
	if err != nil {
		t.Fatal(err)
	}
	want, err := pgx.CollectRows(rows, pgx.RowTo[string])
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(seen, ",") != strings.Join(want, ",") {
		t.Fatalf("cursor walk returned %d rows out of order or with gaps, want %d", len(seen), len(want))
	}
}

func TestContainsLikeMatchesStrposSemantics(t *testing.T) {
	_, pool, schemas, records := newR0QueryServiceTest(t)
	ctx := context.Background()
	collection := applyR0QueryCollection(t, schemas, schema.CreateCollectionInput{
		Name:   "contains_semantics",
		Fields: []schema.FieldInput{{Name: "title", Type: schema.TypeText}},
	})
	table := pgx.Identifier{"app", collection.TableName}.Sanitize()
	values := []any{"50% off", "a_b", "axb", `back\slash`, `a\%b`, "Abc", "abc", "ÄBC straße", "", nil}
	for _, value := range values {
		if _, err := pool.Exec(ctx, `insert into `+table+` (id, created_at, updated_at, title) values ($1, now(), now(), $2)`, uuid.New(), value); err != nil {
			t.Fatal(err)
		}
	}
	syncMembershipOrdinals(t, ctx, pool)
	for _, needle := range []string{"%", "_", `\`, "a_b", `\%`, "A", "abc", "straße", "ä", ""} {
		for _, test := range []struct{ filter, where string }{
			{"title ~ ", "strpos(title, $1) > 0"},
			{"title !~ ", "NOT (strpos(title, $1) > 0)"},
			{"title:lower ~ ", "strpos(lower(title), $1) > 0"},
		} {
			rows, err := pool.Query(ctx, `select id::text from `+table+` where `+test.where, needle)
			if err != nil {
				t.Fatal(err)
			}
			want, err := pgx.CollectRows(rows, pgx.RowTo[string])
			if err != nil {
				t.Fatal(err)
			}
			page, err := records.List(ctx, ListRequest{
				Principal: testReadPrincipal, Collection: collection.Name, Limit: 200,
				Filter: test.filter + "'" + strings.ReplaceAll(needle, "'", `\'`) + "'",
			})
			if err != nil {
				t.Fatalf("%s%q: %v", test.filter, needle, err)
			}
			got := make([]string, 0, len(page.Items))
			for _, item := range page.Items {
				got = append(got, item["id"].(string))
			}
			sort.Strings(got)
			sort.Strings(want)
			if strings.Join(got, ",") != strings.Join(want, ",") {
				t.Fatalf("%s%q matched %v, strpos matched %v", test.filter, needle, got, want)
			}
		}
	}
}
