package query

import (
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/webisnoteasy/PgBase/internal/relation"
	"github.com/webisnoteasy/PgBase/internal/schema"
)

func TestRelationCodecPreservesSingleAndOrderedMultipleValues(t *testing.T) {
	t.Parallel()

	first := uuid.MustParse("11111111-1111-4111-8111-111111111111")
	second := uuid.MustParse("22222222-2222-4222-8222-222222222222")
	decoded, err := decodeInput(relationCompilerCollection(), map[string]json.RawMessage{
		"owner": json.RawMessage(`"11111111-1111-4111-8111-111111111111"`),
		"tags":  json.RawMessage(`["22222222-2222-4222-8222-222222222222","11111111-1111-4111-8111-111111111111"]`),
	})
	if err != nil {
		t.Fatalf("decodeInput() error = %v", err)
	}
	if decoded["owner"] != first {
		t.Fatalf("owner = %#v, want %s", decoded["owner"], first)
	}
	if !reflect.DeepEqual(decoded["tags"], []uuid.UUID{second, first}) {
		t.Fatalf("tags = %#v, want ordered UUID values", decoded["tags"])
	}
}

func TestRelationCodecRejectsInvalidBoundsAndDuplicates(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		field string
		raw   string
		code  string
	}{
		{name: "single invalid", field: "owner", raw: `"not-a-uuid"`, code: "relation_invalid_value"},
		{name: "multi null", field: "tags", raw: `null`, code: "relation_invalid_value"},
		{name: "multi scalar", field: "tags", raw: `"11111111-1111-4111-8111-111111111111"`, code: "relation_invalid_value"},
		{name: "multi duplicate", field: "tags", raw: `["11111111-1111-4111-8111-111111111111","11111111-1111-4111-8111-111111111111"]`, code: "duplicate_relation_value"},
		{name: "multi over maximum", field: "tags", raw: `["11111111-1111-4111-8111-111111111111","22222222-2222-4222-8222-222222222222","33333333-3333-4333-8333-333333333333","44444444-4444-4444-8444-444444444444"]`, code: "relation_max_select"},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			_, err := decodeInput(relationCompilerCollection(), map[string]json.RawMessage{test.field: json.RawMessage(test.raw)})
			assertValidationDetail(t, err, test.field, test.code)
		})
	}

	required := relationCompilerCollection()
	required.Fields[1].Required = true
	required.Fields[1].Relation.MinSelect = 1
	_, err := CompileInsert(required, map[string]json.RawMessage{"tags": json.RawMessage(`[]`)}, uuid.New())
	assertValidationDetail(t, err, "tags", "relation_min_select")
}

func TestRelationCompilerStoresSingleInlineAndLeavesMultipleToStorageFacet(t *testing.T) {
	t.Parallel()

	recordID := uuid.MustParse("aaaaaaaa-bbbb-4ccc-8ddd-eeeeeeeeeeee")
	targetID := uuid.MustParse("11111111-1111-4111-8111-111111111111")
	input := map[string]json.RawMessage{
		"owner": json.RawMessage(`"11111111-1111-4111-8111-111111111111"`),
		"tags":  json.RawMessage(`["22222222-2222-4222-8222-222222222222"]`),
	}
	insert, err := CompileInsert(relationCompilerCollection(), input, recordID)
	if err != nil {
		t.Fatalf("CompileInsert() error = %v", err)
	}
	wantInsert := `INSERT INTO "app"."posts" ("id", "created_at", "updated_at", "owner") VALUES ($1, statement_timestamp(), statement_timestamp(), $2) RETURNING "id", "created_at", "updated_at", "owner"`
	if insert.SQL != wantInsert {
		t.Fatalf("insert SQL = %q, want %q", insert.SQL, wantInsert)
	}
	if !reflect.DeepEqual(insert.Args, []any{recordID, targetID}) {
		t.Fatalf("insert args = %#v", insert.Args)
	}

	update, err := CompileUpdate(relationCompilerCollection(), recordID, map[string]json.RawMessage{
		"tags": json.RawMessage(`["22222222-2222-4222-8222-222222222222"]`),
	})
	if err != nil {
		t.Fatalf("CompileUpdate() error = %v", err)
	}
	wantUpdate := `UPDATE "app"."posts" SET "updated_at" = statement_timestamp() WHERE "id" = $1 RETURNING "id", "created_at", "updated_at", "owner"`
	if update.SQL != wantUpdate || !reflect.DeepEqual(update.Args, []any{recordID}) {
		t.Fatalf("relation-only update = %#v, want SQL %q", update, wantUpdate)
	}
}

func TestEncodeRecordScansSingleAndInitializesMultipleAsArray(t *testing.T) {
	t.Parallel()

	id := uuid.MustParse("aaaaaaaa-bbbb-4ccc-8ddd-eeeeeeeeeeee")
	owner := uuid.MustParse("11111111-1111-4111-8111-111111111111")
	now := time.Date(2026, 8, 31, 0, 0, 0, 0, time.UTC)
	record, err := EncodeRecord(relationCompilerCollection(), &fixtureRow{values: []any{
		pgtype.UUID{Bytes: id, Valid: true}, now, now, pgtype.UUID{Bytes: owner, Valid: true},
	}})
	if err != nil {
		t.Fatalf("EncodeRecord() error = %v", err)
	}
	if record["owner"] != owner.String() {
		t.Fatalf("owner = %#v", record["owner"])
	}
	if !reflect.DeepEqual(record["tags"], []string{}) {
		t.Fatalf("tags = %#v, want non-nil empty array", record["tags"])
	}
}

func TestRelationCompilerBoundsUnsupportedMultiFilterAndSort(t *testing.T) {
	t.Parallel()

	collection := relationCompilerCollection()
	_, err := CompileList(collection, ListQuery{Filter: mustParseFilter(t, `tags = '11111111-1111-4111-8111-111111111111'`)})
	assertValidationDetail(t, err, "filter", "relation_filter_unsupported")
	_, err = CompileList(collection, ListQuery{Sort: []SortKey{{Field: "tags"}}})
	assertValidationDetail(t, err, "sort", "relation_sort_unsupported")

	statement, err := CompileList(collection, ListQuery{Filter: mustParseFilter(t, `owner = '11111111-1111-4111-8111-111111111111'`)})
	if err != nil {
		t.Fatalf("single relation filter error = %v", err)
	}
	if !strings.Contains(statement.SQL, `("owner" = $1)`) {
		t.Fatalf("single relation filter SQL = %q", statement.SQL)
	}
}

func TestFinalizeRecordPageRejectsOneOversizedHydratedRelationRecord(t *testing.T) {
	t.Parallel()

	record := map[string]any{
		"id":         "aaaaaaaa-bbbb-4ccc-8ddd-eeeeeeeeeeee",
		"created_at": "2026-08-31T00:00:00Z",
		"updated_at": "2026-08-31T00:00:00Z",
	}
	target := "11111111-1111-4111-8111-111111111111"
	for field := 0; field < 120; field++ {
		values := make([]string, 1000)
		for index := range values {
			values[index] = target
		}
		record["relation_"+strconv.Itoa(field)] = values
	}
	page := RecordPage{Items: []map[string]any{record}}
	encoded, err := json.Marshal(page)
	if err != nil {
		t.Fatal(err)
	}
	if len(encoded)+1 <= maximumRecordPageBytes {
		t.Fatalf("oversized relation fixture = %d bytes, want over %d", len(encoded)+1, maximumRecordPageBytes)
	}

	service := &Service{}
	_, err = assembleRecordPageForTest(service, page.Items, uuid.New(), effectiveSortKeys(nil), time.Now().Add(cursorTTL), false)
	assertValidationDetail(t, err, "response", "response_too_large")
}

func TestRelationHydrationFailureClassifiesDriftWithoutDisclosingPhysicalCause(t *testing.T) {
	t.Parallel()

	drift := fmt.Errorf("physical table r_private: %w", relation.ErrInvalidStoredValue)
	err := relationHydrationFailure(drift)
	if !errors.Is(err, schema.ErrConflict) {
		t.Fatalf("hydration drift error = %v, want schema conflict", err)
	}
	if err.Error() != "hydrate relation values failed" || strings.Contains(err.Error(), "r_private") {
		t.Fatalf("hydration drift disclosed physical cause: %q", err)
	}

	diagnostic := errors.New("physical table r_diagnostic")
	err = relationHydrationFailure(diagnostic)
	if !errors.Is(err, diagnostic) || strings.Contains(err.Error(), "r_diagnostic") {
		t.Fatalf("hydration diagnostic wrapping = %q", err)
	}
}

func relationCompilerCollection() schema.Collection {
	targetID := uuid.MustParse("99999999-9999-4999-8999-999999999999")
	return schema.Collection{
		Name: "posts", TableName: "posts",
		Fields: []schema.Field{
			{Name: "owner", ColumnName: "owner", Type: schema.TypeRelation, Indexed: true, Ordinal: 0, Relation: &schema.RelationSpec{
				TargetCollectionID: targetID, Cardinality: schema.RelationSingle, MinSelect: 0, MaxSelect: 1,
				OnDelete: schema.RelationRestrict, ValueCodecVersion: schema.RelationValueCodecVersion,
				Storage: schema.RelationStorage{Kind: schema.RelationStorageInlineFKV1, Name: "owner"},
			}},
			{Name: "tags", ColumnName: "tags", Type: schema.TypeRelation, Indexed: true, Ordinal: 1, Relation: &schema.RelationSpec{
				TargetCollectionID: targetID, Cardinality: schema.RelationMultiple, MinSelect: 0, MaxSelect: 3,
				OnDelete: schema.RelationRestrict, ValueCodecVersion: schema.RelationValueCodecVersion,
				Storage: schema.RelationStorage{Kind: schema.RelationStorageEdgeTableV1, Name: "rel_tbl_0123456789abcdef0123456789abcdef"},
			}},
		},
	}
}
