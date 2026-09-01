//go:build integration

package relation

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/webisnoteasy/PgBase/internal/schema"
	"github.com/webisnoteasy/PgBase/internal/testutil"
)

func TestRelationStorageLivePostgreSQL(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	databaseURL := testutil.NewDatabase(t)
	fixture := newLiveFixture(t, ctx, databaseURL)
	defer fixture.conn.Close(ctx)

	t.Run("missing target has one stable non-oracle error", func(t *testing.T) {
		missing := uuid.MustParse("0199aa21-0000-7000-8000-00000000ffff")
		input := fixture.decode(t, map[string]json.RawMessage{
			"labels": json.RawMessage(`["` + fixture.targetOne.String() + `","` + missing.String() + `"]`),
		})
		tx, err := fixture.conn.Begin(ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer tx.Rollback(ctx)
		if err := fixture.bindings.LockCollections(ctx, tx, input); err != nil {
			t.Fatalf("lock collections: %v", err)
		}
		err = fixture.bindings.LockRows(ctx, tx, fixture.sourceOne, SourceStored, input)
		assertValidation(t, err, "labels", "relation_target_invalid")
		if strings.Contains(err.Error(), missing.String()) || strings.Contains(err.Error(), "targets") {
			t.Fatalf("missing-target error disclosed existence details: %q", err)
		}
	})

	t.Run("opposite value order uses one target batch", func(t *testing.T) {
		tracer := &relationQueryTracer{}
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
		input := fixture.decode(t, map[string]json.RawMessage{
			"labels": json.RawMessage(`["` + fixture.targetTwo.String() + `","` + fixture.targetOne.String() + `"]`),
			"groups": json.RawMessage(`["` + fixture.targetOne.String() + `","` + fixture.targetTwo.String() + `"]`),
		})
		tx, err := conn.Begin(ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer tx.Rollback(ctx)
		tracer.Reset()
		if err := fixture.bindings.LockRows(ctx, tx, fixture.sourceOne, SourceStored, input); err != nil {
			t.Fatalf("lock batched rows: %v", err)
		}
		if got := tracer.RowLockQueries(); got != 2 {
			t.Fatalf("LockRows issued %d row-lock queries; want source UPDATE + one target batch", got)
		}
	})

	t.Run("replace preserves positions and rollback restores every field", func(t *testing.T) {
		fixture.resetEdges(t, ctx)
		input := fixture.decode(t, map[string]json.RawMessage{
			"labels": json.RawMessage(`["` + fixture.targetTwo.String() + `","` + fixture.targetOne.String() + `"]`),
			"groups": json.RawMessage(`[]`),
		})
		tx, err := fixture.conn.Begin(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if err := fixture.bindings.LockCollections(ctx, tx, input); err != nil {
			t.Fatal(err)
		}
		if err := fixture.bindings.LockRows(ctx, tx, fixture.sourceOne, SourceStored, input); err != nil {
			t.Fatal(err)
		}
		if err := fixture.bindings.ReplaceMulti(ctx, tx, fixture.sourceOne, input); err != nil {
			t.Fatalf("replace multi: %v", err)
		}
		if got := readTargets(t, ctx, tx, fixture.labelTable, fixture.sourceOne); !reflect.DeepEqual(got, []uuid.UUID{fixture.targetTwo, fixture.targetOne}) {
			t.Fatalf("replacement order changed: %v", got)
		}
		if got := readTargets(t, ctx, tx, fixture.groupTable, fixture.sourceOne); len(got) != 0 {
			t.Fatalf("explicit clear retained groups: %v", got)
		}
		if err := tx.Rollback(ctx); err != nil {
			t.Fatal(err)
		}
		if got := readTargets(t, ctx, fixture.conn, fixture.labelTable, fixture.sourceOne); !reflect.DeepEqual(got, []uuid.UUID{fixture.targetOne}) {
			t.Fatalf("rollback did not restore labels: %v", got)
		}
		if got := readTargets(t, ctx, fixture.conn, fixture.groupTable, fixture.sourceOne); !reflect.DeepEqual(got, []uuid.UUID{fixture.targetThree}) {
			t.Fatalf("rollback did not restore groups: %v", got)
		}
	})

	t.Run("hydrate is one ordered query per field", func(t *testing.T) {
		fixture.resetEdges(t, ctx)
		counter := &countingRowsQuerier{delegate: fixture.conn}
		records := map[uuid.UUID]map[string]any{
			fixture.sourceOne: {"id": fixture.sourceOne.String()},
			fixture.sourceTwo: {"id": fixture.sourceTwo.String()},
		}
		if err := fixture.bindings.Hydrate(ctx, counter,
			[]uuid.UUID{fixture.sourceTwo, fixture.sourceOne, fixture.sourceOne}, records); err != nil {
			t.Fatalf("hydrate: %v", err)
		}
		if counter.queries != 2 {
			t.Fatalf("hydrate query count = %d; want one for each of 2 multi fields", counter.queries)
		}
		if got := records[fixture.sourceOne]["labels"]; !reflect.DeepEqual(got, []string{fixture.targetOne.String()}) {
			t.Fatalf("unexpected labels: %#v", got)
		}
		if got := records[fixture.sourceOne]["groups"]; !reflect.DeepEqual(got, []string{fixture.targetThree.String()}) {
			t.Fatalf("unexpected groups: %#v", got)
		}
		if got := records[fixture.sourceTwo]["labels"]; !reflect.DeepEqual(got, []string{}) {
			t.Fatalf("empty labels encoded as %#v", got)
		}
		if got := records[fixture.sourceTwo]["groups"]; !reflect.DeepEqual(got, []string{}) {
			t.Fatalf("empty groups encoded as %#v", got)
		}
	})

	t.Run("bounded hydrate accepts the exact edge boundary and stops on the next row", func(t *testing.T) {
		fixture.resetEdges(t, ctx)
		exactRecords := map[uuid.UUID]map[string]any{
			fixture.sourceOne: {"id": fixture.sourceOne.String()},
			fixture.sourceTwo: {"id": fixture.sourceTwo.String()},
		}
		exactCounter := &countingRowsQuerier{delegate: fixture.conn}
		used, err := fixture.bindings.HydrateBounded(ctx, exactCounter,
			[]uuid.UUID{fixture.sourceTwo, fixture.sourceOne}, exactRecords, 2)
		if err != nil {
			t.Fatalf("hydrate exact boundary: %v", err)
		}
		if used != 2 || exactCounter.queries != 2 || exactCounter.closes != 2 {
			t.Fatalf("exact boundary used/queries/closes = %d/%d/%d, want 2/2/2",
				used, exactCounter.queries, exactCounter.closes)
		}
		if got := exactCounter.queryLimits(); !reflect.DeepEqual(got, []int{3, 2}) {
			t.Fatalf("exact boundary query limits = %v, want [3 2]", got)
		}
		if got := exactRecords[fixture.sourceOne]["labels"]; !reflect.DeepEqual(got, []string{fixture.targetOne.String()}) {
			t.Fatalf("exact boundary labels = %#v", got)
		}
		if got := exactRecords[fixture.sourceOne]["groups"]; !reflect.DeepEqual(got, []string{fixture.targetThree.String()}) {
			t.Fatalf("exact boundary groups = %#v", got)
		}

		overRecords := map[uuid.UUID]map[string]any{
			fixture.sourceOne: {"id": fixture.sourceOne.String()},
			fixture.sourceTwo: {"id": fixture.sourceTwo.String()},
		}
		overCounter := &countingRowsQuerier{delegate: fixture.conn}
		used, err = fixture.bindings.HydrateBounded(ctx, overCounter,
			[]uuid.UUID{fixture.sourceOne, fixture.sourceTwo}, overRecords, 1)
		if used != 2 || !errors.Is(err, ErrHydrationBudgetExceeded) {
			t.Fatalf("over boundary used/error = %d/%v", used, err)
		}
		if overCounter.queries != 2 || overCounter.closes != 2 {
			t.Fatalf("over boundary queries/closes = %d/%d, want 2/2", overCounter.queries, overCounter.closes)
		}
		if got := overCounter.queryLimits(); !reflect.DeepEqual(got, []int{2, 1}) {
			t.Fatalf("over boundary query limits = %v, want [2 1]", got)
		}
		if strings.Contains(err.Error(), fixture.sourceOne.String()) ||
			strings.Contains(err.Error(), fixture.labelTable) || strings.Contains(err.Error(), fixture.groupTable) {
			t.Fatalf("budget error disclosed relation storage details: %q", err)
		}
	})

	t.Run("bounded hydrate validates negative and zero remaining budgets", func(t *testing.T) {
		fixture.resetEdges(t, ctx)
		noSourceCounter := &countingRowsQuerier{delegate: fixture.conn}
		used, err := fixture.bindings.HydrateBounded(ctx, noSourceCounter,
			nil, map[uuid.UUID]map[string]any{}, 0)
		if err != nil || used != 0 || noSourceCounter.queries != 0 {
			t.Fatalf("no-source used/error/queries = %d/%v/%d", used, err, noSourceCounter.queries)
		}
		negativeCounter := &countingRowsQuerier{delegate: fixture.conn}
		used, err = fixture.bindings.HydrateBounded(ctx, negativeCounter,
			[]uuid.UUID{fixture.sourceOne}, map[uuid.UUID]map[string]any{fixture.sourceOne: {}}, -1)
		if used != 0 || !errors.Is(err, ErrInvalidBinding) || negativeCounter.queries != 0 {
			t.Fatalf("negative budget used/error/queries = %d/%v/%d", used, err, negativeCounter.queries)
		}

		for _, table := range []string{fixture.labelTable, fixture.groupTable} {
			if _, err := fixture.conn.Exec(ctx, "delete from "+pgx.Identifier{"pgbase_relation", table}.Sanitize()); err != nil {
				t.Fatal(err)
			}
		}
		emptyRecords := map[uuid.UUID]map[string]any{fixture.sourceOne: {}}
		emptyCounter := &countingRowsQuerier{delegate: fixture.conn}
		used, err = fixture.bindings.HydrateBounded(ctx, emptyCounter,
			[]uuid.UUID{fixture.sourceOne}, emptyRecords, 0)
		if err != nil || used != 0 || emptyCounter.queries != 2 || emptyCounter.closes != 2 {
			t.Fatalf("zero budget empty hydrate used/error/queries/closes = %d/%v/%d/%d",
				used, err, emptyCounter.queries, emptyCounter.closes)
		}
		if got := emptyCounter.queryLimits(); !reflect.DeepEqual(got, []int{1, 1}) {
			t.Fatalf("zero budget empty query limits = %v", got)
		}
		if !reflect.DeepEqual(emptyRecords[fixture.sourceOne]["labels"], []string{}) ||
			!reflect.DeepEqual(emptyRecords[fixture.sourceOne]["groups"], []string{}) {
			t.Fatalf("zero budget empty hydration = %#v", emptyRecords)
		}

		fixture.resetEdges(t, ctx)
		overCounter := &countingRowsQuerier{delegate: fixture.conn}
		used, err = fixture.bindings.HydrateBounded(ctx, overCounter,
			[]uuid.UUID{fixture.sourceOne}, map[uuid.UUID]map[string]any{fixture.sourceOne: {}}, 0)
		if used != 1 || !errors.Is(err, ErrHydrationBudgetExceeded) ||
			overCounter.queries != 1 || overCounter.closes != 1 {
			t.Fatalf("zero budget one row used/error/queries/closes = %d/%v/%d/%d",
				used, err, overCounter.queries, overCounter.closes)
		}
		if got := overCounter.queryLimits(); !reflect.DeepEqual(got, []int{1}) {
			t.Fatalf("zero budget overflow query limit = %v", got)
		}
	})

	t.Run("bounded hydrate closes rows and preserves used count on query and stream errors", func(t *testing.T) {
		fixture.resetEdges(t, ctx)
		queryFailure := errors.New("forced hydration query failure")
		queryCounter := &countingRowsQuerier{
			delegate: fixture.conn, failQuery: 2, queryErr: queryFailure,
		}
		used, err := fixture.bindings.HydrateBounded(ctx, queryCounter,
			[]uuid.UUID{fixture.sourceOne}, map[uuid.UUID]map[string]any{fixture.sourceOne: {}}, 10)
		if used != 1 || !errors.Is(err, queryFailure) || queryCounter.queries != 2 || queryCounter.closes != 1 {
			t.Fatalf("query failure used/error/queries/closes = %d/%v/%d/%d",
				used, err, queryCounter.queries, queryCounter.closes)
		}

		rowsFailure := errors.New("forced hydration rows failure")
		rowsCounter := &countingRowsQuerier{
			delegate: fixture.conn, rowsErrQuery: 2, rowsErrAfter: 0, rowsErr: rowsFailure,
		}
		used, err = fixture.bindings.HydrateBounded(ctx, rowsCounter,
			[]uuid.UUID{fixture.sourceOne}, map[uuid.UUID]map[string]any{fixture.sourceOne: {}}, 10)
		if used != 1 || !errors.Is(err, rowsFailure) || rowsCounter.queries != 2 || rowsCounter.closes != 2 {
			t.Fatalf("rows failure used/error/queries/closes = %d/%v/%d/%d",
				used, err, rowsCounter.queries, rowsCounter.closes)
		}

		scanFailure := errors.New("forced hydration scan failure")
		scanCounter := &countingRowsQuerier{
			delegate: fixture.conn, scanErrQuery: 1, scanErr: scanFailure,
		}
		used, err = fixture.bindings.HydrateBounded(ctx, scanCounter,
			[]uuid.UUID{fixture.sourceOne}, map[uuid.UUID]map[string]any{fixture.sourceOne: {}}, 10)
		if used != 1 || !errors.Is(err, scanFailure) || scanCounter.queries != 1 || scanCounter.closes != 1 {
			t.Fatalf("scan failure used/error/queries/closes = %d/%v/%d/%d",
				used, err, scanCounter.queries, scanCounter.closes)
		}
	})

	t.Run("bounded hydrate query count follows multi fields not source records", func(t *testing.T) {
		fixture.resetEdges(t, ctx)
		catalogWithSource := func(source schema.Collection) schema.Catalog {
			catalog := fixture.catalog
			catalog.Collections = make(map[string]schema.Collection, len(fixture.catalog.Collections))
			for name, collection := range fixture.catalog.Collections {
				catalog.Collections[name] = collection
			}
			catalog.Collections[source.Name] = source
			return catalog
		}

		zeroSource := fixture.source
		zeroSource.Fields = []schema.Field{}
		zeroBindings, err := Bind(catalogWithSource(zeroSource), zeroSource)
		if err != nil {
			t.Fatal(err)
		}
		zeroCounter := &countingRowsQuerier{delegate: fixture.conn}
		used, err := zeroBindings.HydrateBounded(ctx, zeroCounter,
			[]uuid.UUID{fixture.sourceOne}, map[uuid.UUID]map[string]any{fixture.sourceOne: {}}, 10)
		if err != nil || used != 0 || zeroCounter.queries != 0 {
			t.Fatalf("zero-field used/error/queries = %d/%v/%d", used, err, zeroCounter.queries)
		}

		oneSource := fixture.source
		oneSource.Fields = append([]schema.Field(nil), fixture.source.Fields[:1]...)
		oneBindings, err := Bind(catalogWithSource(oneSource), oneSource)
		if err != nil {
			t.Fatal(err)
		}
		oneCounter := &countingRowsQuerier{delegate: fixture.conn}
		used, err = oneBindings.HydrateBounded(ctx, oneCounter,
			[]uuid.UUID{fixture.sourceOne}, map[uuid.UUID]map[string]any{fixture.sourceOne: {}}, 1)
		if err != nil || used != 1 || oneCounter.queries != 1 || oneCounter.closes != 1 {
			t.Fatalf("one-field used/error/queries/closes = %d/%v/%d/%d",
				used, err, oneCounter.queries, oneCounter.closes)
		}
		quotedLabelTable := pgx.Identifier{"pgbase_relation", fixture.labelTable}.Sanitize()
		if len(oneCounter.statements) != 1 || !strings.Contains(oneCounter.statements[0], "from "+quotedLabelTable) ||
			!strings.HasSuffix(oneCounter.statements[0], " limit $2") ||
			strings.Contains(oneCounter.statements[0], fixture.sourceOne.String()) {
			t.Fatalf("bounded hydrate SQL is not identifier/value safe: %q", oneCounter.statements)
		}

		extraIDs := make([]uuid.UUID, 24)
		for index := range extraIDs {
			extraIDs[index] = uuid.New()
		}
		if _, err := fixture.conn.Exec(ctx, `insert into app.sources (id)
			select source_id from unnest($1::uuid[]) as sources(source_id)`, extraIDs); err != nil {
			t.Fatal(err)
		}
		if _, err := fixture.conn.Exec(ctx, "insert into "+quotedLabelTable+` (source_id, target_id, position)
			select source_id, $2, 0 from unnest($1::uuid[]) as sources(source_id)`, extraIDs, fixture.targetOne); err != nil {
			t.Fatal(err)
		}
		ids := append([]uuid.UUID{fixture.sourceOne}, extraIDs...)
		records := make(map[uuid.UUID]map[string]any, len(ids))
		for _, id := range ids {
			records[id] = map[string]any{"id": id.String()}
		}
		manyCounter := &countingRowsQuerier{delegate: fixture.conn}
		wantRows := len(ids) + 1 // one label per source plus sourceOne's group edge.
		used, err = fixture.bindings.HydrateBounded(ctx, manyCounter, ids, records, wantRows)
		if err != nil || used != wantRows || manyCounter.queries != 2 || manyCounter.closes != 2 {
			t.Fatalf("many-record used/error/queries/closes = %d/%v/%d/%d, want %d/nil/2/2",
				used, err, manyCounter.queries, manyCounter.closes, wantRows)
		}
		for _, id := range extraIDs {
			if !reflect.DeepEqual(records[id]["labels"], []string{fixture.targetOne.String()}) ||
				!reflect.DeepEqual(records[id]["groups"], []string{}) {
				t.Fatalf("many-record hydration for %s = %#v", id, records[id])
			}
		}
		if _, err := fixture.conn.Exec(ctx, `delete from app.sources where id=any($1::uuid[])`, extraIDs); err != nil {
			t.Fatal(err)
		}
	})

	t.Run("bounded hydrate preserves order and rejects cardinality or position drift", func(t *testing.T) {
		fixture.resetEdges(t, ctx)
		quotedLabels := pgx.Identifier{"pgbase_relation", fixture.labelTable}.Sanitize()
		if _, err := fixture.conn.Exec(ctx, "insert into "+quotedLabels+
			" (source_id, target_id, position) values ($1, $2, 1)", fixture.sourceOne, fixture.targetTwo); err != nil {
			t.Fatal(err)
		}
		ordered := map[uuid.UUID]map[string]any{fixture.sourceOne: {}}
		used, err := fixture.bindings.HydrateBounded(ctx, fixture.conn,
			[]uuid.UUID{fixture.sourceOne}, ordered, 3)
		if err != nil || used != 3 {
			t.Fatalf("ordered bounded hydrate used/error = %d/%v", used, err)
		}
		if got := ordered[fixture.sourceOne]["labels"]; !reflect.DeepEqual(got,
			[]string{fixture.targetOne.String(), fixture.targetTwo.String()}) {
			t.Fatalf("bounded relation order = %#v", got)
		}

		fixture.resetEdges(t, ctx)
		minimumCounter := &countingRowsQuerier{delegate: fixture.conn}
		used, err = bindLiveFirstFieldBounds(t, fixture, 2, 10, true).HydrateBounded(ctx, minimumCounter,
			[]uuid.UUID{fixture.sourceOne}, map[uuid.UUID]map[string]any{fixture.sourceOne: {}}, 10)
		if used != 1 || !errors.Is(err, ErrInvalidStoredValue) ||
			minimumCounter.queries != 1 || minimumCounter.closes != 1 {
			t.Fatalf("minimum drift used/error/queries/closes = %d/%v/%d/%d",
				used, err, minimumCounter.queries, minimumCounter.closes)
		}

		if _, err := fixture.conn.Exec(ctx, "insert into "+quotedLabels+
			" (source_id, target_id, position) values ($1, $2, 1), ($1, $3, 2)",
			fixture.sourceOne, fixture.targetTwo, fixture.targetThree); err != nil {
			t.Fatal(err)
		}
		maximumCounter := &countingRowsQuerier{delegate: fixture.conn}
		used, err = bindLiveFirstFieldBounds(t, fixture, 0, 2, false).HydrateBounded(ctx, maximumCounter,
			[]uuid.UUID{fixture.sourceOne}, map[uuid.UUID]map[string]any{fixture.sourceOne: {}}, 10)
		if used != 3 || !errors.Is(err, ErrInvalidStoredValue) ||
			maximumCounter.queries != 1 || maximumCounter.closes != 1 {
			t.Fatalf("maximum drift used/error/queries/closes = %d/%v/%d/%d",
				used, err, maximumCounter.queries, maximumCounter.closes)
		}

		fixture.resetEdges(t, ctx)
		if _, err := fixture.conn.Exec(ctx, "update "+quotedLabels+" set position=1 where source_id=$1", fixture.sourceOne); err != nil {
			t.Fatal(err)
		}
		positionCounter := &countingRowsQuerier{delegate: fixture.conn}
		used, err = fixture.bindings.HydrateBounded(ctx, positionCounter,
			[]uuid.UUID{fixture.sourceOne}, map[uuid.UUID]map[string]any{fixture.sourceOne: {}}, 10)
		if used != 1 || !errors.Is(err, ErrInvalidStoredValue) ||
			positionCounter.queries != 1 || positionCounter.closes != 1 {
			t.Fatalf("position drift used/error/queries/closes = %d/%v/%d/%d",
				used, err, positionCounter.queries, positionCounter.closes)
		}
		if strings.Contains(err.Error(), fixture.sourceOne.String()) || strings.Contains(err.Error(), fixture.labelTable) {
			t.Fatalf("stored-value error disclosed relation storage details: %q", err)
		}
		fixture.resetEdges(t, ctx)
	})

	t.Run("hydrate fails closed on stored cardinality drift", func(t *testing.T) {
		fixture.resetEdges(t, ctx)
		bindWithBounds := func(t *testing.T, minimum, maximum int, required bool) *Bindings {
			t.Helper()
			source := fixture.source
			source.Fields = append([]schema.Field(nil), source.Fields...)
			spec := *source.Fields[0].Relation
			spec.MinSelect = minimum
			spec.MaxSelect = maximum
			source.Fields[0].Relation = &spec
			source.Fields[0].Required = required
			catalog := fixture.catalog
			catalog.Collections = make(map[string]schema.Collection, len(fixture.catalog.Collections))
			for name, collection := range fixture.catalog.Collections {
				catalog.Collections[name] = collection
			}
			catalog.Collections[source.Name] = source
			bindings, err := Bind(catalog, source)
			if err != nil {
				t.Fatalf("bind drift fixture: %v", err)
			}
			return bindings
		}

		records := map[uuid.UUID]map[string]any{fixture.sourceOne: {}}
		if err := bindWithBounds(t, 2, 10, true).Hydrate(ctx, fixture.conn,
			[]uuid.UUID{fixture.sourceOne}, records); !errors.Is(err, ErrInvalidStoredValue) {
			t.Fatalf("under-minimum hydrate error = %v", err)
		}

		if _, err := fixture.conn.Exec(ctx, "insert into "+pgx.Identifier{"pgbase_relation", fixture.labelTable}.Sanitize()+
			" (source_id, target_id, position) values ($1, $2, 1), ($1, $3, 2)",
			fixture.sourceOne, fixture.targetTwo, fixture.targetThree); err != nil {
			t.Fatal(err)
		}
		records = map[uuid.UUID]map[string]any{fixture.sourceOne: {}}
		if err := bindWithBounds(t, 0, 2, false).Hydrate(ctx, fixture.conn,
			[]uuid.UUID{fixture.sourceOne}, records); !errors.Is(err, ErrInvalidStoredValue) {
			t.Fatalf("over-maximum hydrate error = %v", err)
		}
		fixture.resetEdges(t, ctx)
	})

	t.Run("pending self target and hydrate", func(t *testing.T) {
		fixture.testPendingSelf(t, ctx)
	})

	t.Run("opposite collection input order cannot deadlock", func(t *testing.T) {
		fixture.testOppositeCollectionLocks(t, ctx, databaseURL)
	})
}

type liveFixture struct {
	conn        *pgx.Conn
	bindings    *Bindings
	catalog     schema.Catalog
	source      schema.Collection
	target      schema.Collection
	labelTable  string
	groupTable  string
	sourceOne   uuid.UUID
	sourceTwo   uuid.UUID
	targetOne   uuid.UUID
	targetTwo   uuid.UUID
	targetThree uuid.UUID
}

func newLiveFixture(t *testing.T, ctx context.Context, databaseURL string) *liveFixture {
	t.Helper()
	conn, err := pgx.Connect(ctx, databaseURL)
	if err != nil {
		t.Fatalf("connect relation test database: %v", err)
	}
	fixture := &liveFixture{
		conn:        conn,
		sourceOne:   uuid.MustParse("0199aa21-0000-7000-8000-000000000101"),
		sourceTwo:   uuid.MustParse("0199aa21-0000-7000-8000-000000000102"),
		targetOne:   uuid.MustParse("0199aa21-0000-7000-8000-000000000201"),
		targetTwo:   uuid.MustParse("0199aa21-0000-7000-8000-000000000202"),
		targetThree: uuid.MustParse("0199aa21-0000-7000-8000-000000000203"),
	}
	sourceID := uuid.MustParse("0199aa21-0000-7000-8000-000000000010")
	targetID := uuid.MustParse("0199aa21-0000-7000-8000-000000000020")
	fixture.labelTable = schema.NewRelationPhysicalNames("sources", "labels").MultiTable
	fixture.groupTable = schema.NewRelationPhysicalNames("sources", "groups").MultiTable
	fixture.source = schema.Collection{
		ID: sourceID, Name: "sources", TableName: "sources",
		Fields: []schema.Field{
			{ID: uuid.MustParse("0199aa21-0000-7000-8000-000000000011"), Name: "labels", ColumnName: "labels", Type: schema.TypeRelation, Indexed: true, Ordinal: 0,
				Relation: relationSpec(targetID, schema.RelationMultiple, 0, 10, schema.RelationStorageEdgeTableV1, fixture.labelTable)},
			{ID: uuid.MustParse("0199aa21-0000-7000-8000-000000000012"), Name: "groups", ColumnName: "groups", Type: schema.TypeRelation, Indexed: true, Ordinal: 1,
				Relation: relationSpec(targetID, schema.RelationMultiple, 0, 10, schema.RelationStorageEdgeTableV1, fixture.groupTable)},
		},
	}
	fixture.target = schema.Collection{ID: targetID, Name: "targets", TableName: "targets", Fields: []schema.Field{}}
	fixture.catalog = schema.Catalog{Version: 1, Collections: map[string]schema.Collection{
		fixture.source.Name: fixture.source,
		fixture.target.Name: fixture.target,
	}}
	fixture.bindings, err = Bind(fixture.catalog, fixture.source)
	if err != nil {
		conn.Close(ctx)
		t.Fatalf("bind live fixture: %v", err)
	}

	setup := []string{
		"create schema app",
		"create schema pgbase_relation",
		`create table app.targets (id uuid primary key)`,
		`create table app.sources (id uuid primary key)`,
		createLiveEdgeSQL(fixture.labelTable, "sources", "targets"),
		createLiveEdgeSQL(fixture.groupTable, "sources", "targets"),
	}
	for _, statement := range setup {
		if _, err := conn.Exec(ctx, statement); err != nil {
			conn.Close(ctx)
			t.Fatalf("setup live relation storage: %v", err)
		}
	}
	if _, err := conn.Exec(ctx, `insert into app.sources (id) values ($1), ($2)`, fixture.sourceOne, fixture.sourceTwo); err != nil {
		t.Fatal(err)
	}
	if _, err := conn.Exec(ctx, `insert into app.targets (id) values ($1), ($2), ($3)`, fixture.targetOne, fixture.targetTwo, fixture.targetThree); err != nil {
		t.Fatal(err)
	}
	fixture.resetEdges(t, ctx)
	return fixture
}

func (f *liveFixture) decode(t *testing.T, payload map[string]json.RawMessage) Input {
	t.Helper()
	input, err := f.bindings.Decode(PatchInput, payload)
	if err != nil {
		t.Fatalf("decode live input: %v", err)
	}
	return input
}

func (f *liveFixture) resetEdges(t *testing.T, ctx context.Context) {
	t.Helper()
	for _, table := range []string{f.labelTable, f.groupTable} {
		if _, err := f.conn.Exec(ctx, "delete from "+pgx.Identifier{"pgbase_relation", table}.Sanitize()); err != nil {
			t.Fatalf("clear edge table: %v", err)
		}
	}
	if _, err := f.conn.Exec(ctx, "insert into "+pgx.Identifier{"pgbase_relation", f.labelTable}.Sanitize()+
		" (source_id, target_id, position) values ($1, $2, 0)", f.sourceOne, f.targetOne); err != nil {
		t.Fatal(err)
	}
	if _, err := f.conn.Exec(ctx, "insert into "+pgx.Identifier{"pgbase_relation", f.groupTable}.Sanitize()+
		" (source_id, target_id, position) values ($1, $2, 0)", f.sourceOne, f.targetThree); err != nil {
		t.Fatal(err)
	}
}

func bindLiveFirstFieldBounds(t *testing.T, fixture *liveFixture, minimum, maximum int, required bool) *Bindings {
	t.Helper()
	source := fixture.source
	source.Fields = append([]schema.Field(nil), fixture.source.Fields...)
	spec := *source.Fields[0].Relation
	spec.MinSelect = minimum
	spec.MaxSelect = maximum
	source.Fields[0].Relation = &spec
	source.Fields[0].Required = required
	catalog := fixture.catalog
	catalog.Collections = make(map[string]schema.Collection, len(fixture.catalog.Collections))
	for name, collection := range fixture.catalog.Collections {
		catalog.Collections[name] = collection
	}
	catalog.Collections[source.Name] = source
	bindings, err := Bind(catalog, source)
	if err != nil {
		t.Fatalf("bind live bounds: %v", err)
	}
	return bindings
}

func createLiveEdgeSQL(table, source, target string) string {
	return "create table " + pgx.Identifier{"pgbase_relation", table}.Sanitize() + ` (
		source_id uuid not null references ` + pgx.Identifier{"app", source}.Sanitize() + `(id) on delete cascade,
		target_id uuid not null references ` + pgx.Identifier{"app", target}.Sanitize() + `(id) on delete restrict,
		position integer not null check (position >= 0),
		primary key (source_id, target_id), unique (source_id, position))`
}

type queryer interface {
	Query(context.Context, string, ...any) (pgx.Rows, error)
}

func readTargets(t *testing.T, ctx context.Context, q queryer, table string, sourceID uuid.UUID) []uuid.UUID {
	t.Helper()
	rows, err := q.Query(ctx, "select target_id from "+pgx.Identifier{"pgbase_relation", table}.Sanitize()+
		" where source_id = $1 order by position", sourceID)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var result []uuid.UUID
	for rows.Next() {
		var id uuid.UUID
		if err := rows.Scan(&id); err != nil {
			t.Fatal(err)
		}
		result = append(result, id)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return result
}

type countingRowsQuerier struct {
	delegate     RowsQuerier
	queries      int
	closes       int
	statements   []string
	arguments    [][]any
	failQuery    int
	queryErr     error
	rowsErrQuery int
	rowsErrAfter int
	rowsErr      error
	scanErrQuery int
	scanErr      error
}

func (q *countingRowsQuerier) Query(ctx context.Context, sql string, arguments ...any) (pgx.Rows, error) {
	q.queries++
	q.statements = append(q.statements, sql)
	q.arguments = append(q.arguments, append([]any(nil), arguments...))
	if q.failQuery == q.queries {
		return nil, q.queryErr
	}
	rows, err := q.delegate.Query(ctx, sql, arguments...)
	if err != nil {
		return nil, err
	}
	tracked := &closeCountingRows{Rows: rows, onClose: func() { q.closes++ }}
	if q.rowsErrQuery == q.queries {
		return &errorAfterRows{Rows: tracked, failAfter: q.rowsErrAfter, err: q.rowsErr}, nil
	}
	if q.scanErrQuery == q.queries {
		return &scanErrorRows{Rows: tracked, err: q.scanErr}, nil
	}
	return tracked, nil
}

func (q *countingRowsQuerier) queryLimits() []int {
	limits := make([]int, 0, len(q.arguments))
	for _, arguments := range q.arguments {
		if len(arguments) < 2 {
			continue
		}
		limit, ok := arguments[1].(int)
		if !ok {
			return nil
		}
		limits = append(limits, limit)
	}
	return limits
}

type closeCountingRows struct {
	pgx.Rows
	onClose func()
	closed  bool
}

func (r *closeCountingRows) Close() {
	if !r.closed {
		r.closed = true
		r.onClose()
	}
	r.Rows.Close()
}

type errorAfterRows struct {
	pgx.Rows
	failAfter int
	observed  int
	failed    bool
	err       error
}

func (r *errorAfterRows) Next() bool {
	if r.observed >= r.failAfter {
		r.failed = true
		r.Rows.Close()
		return false
	}
	if !r.Rows.Next() {
		return false
	}
	r.observed++
	return true
}

func (r *errorAfterRows) Err() error {
	if r.failed {
		return r.err
	}
	return r.Rows.Err()
}

type scanErrorRows struct {
	pgx.Rows
	err error
}

func (r *scanErrorRows) Scan(...any) error {
	r.Rows.Close()
	return r.err
}

type relationQueryTracer struct {
	mu      sync.Mutex
	queries []string
}

func (t *relationQueryTracer) TraceQueryStart(ctx context.Context, _ *pgx.Conn, data pgx.TraceQueryStartData) context.Context {
	t.mu.Lock()
	t.queries = append(t.queries, data.SQL)
	t.mu.Unlock()
	return ctx
}

func (*relationQueryTracer) TraceQueryEnd(context.Context, *pgx.Conn, pgx.TraceQueryEndData) {}

func (t *relationQueryTracer) Reset() {
	t.mu.Lock()
	t.queries = nil
	t.mu.Unlock()
}

func (t *relationQueryTracer) RowLockQueries() int {
	t.mu.Lock()
	defer t.mu.Unlock()
	count := 0
	for _, statement := range t.queries {
		if strings.Contains(statement, "select id from") &&
			(strings.Contains(statement, "FOR UPDATE") || strings.Contains(statement, "FOR KEY SHARE")) {
			count++
		}
	}
	return count
}

func (f *liveFixture) testPendingSelf(t *testing.T, ctx context.Context) {
	t.Helper()
	collectionID := uuid.MustParse("0199aa21-0000-7000-8000-000000000310")
	fieldID := uuid.MustParse("0199aa21-0000-7000-8000-000000000311")
	recordID := uuid.MustParse("0199aa21-0000-7000-8000-000000000312")
	table := schema.NewRelationPhysicalNames("nodes", "peers").MultiTable
	collection := schema.Collection{ID: collectionID, Name: "nodes", TableName: "nodes", Fields: []schema.Field{
		{ID: fieldID, Name: "peers", ColumnName: "peers", Type: schema.TypeRelation, Indexed: true,
			Relation: relationSpec(collectionID, schema.RelationMultiple, 0, 5, schema.RelationStorageEdgeTableV1, table)},
	}}
	catalog := schema.Catalog{Version: 1, Collections: map[string]schema.Collection{"nodes": collection}}
	bindings, err := Bind(catalog, collection)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.conn.Exec(ctx, `create table app.nodes (id uuid primary key)`); err != nil {
		t.Fatal(err)
	}
	if _, err := f.conn.Exec(ctx, createLiveEdgeSQL(table, "nodes", "nodes")); err != nil {
		t.Fatal(err)
	}
	input, err := bindings.Decode(CreateInput, map[string]json.RawMessage{
		"peers": json.RawMessage(`["` + recordID.String() + `"]`),
	})
	if err != nil {
		t.Fatal(err)
	}
	tx, err := f.conn.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx)
	if err := bindings.LockCollections(ctx, tx, input); err != nil {
		t.Fatal(err)
	}
	if err := bindings.LockRows(ctx, tx, recordID, SourcePending, input); err != nil {
		t.Fatalf("pending self target was rejected: %v", err)
	}
	if _, err := tx.Exec(ctx, `insert into app.nodes (id) values ($1)`, recordID); err != nil {
		t.Fatal(err)
	}
	if err := bindings.ReplaceMulti(ctx, tx, recordID, input); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	records := map[uuid.UUID]map[string]any{recordID: {}}
	if err := bindings.Hydrate(ctx, f.conn, []uuid.UUID{recordID}, records); err != nil {
		t.Fatal(err)
	}
	if got := records[recordID]["peers"]; !reflect.DeepEqual(got, []string{recordID.String()}) {
		t.Fatalf("unexpected self hydration: %#v", got)
	}

	lower := uuid.MustParse("0199aa21-0000-7000-8000-000000000301")
	upper := uuid.MustParse("0199aa21-0000-7000-8000-000000000399")
	if _, err := f.conn.Exec(ctx, `insert into app.nodes (id) values ($1), ($2)`, lower, upper); err != nil {
		t.Fatal(err)
	}
	storedInput, err := bindings.Decode(PatchInput, map[string]json.RawMessage{
		"peers": json.RawMessage(`["` + upper.String() + `","` + recordID.String() + `","` + lower.String() + `"]`),
	})
	if err != nil {
		t.Fatal(err)
	}
	storedTx, err := f.conn.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer storedTx.Rollback(ctx)
	counted := &countingTx{Tx: storedTx}
	if err := bindings.LockRows(ctx, counted, recordID, SourceStored, storedInput); err != nil {
		t.Fatalf("lock stored self relation: %v", err)
	}
	if counted.rowLockQueries != 3 {
		t.Fatalf("stored self relation used %d row-lock queries; want lower batch, source UPDATE, upper batch", counted.rowLockQueries)
	}
}

func (f *liveFixture) testOppositeCollectionLocks(t *testing.T, ctx context.Context, databaseURL string) {
	t.Helper()
	aID := uuid.MustParse("0199aa21-0000-7000-8000-000000000410")
	bID := uuid.MustParse("0199aa21-0000-7000-8000-000000000420")
	aRecord := uuid.MustParse("0199aa21-0000-7000-8000-000000000411")
	bRecord := uuid.MustParse("0199aa21-0000-7000-8000-000000000421")
	a := schema.Collection{ID: aID, Name: "alpha", TableName: "alpha", Fields: []schema.Field{
		{ID: uuid.MustParse("0199aa21-0000-7000-8000-000000000412"), Name: "beta", ColumnName: "beta", Type: schema.TypeRelation, Indexed: true,
			Relation: relationSpec(bID, schema.RelationSingle, 0, 1, schema.RelationStorageInlineFKV1, "beta")},
	}}
	b := schema.Collection{ID: bID, Name: "beta", TableName: "beta", Fields: []schema.Field{
		{ID: uuid.MustParse("0199aa21-0000-7000-8000-000000000422"), Name: "alpha", ColumnName: "alpha", Type: schema.TypeRelation, Indexed: true,
			Relation: relationSpec(aID, schema.RelationSingle, 0, 1, schema.RelationStorageInlineFKV1, "alpha")},
	}}
	catalog := schema.Catalog{Version: 1, Collections: map[string]schema.Collection{"alpha": a, "beta": b}}
	aBindings, err := Bind(catalog, a)
	if err != nil {
		t.Fatal(err)
	}
	bBindings, err := Bind(catalog, b)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.conn.Exec(ctx, `create table app.alpha (id uuid primary key, beta uuid)`); err != nil {
		t.Fatal(err)
	}
	if _, err := f.conn.Exec(ctx, `create table app.beta (id uuid primary key, alpha uuid)`); err != nil {
		t.Fatal(err)
	}
	if _, err := f.conn.Exec(ctx, `insert into app.alpha (id) values ($1)`, aRecord); err != nil {
		t.Fatal(err)
	}
	if _, err := f.conn.Exec(ctx, `insert into app.beta (id) values ($1)`, bRecord); err != nil {
		t.Fatal(err)
	}
	aInput, err := aBindings.Decode(PatchInput, map[string]json.RawMessage{"beta": json.RawMessage(`"` + bRecord.String() + `"`)})
	if err != nil {
		t.Fatal(err)
	}
	bInput, err := bBindings.Decode(PatchInput, map[string]json.RawMessage{"alpha": json.RawMessage(`"` + aRecord.String() + `"`)})
	if err != nil {
		t.Fatal(err)
	}

	first, err := pgx.Connect(ctx, databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer first.Close(ctx)
	second, err := pgx.Connect(ctx, databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer second.Close(ctx)
	firstTx, err := first.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer firstTx.Rollback(ctx)
	secondTx, err := second.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer secondTx.Rollback(ctx)
	if _, err := firstTx.Exec(ctx, `set local lock_timeout = '3s'`); err != nil {
		t.Fatal(err)
	}
	if _, err := secondTx.Exec(ctx, `set local lock_timeout = '3s'`); err != nil {
		t.Fatal(err)
	}

	type result struct {
		first bool
		err   error
	}
	start := make(chan struct{})
	results := make(chan result, 2)
	go func() {
		<-start
		results <- result{first: true, err: aBindings.LockCollections(ctx, firstTx, aInput)}
	}()
	go func() {
		<-start
		results <- result{first: false, err: bBindings.LockCollections(ctx, secondTx, bInput)}
	}()
	close(start)

	var winner result
	select {
	case winner = <-results:
	case <-time.After(4 * time.Second):
		t.Fatal("opposite collection locks did not produce a winner")
	}
	if winner.err != nil {
		t.Fatalf("first collection lock failed: %v", winner.err)
	}
	if winner.first {
		if err := firstTx.Commit(ctx); err != nil {
			t.Fatal(err)
		}
	} else if err := secondTx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	select {
	case loser := <-results:
		if loser.err != nil {
			t.Fatalf("second collection lock failed: %v", loser.err)
		}
		if loser.first {
			if err := firstTx.Commit(ctx); err != nil {
				t.Fatal(err)
			}
		} else if err := secondTx.Commit(ctx); err != nil {
			t.Fatal(err)
		}
	case <-time.After(4 * time.Second):
		t.Fatal("second opposite collection lock remained blocked")
	}
}

type countingTx struct {
	pgx.Tx
	rowLockQueries int
}

func (t *countingTx) Query(ctx context.Context, sql string, arguments ...any) (pgx.Rows, error) {
	if strings.Contains(sql, "FOR KEY SHARE") {
		t.rowLockQueries++
	}
	return t.Tx.Query(ctx, sql, arguments...)
}

func (t *countingTx) QueryRow(ctx context.Context, sql string, arguments ...any) pgx.Row {
	if strings.Contains(sql, "FOR UPDATE") {
		t.rowLockQueries++
	}
	return t.Tx.QueryRow(ctx, sql, arguments...)
}
