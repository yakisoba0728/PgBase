package relation

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/webisnoteasy/PgBase/internal/schema"
	"github.com/webisnoteasy/PgBase/internal/testutil"
)

func TestDeleteGraphLivePostgreSQL(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	fixture := newDeleteLiveFixture(t, ctx)
	defer fixture.conn.Close(ctx)

	t.Run("cascade set-null self cycle diamond and internal restrict", func(t *testing.T) {
		fixture.reset(t, ctx)
		fixture.seedComplexGraph(t, ctx)
		tx, err := fixture.conn.Begin(ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer tx.Rollback(ctx)
		plan, err := fixture.graph.Plan(ctx, tx, fixture.ref(fixture.root))
		if err != nil {
			t.Fatalf("plan complex delete: %v", err)
		}
		wantDeletes := []RecordRef{
			fixture.ref(fixture.root), fixture.ref(fixture.a), fixture.ref(fixture.b),
			fixture.ref(fixture.c), fixture.ref(fixture.d), fixture.ref(fixture.e),
		}
		sortRecordRefs(wantDeletes)
		if got := plan.Deletes(); !reflect.DeepEqual(got, wantDeletes) {
			t.Fatalf("unexpected cascade closure:\n got=%v\nwant=%v", got, wantDeletes)
		}
		clears := plan.Clears()
		if len(clears) != 2 {
			t.Fatalf("clear count=%d, want multi+single: %#v", len(clears), clears)
		}
		byField := make(map[string]Clear, len(clears))
		for _, clear := range clears {
			byField[clear.FieldName] = clear
			if clear.Source != fixture.ref(fixture.survivor) {
				t.Fatalf("unexpected clear source: %#v", clear)
			}
		}
		wantMultiTargets := []uuid.UUID{fixture.root, fixture.a}
		sortUUIDs(wantMultiTargets)
		if !reflect.DeepEqual(byField["null_links"].TargetIDs, wantMultiTargets) {
			t.Fatalf("unexpected multi clear targets: %v", byField["null_links"].TargetIDs)
		}
		if !reflect.DeepEqual(byField["single_null"].TargetIDs, []uuid.UUID{fixture.root}) {
			t.Fatalf("unexpected single clear targets: %v", byField["single_null"].TargetIDs)
		}
		updates := plan.Updates()
		if len(updates) != 1 || updates[0].Source != fixture.ref(fixture.survivor) ||
			!reflect.DeepEqual(updates[0].ChangedFields, []string{"null_links", "single_null"}) {
			t.Fatalf("unexpected record-level updates: %#v", updates)
		}

		if err := plan.ExecuteStorage(ctx); err != nil {
			t.Fatalf("execute relation cleanup: %v", err)
		}
		for _, record := range plan.Deletes() {
			if _, err := tx.Exec(ctx, `delete from app.items where id = $1`, record.RecordID); err != nil {
				t.Fatalf("delete base record: %v", err)
			}
		}
		if err := tx.Commit(ctx); err != nil {
			t.Fatalf("commit complex delete: %v", err)
		}

		if got := fixture.itemIDs(t, ctx); !reflect.DeepEqual(got, []uuid.UUID{fixture.survivor, fixture.outside}) {
			t.Fatalf("unexpected surviving records: %v", got)
		}
		var singleNull uuid.NullUUID
		if err := fixture.conn.QueryRow(ctx, `select single_null from app.items where id = $1`, fixture.survivor).Scan(&singleNull); err != nil {
			t.Fatal(err)
		}
		if singleNull.Valid {
			t.Fatalf("single set-null was not applied: %v", singleNull.UUID)
		}
		nullEdges := fixture.readEdges(t, ctx, "null_links", fixture.survivor)
		if !reflect.DeepEqual(nullEdges, []edgeValue{{target: fixture.outside, position: 0}}) {
			t.Fatalf("multi set-null did not preserve/compact survivors: %#v", nullEdges)
		}
		for _, field := range []string{"cascade_links", "restrict_links"} {
			if count := fixture.edgeCount(t, ctx, field); count != 0 {
				t.Fatalf("internal %s edges remain: %d", field, count)
			}
		}
	})

	t.Run("single and multi restrict conflicts roll back", func(t *testing.T) {
		for _, cardinality := range []string{"single", "multi"} {
			t.Run(cardinality, func(t *testing.T) {
				fixture.reset(t, ctx)
				fixture.insertItem(t, ctx, fixture.root, nil, nil, nil)
				if cardinality == "single" {
					fixture.insertItem(t, ctx, fixture.survivor, nil, &fixture.root, nil)
				} else {
					fixture.insertItem(t, ctx, fixture.survivor, nil, nil, nil)
					fixture.insertEdge(t, ctx, "restrict_links", fixture.survivor, fixture.root, 0)
				}
				tx, err := fixture.conn.Begin(ctx)
				if err != nil {
					t.Fatal(err)
				}
				if _, err := tx.Exec(ctx, `update app.items set touched = true where id = $1`, fixture.root); err != nil {
					t.Fatal(err)
				}
				_, err = fixture.graph.Plan(ctx, tx, fixture.ref(fixture.root))
				assertValidation(t, err, "id", "relation_delete_restricted")
				if err := tx.Rollback(ctx); err != nil {
					t.Fatal(err)
				}
				if fixture.touched(t, ctx, fixture.root) {
					t.Fatal("restrict failure did not roll back caller changes")
				}
				if got := fixture.itemIDs(t, ctx); len(got) != 2 {
					t.Fatalf("restrict failure removed records: %v", got)
				}
			})
		}
	})

	t.Run("restrict source discovered later by cascade is internal", func(t *testing.T) {
		fixture.reset(t, ctx)
		fixture.insertItem(t, ctx, fixture.root, nil, nil, nil)
		fixture.insertItem(t, ctx, fixture.a, nil, nil, nil)
		fixture.insertItem(t, ctx, fixture.survivor, nil, nil, nil)
		fixture.insertEdge(t, ctx, "cascade_links", fixture.a, fixture.root, 0)
		fixture.insertEdge(t, ctx, "cascade_links", fixture.survivor, fixture.a, 0)
		fixture.insertEdge(t, ctx, "restrict_links", fixture.survivor, fixture.root, 0)
		tx, err := fixture.conn.Begin(ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer tx.Rollback(ctx)
		plan, err := fixture.graph.Plan(ctx, tx, fixture.ref(fixture.root))
		if err != nil {
			t.Fatalf("delayed internal restrict was rejected: %v", err)
		}
		if len(plan.Deletes()) != 3 || len(plan.Clears()) != 0 {
			t.Fatalf("unexpected delayed closure: deletes=%v clears=%v", plan.Deletes(), plan.Clears())
		}
	})

	t.Run("depth records and edge budgets fail closed", func(t *testing.T) {
		tests := []struct {
			name   string
			limits deleteLimits
			seed   func(*testing.T)
		}{
			{
				name:   "depth",
				limits: deleteLimits{depth: 1, records: 10, edges: 10},
				seed: func(t *testing.T) {
					fixture.insertItem(t, ctx, fixture.root, nil, nil, nil)
					fixture.insertItem(t, ctx, fixture.a, nil, nil, nil)
					fixture.insertItem(t, ctx, fixture.b, nil, nil, nil)
					fixture.insertEdge(t, ctx, "cascade_links", fixture.a, fixture.root, 0)
					fixture.insertEdge(t, ctx, "cascade_links", fixture.b, fixture.a, 0)
				},
			},
			{
				name:   "affected records include set-null survivors",
				limits: deleteLimits{depth: 5, records: 2, edges: 10},
				seed: func(t *testing.T) {
					fixture.insertItem(t, ctx, fixture.root, nil, nil, nil)
					fixture.insertItem(t, ctx, fixture.a, nil, nil, nil)
					fixture.insertItem(t, ctx, fixture.survivor, nil, nil, nil)
					fixture.insertEdge(t, ctx, "cascade_links", fixture.a, fixture.root, 0)
					fixture.insertEdge(t, ctx, "null_links", fixture.survivor, fixture.root, 0)
				},
			},
			{
				name:   "edges",
				limits: deleteLimits{depth: 5, records: 10, edges: 1},
				seed: func(t *testing.T) {
					fixture.insertItem(t, ctx, fixture.root, nil, nil, nil)
					fixture.insertItem(t, ctx, fixture.a, nil, nil, nil)
					fixture.insertItem(t, ctx, fixture.d, nil, nil, nil)
					fixture.insertEdge(t, ctx, "cascade_links", fixture.a, fixture.root, 0)
					fixture.insertEdge(t, ctx, "cascade_links", fixture.d, fixture.root, 0)
				},
			},
		}
		for _, test := range tests {
			t.Run(test.name, func(t *testing.T) {
				fixture.reset(t, ctx)
				test.seed(t)
				graph, err := BindDeleteGraph(fixture.catalog)
				if err != nil {
					t.Fatal(err)
				}
				graph.limits = test.limits
				tx, err := fixture.conn.Begin(ctx)
				if err != nil {
					t.Fatal(err)
				}
				if _, err := tx.Exec(ctx, `update app.items set touched = true where id = $1`, fixture.root); err != nil {
					t.Fatal(err)
				}
				_, err = graph.Plan(ctx, tx, fixture.ref(fixture.root))
				assertValidation(t, err, "id", "relation_delete_budget_exceeded")
				if err := tx.Rollback(ctx); err != nil {
					t.Fatal(err)
				}
				if fixture.touched(t, ctx, fixture.root) {
					t.Fatal("budget failure did not roll back caller changes")
				}
			})
		}
	})

	t.Run("exact depth record and edge limits are accepted", func(t *testing.T) {
		fixture.reset(t, ctx)
		fixture.insertItem(t, ctx, fixture.root, nil, nil, nil)
		fixture.insertItem(t, ctx, fixture.a, nil, nil, nil)
		fixture.insertItem(t, ctx, fixture.b, nil, nil, nil)
		fixture.insertEdge(t, ctx, "cascade_links", fixture.a, fixture.root, 0)
		fixture.insertEdge(t, ctx, "cascade_links", fixture.b, fixture.a, 0)
		graph, err := BindDeleteGraph(fixture.catalog)
		if err != nil {
			t.Fatal(err)
		}
		graph.limits = deleteLimits{depth: 2, records: 3, edges: 2}
		tx, err := fixture.conn.Begin(ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer tx.Rollback(ctx)
		plan, err := graph.Plan(ctx, tx, fixture.ref(fixture.root))
		if err != nil || len(plan.Deletes()) != 3 {
			t.Fatalf("exact limits rejected: plan=%v err=%v", plan.Deletes(), err)
		}
	})

	t.Run("non-read-committed transactions fail before discovery", func(t *testing.T) {
		fixture.reset(t, ctx)
		fixture.insertItem(t, ctx, fixture.root, nil, nil, nil)
		tx, err := fixture.conn.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead})
		if err != nil {
			t.Fatal(err)
		}
		defer tx.Rollback(ctx)
		if err := tx.QueryRow(ctx, `select count(*) from app.items`).Scan(new(int)); err != nil {
			t.Fatal(err)
		}
		_, err = fixture.graph.Plan(ctx, tx, fixture.ref(fixture.root))
		if !errors.Is(err, ErrDeleteIsolation) {
			t.Fatalf("repeatable-read delete did not fail closed: %v", err)
		}
	})

	t.Run("missing root is distinct from hidden target misses", func(t *testing.T) {
		fixture.reset(t, ctx)
		tx, err := fixture.conn.Begin(ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer tx.Rollback(ctx)
		_, err = fixture.graph.Plan(ctx, tx, fixture.ref(fixture.root))
		if !errors.Is(err, ErrSourceNotFound) {
			t.Fatalf("missing root error=%v", err)
		}
	})
}

type deleteLiveFixture struct {
	url        string
	conn       *pgx.Conn
	catalog    schema.Catalog
	collection schema.Collection
	graph      *DeleteGraph
	root       uuid.UUID
	a          uuid.UUID
	b          uuid.UUID
	c          uuid.UUID
	d          uuid.UUID
	e          uuid.UUID
	survivor   uuid.UUID
	outside    uuid.UUID
}

func newDeleteLiveFixture(t *testing.T, ctx context.Context) *deleteLiveFixture {
	t.Helper()
	databaseURL := testutil.NewDatabase(t)
	conn, err := pgx.Connect(ctx, databaseURL)
	if err != nil {
		t.Fatalf("connect delete graph database: %v", err)
	}
	catalog, collection := deleteCatalogFixture()
	graph, err := BindDeleteGraph(catalog)
	if err != nil {
		conn.Close(ctx)
		t.Fatalf("bind live delete graph: %v", err)
	}
	fixture := &deleteLiveFixture{
		url: databaseURL, conn: conn, catalog: catalog, collection: collection, graph: graph,
		root:     uuid.MustParse("0199aa23-0000-7000-8000-000000000101"),
		a:        uuid.MustParse("0199aa23-0000-7000-8000-000000000102"),
		b:        uuid.MustParse("0199aa23-0000-7000-8000-000000000103"),
		c:        uuid.MustParse("0199aa23-0000-7000-8000-000000000104"),
		d:        uuid.MustParse("0199aa23-0000-7000-8000-000000000105"),
		e:        uuid.MustParse("0199aa23-0000-7000-8000-000000000106"),
		survivor: uuid.MustParse("0199aa23-0000-7000-8000-000000000107"),
		outside:  uuid.MustParse("0199aa23-0000-7000-8000-000000000108"),
	}
	if _, err := conn.Exec(ctx, `create schema app; create schema pgbase_relation;
		create table app.items (
			id uuid primary key,
			touched boolean not null default false,
			single_cascade uuid,
			single_restrict uuid,
			single_null uuid
		)`); err != nil {
		conn.Close(ctx)
		t.Fatalf("create delete graph tables: %v", err)
	}
	for _, name := range []string{"single_cascade", "single_restrict", "single_null"} {
		if _, err := conn.Exec(ctx, "alter table app.items add foreign key ("+pgx.Identifier{name}.Sanitize()+
			") references app.items(id) on delete no action deferrable initially deferred"); err != nil {
			conn.Close(ctx)
			t.Fatalf("create single relation FK: %v", err)
		}
	}
	for _, field := range collection.Fields[:3] {
		if _, err := conn.Exec(ctx, createDeleteEdgeTableSQL(field.Relation.Storage.Name, "items", "items")); err != nil {
			conn.Close(ctx)
			t.Fatalf("create multi relation table: %v", err)
		}
	}
	return fixture
}

func createDeleteEdgeTableSQL(table, source, target string) string {
	return "create table " + pgx.Identifier{"pgbase_relation", table}.Sanitize() + ` (
		source_id uuid not null references ` + pgx.Identifier{"app", source}.Sanitize() + `(id) on delete cascade,
		target_id uuid not null references ` + pgx.Identifier{"app", target}.Sanitize() + `(id) on delete no action deferrable initially deferred,
		position integer not null check (position >= 0),
		primary key (source_id, target_id), unique (source_id, position))`
}

func (f *deleteLiveFixture) ref(id uuid.UUID) RecordRef {
	return RecordRef{CollectionID: f.collection.ID, RecordID: id}
}

func (f *deleteLiveFixture) reset(t *testing.T, ctx context.Context) {
	t.Helper()
	if _, err := f.conn.Exec(ctx, `truncate table app.items cascade`); err != nil {
		t.Fatalf("reset delete graph fixture: %v", err)
	}
}

func (f *deleteLiveFixture) insertItem(
	t *testing.T,
	ctx context.Context,
	id uuid.UUID,
	cascade, restrict, setNull *uuid.UUID,
) {
	t.Helper()
	if _, err := f.conn.Exec(ctx, `insert into app.items
		(id, single_cascade, single_restrict, single_null) values ($1, $2, $3, $4)`, id, cascade, restrict, setNull); err != nil {
		t.Fatalf("insert delete graph item: %v", err)
	}
}

func (f *deleteLiveFixture) insertEdge(
	t *testing.T,
	ctx context.Context,
	field string,
	source, target uuid.UUID,
	position int,
) {
	t.Helper()
	table := f.relationField(t, field).Relation.Storage.Name
	if _, err := f.conn.Exec(ctx, "insert into "+pgx.Identifier{"pgbase_relation", table}.Sanitize()+
		" (source_id, target_id, position) values ($1, $2, $3)", source, target, position); err != nil {
		t.Fatalf("insert %s edge: %v", field, err)
	}
}

func (f *deleteLiveFixture) relationField(t *testing.T, name string) schema.Field {
	t.Helper()
	for _, field := range f.collection.Fields {
		if field.Name == name {
			return field
		}
	}
	t.Fatalf("relation field %q not found", name)
	return schema.Field{}
}

func (f *deleteLiveFixture) seedComplexGraph(t *testing.T, ctx context.Context) {
	t.Helper()
	f.insertItem(t, ctx, f.root, nil, nil, nil)
	f.insertItem(t, ctx, f.a, nil, &f.root, nil)
	f.insertItem(t, ctx, f.b, nil, nil, nil)
	f.insertItem(t, ctx, f.c, nil, nil, nil)
	f.insertItem(t, ctx, f.d, nil, nil, nil)
	f.insertItem(t, ctx, f.e, nil, nil, nil)
	f.insertItem(t, ctx, f.survivor, nil, nil, &f.root)
	f.insertItem(t, ctx, f.outside, nil, nil, nil)
	if _, err := f.conn.Exec(ctx, `update app.items set single_cascade = case id
		when $1 then $2::uuid when $2 then $1::uuid else single_cascade end
		where id in ($1, $2)`, f.root, f.e); err != nil {
		t.Fatalf("create single cascade cycle: %v", err)
	}

	f.insertEdge(t, ctx, "cascade_links", f.a, f.root, 0)
	f.insertEdge(t, ctx, "cascade_links", f.d, f.root, 0)
	f.insertEdge(t, ctx, "cascade_links", f.c, f.a, 0)
	f.insertEdge(t, ctx, "cascade_links", f.c, f.d, 1)
	f.insertEdge(t, ctx, "cascade_links", f.b, f.a, 0)
	f.insertEdge(t, ctx, "cascade_links", f.root, f.root, 0)
	f.insertEdge(t, ctx, "restrict_links", f.a, f.root, 0)
	f.insertEdge(t, ctx, "null_links", f.survivor, f.root, 0)
	f.insertEdge(t, ctx, "null_links", f.survivor, f.a, 1)
	f.insertEdge(t, ctx, "null_links", f.survivor, f.outside, 2)
	f.insertEdge(t, ctx, "null_links", f.a, f.outside, 0)
}

type edgeValue struct {
	target   uuid.UUID
	position int
}

func (f *deleteLiveFixture) readEdges(t *testing.T, ctx context.Context, field string, source uuid.UUID) []edgeValue {
	t.Helper()
	table := f.relationField(t, field).Relation.Storage.Name
	rows, err := f.conn.Query(ctx, "select target_id, position from "+pgx.Identifier{"pgbase_relation", table}.Sanitize()+
		" where source_id = $1 order by position", source)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var values []edgeValue
	for rows.Next() {
		var value edgeValue
		if err := rows.Scan(&value.target, &value.position); err != nil {
			t.Fatal(err)
		}
		values = append(values, value)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return values
}

func (f *deleteLiveFixture) edgeCount(t *testing.T, ctx context.Context, field string) int {
	t.Helper()
	table := f.relationField(t, field).Relation.Storage.Name
	var count int
	if err := f.conn.QueryRow(ctx, "select count(*) from "+pgx.Identifier{"pgbase_relation", table}.Sanitize()).Scan(&count); err != nil {
		t.Fatal(err)
	}
	return count
}

func (f *deleteLiveFixture) itemIDs(t *testing.T, ctx context.Context) []uuid.UUID {
	t.Helper()
	rows, err := f.conn.Query(ctx, `select id from app.items order by id`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var ids []uuid.UUID
	for rows.Next() {
		var id uuid.UUID
		if err := rows.Scan(&id); err != nil {
			t.Fatal(err)
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return ids
}

func (f *deleteLiveFixture) touched(t *testing.T, ctx context.Context, id uuid.UUID) bool {
	t.Helper()
	var touched bool
	if err := f.conn.QueryRow(ctx, `select touched from app.items where id = $1`, id).Scan(&touched); err != nil {
		t.Fatal(err)
	}
	return touched
}
