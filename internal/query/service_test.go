package query

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/webisnoteasy/PgBase/internal/database"
	"github.com/webisnoteasy/PgBase/internal/migrate"
	"github.com/webisnoteasy/PgBase/internal/schema"
	"github.com/webisnoteasy/PgBase/internal/testutil"
)

func TestRecordServiceCRUDConstraintsPrecisionAndPagination(t *testing.T) {
	pool, schemas, records := newRecordServiceTest(t)
	_ = pool
	ctx := context.Background()
	input := schema.CreateCollectionInput{Name: "records", Fields: []schema.FieldInput{
		{Name: "active", Type: schema.TypeBoolean, Required: true},
		{Name: "count", Type: schema.TypeInteger, Required: true},
		{Name: "large_count", Type: schema.TypeBigInt, Required: true},
		{Name: "amount", Type: schema.TypeNumeric, Required: true},
		{Name: "title", Type: schema.TypeText, Required: true, Unique: true},
		{Name: "published_at", Type: schema.TypeTimestampTZ, Required: true},
		{Name: "external_id", Type: schema.TypeUUID, Required: true},
		{Name: "metadata", Type: schema.TypeJSONB, Default: json.RawMessage(`"default"`)},
	}}
	applyRecordCollection(t, schemas, input)

	first, err := records.Create(ctx, "records", recordInput("first", `"scalar"`))
	if err != nil {
		t.Fatalf("Create(first): %v", err)
	}
	if first["large_count"] != "9223372036854775807" || first["amount"] != "12345678901234567890.12345678901234567890" {
		t.Fatalf("precision lost: %#v", first)
	}
	assertRecordJSON(t, first["metadata"], `"scalar"`)
	defaultedInput := recordInput("defaulted", `{}`)
	delete(defaultedInput, "metadata")
	defaulted, err := records.Create(ctx, "records", defaultedInput)
	if err != nil {
		t.Fatalf("Create(defaulted): %v", err)
	}
	assertRecordJSON(t, defaulted["metadata"], `"default"`)
	id := uuid.MustParse(first["id"].(string))

	got, err := records.Get(ctx, "records", id)
	if err != nil || got["title"] != "first" {
		t.Fatalf("Get() = %#v, %v", got, err)
	}
	updated, err := records.Update(ctx, "records", id, map[string]json.RawMessage{"title": json.RawMessage(`"updated"`)})
	if err != nil || updated["title"] != "updated" || updated["active"] != true || updated["created_at"] == updated["updated_at"] {
		t.Fatalf("Update() = %#v, %v", updated, err)
	}
	if _, err := records.Update(ctx, "records", id, map[string]json.RawMessage{}); err == nil {
		t.Fatal("Update(empty) error=nil")
	}
	if _, err := records.Update(ctx, "records", uuid.New(), map[string]json.RawMessage{"title": json.RawMessage(`"missing"`)}); !errors.Is(err, ErrRecordNotFound) {
		t.Fatalf("Update(missing) error=%v", err)
	}

	if _, err := records.Create(ctx, "records", recordInput("updated", `{}`)); database.Classify(err) != database.KindConflict {
		t.Fatalf("unique error = %v, want conflict", err)
	}
	for name, input := range map[string]map[string]json.RawMessage{
		"required": recordInputWithoutTitle(),
		"unknown":  {"unknown": json.RawMessage(`true`)},
	} {
		if _, err := records.Create(ctx, "records", input); err == nil {
			t.Fatalf("%s Create() error=nil", name)
		} else {
			var validation *ValidationError
			if !errors.As(err, &validation) {
				t.Fatalf("%s error=%T %v, want validation", name, err, err)
			}
		}
	}

	for _, title := range []string{"second", "third"} {
		if _, err := records.Create(ctx, "records", recordInput(title, `null`)); err != nil {
			t.Fatalf("Create(%s): %v", title, err)
		}
	}
	identicalCreatedAt := time.Date(2026, 8, 28, 12, 0, 0, 0, time.UTC)
	if _, err := pool.Exec(ctx, `update app.records set created_at = $1`, identicalCreatedAt); err != nil {
		t.Fatal(err)
	}
	page1, err := records.List(ctx, "records", "", 2, "", "")
	if err != nil || len(page1.Items) != 2 || page1.NextCursor == "" {
		t.Fatalf("List(page1) = %#v, %v", page1, err)
	}
	page2, err := records.List(ctx, "records", page1.NextCursor, 2, "", "")
	if err != nil || len(page2.Items) != 2 || page2.NextCursor != "" || page1.Items[1]["id"] == page2.Items[0]["id"] {
		t.Fatalf("List(page2) = %#v, %v", page2, err)
	}
	gotIDs := []string{page1.Items[0]["id"].(string), page1.Items[1]["id"].(string), page2.Items[0]["id"].(string), page2.Items[1]["id"].(string)}
	wantIDs := make([]string, 0, 4)
	all, err := pool.Query(ctx, `select id::text from app.records order by created_at, id`)
	if err != nil {
		t.Fatal(err)
	}
	defer all.Close()
	for all.Next() {
		var value string
		if err := all.Scan(&value); err != nil {
			t.Fatal(err)
		}
		wantIDs = append(wantIDs, value)
	}
	if err := all.Err(); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(gotIDs, wantIDs) {
		t.Fatalf("same-timestamp cursor page order/union = %v, want live (created_at,id) order %v", gotIDs, wantIDs)
	}
	if _, err := records.List(ctx, "records", "not-a-signed-cursor", 2, "", ""); !IsInvalidCursor(err) {
		t.Fatalf("List(invalid cursor) error=%v", err)
	}

	if err := records.Delete(ctx, "records", id); err != nil {
		t.Fatalf("Delete(): %v", err)
	}
	if _, err := records.Get(ctx, "records", id); !errors.Is(err, ErrRecordNotFound) {
		t.Fatalf("Get(deleted) error=%v", err)
	}
	if err := records.Delete(ctx, "records", id); !errors.Is(err, ErrRecordNotFound) {
		t.Fatalf("Delete(missing) error=%v", err)
	}
	if _, err := records.Get(ctx, "missing", id); !errors.Is(err, ErrCollectionNotFound) {
		t.Fatalf("Get(missing collection) error=%v", err)
	}
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	if _, err := records.Get(canceled, "records", uuid.New()); database.Classify(err) != database.KindCanceled {
		t.Fatalf("Get(canceled) error=%v", err)
	}
}

func TestRecordServiceUsesDatabaseClockForTimestamps(t *testing.T) {
	pool, schemas, records := newRecordServiceTest(t)
	ctx := context.Background()
	applyRecordCollection(t, schemas, schema.CreateCollectionInput{Name: "clock_records", Fields: []schema.FieldInput{{Name: "title", Type: schema.TypeText, Required: true}}})
	var before, after time.Time
	if err := pool.QueryRow(ctx, `select clock_timestamp()`).Scan(&before); err != nil {
		t.Fatal(err)
	}
	records.now = func() time.Time { return time.Date(2099, 1, 1, 0, 0, 0, 0, time.UTC) }
	created, err := records.Create(ctx, "clock_records", map[string]json.RawMessage{"title": json.RawMessage(`"database time"`)})
	if err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `select clock_timestamp()`).Scan(&after); err != nil {
		t.Fatal(err)
	}
	createdAt, err := time.Parse(time.RFC3339Nano, created["created_at"].(string))
	if err != nil {
		t.Fatal(err)
	}
	if createdAt.Before(before) || createdAt.After(after) || createdAt.Year() == 2099 {
		t.Fatalf("created_at = %s, want database clock between %s and %s", createdAt, before, after)
	}
}

func TestRecordServiceCapsPageResponseBytesAndContinuesWithCursor(t *testing.T) {
	_, schemas, records := newRecordServiceTest(t)
	ctx := context.Background()
	applyRecordCollection(t, schemas, schema.CreateCollectionInput{Name: "large_records", Fields: []schema.FieldInput{{Name: "payload", Type: schema.TypeText, Required: true}}})
	large := strings.Repeat("x", 900_000)
	wantIDs := make([]string, 0, 6)
	for index := 0; index < 6; index++ {
		created, err := records.Create(ctx, "large_records", map[string]json.RawMessage{"payload": json.RawMessage(fmt.Sprintf("%q", large))})
		if err != nil {
			t.Fatal(err)
		}
		wantIDs = append(wantIDs, created["id"].(string))
	}
	first, err := records.List(ctx, "large_records", "", 100, "", "")
	if err != nil {
		t.Fatal(err)
	}
	if len(first.Items) >= len(wantIDs) || first.NextCursor == "" {
		t.Fatalf("first byte-limited page = %d items cursor=%q", len(first.Items), first.NextCursor)
	}
	second, err := records.List(ctx, "large_records", first.NextCursor, 100, "", "")
	if err != nil {
		t.Fatal(err)
	}
	gotIDs := make([]string, 0, len(first.Items)+len(second.Items))
	for _, page := range [][]map[string]any{first.Items, second.Items} {
		for _, item := range page {
			gotIDs = append(gotIDs, item["id"].(string))
		}
	}
	sort.Strings(gotIDs)
	sort.Strings(wantIDs)
	if !reflect.DeepEqual(gotIDs, wantIDs) {
		t.Fatalf("byte-limited cursor pages IDs differ: got=%v want=%v", gotIDs, wantIDs)
	}
}

func TestRecordServicePersistsExplicitNullByCatalogNullability(t *testing.T) {
	pool, schemas, records := newRecordServiceTest(t)
	ctx := context.Background()
	applyRecordCollection(t, schemas, schema.CreateCollectionInput{Name: "nullable_records", Fields: []schema.FieldInput{
		{Name: "title", Type: schema.TypeText, Required: true},
		{Name: "active", Type: schema.TypeBoolean},
		{Name: "count", Type: schema.TypeInteger},
		{Name: "large_count", Type: schema.TypeBigInt},
		{Name: "amount", Type: schema.TypeNumeric},
		{Name: "note", Type: schema.TypeText},
		{Name: "published_at", Type: schema.TypeTimestampTZ},
		{Name: "external_id", Type: schema.TypeUUID},
		{Name: "metadata", Type: schema.TypeJSONB, Required: true},
	}})

	input := map[string]json.RawMessage{
		"title":        json.RawMessage(`"kept"`),
		"active":       json.RawMessage(`null`),
		"count":        json.RawMessage(`null`),
		"large_count":  json.RawMessage(`null`),
		"amount":       json.RawMessage(`null`),
		"note":         json.RawMessage(`null`),
		"published_at": json.RawMessage(`null`),
		"external_id":  json.RawMessage(`null`),
		"metadata":     json.RawMessage(`null`),
	}
	created, err := records.Create(ctx, "nullable_records", input)
	if err != nil {
		t.Fatalf("Create(nullable values): %v", err)
	}
	for _, name := range []string{"active", "count", "large_count", "amount", "note", "published_at", "external_id"} {
		if created[name] != nil {
			t.Errorf("created %s = %#v, want nil", name, created[name])
		}
	}
	assertRecordJSON(t, created["metadata"], `null`)

	id := uuid.MustParse(created["id"].(string))
	var activeNull, countNull, largeCountNull, amountNull, noteNull, publishedAtNull, externalIDNull bool
	var metadataSQLNull, metadataJSONNull bool
	err = pool.QueryRow(ctx, `select
		active is null, count is null, large_count is null, amount is null, note is null,
		published_at is null, external_id is null, metadata is null, metadata = 'null'::jsonb
		from app.nullable_records where id = $1`, id).Scan(
		&activeNull, &countNull, &largeCountNull, &amountNull, &noteNull,
		&publishedAtNull, &externalIDNull, &metadataSQLNull, &metadataJSONNull,
	)
	if err != nil {
		t.Fatalf("inspect persisted nullable values: %v", err)
	}
	if !activeNull || !countNull || !largeCountNull || !amountNull || !noteNull || !publishedAtNull || !externalIDNull {
		t.Fatalf("non-JSONB SQL nulls = %v %v %v %v %v %v %v, want all true",
			activeNull, countNull, largeCountNull, amountNull, noteNull, publishedAtNull, externalIDNull)
	}
	if metadataSQLNull || !metadataJSONNull {
		t.Fatalf("metadata SQL null = %v, JSON null = %v, want false/true", metadataSQLNull, metadataJSONNull)
	}
	updated, err := records.Update(ctx, "nullable_records", id, map[string]json.RawMessage{
		"active": json.RawMessage(`false`),
		"note":   json.RawMessage(`""`),
	})
	if err != nil || updated["active"] != false || updated["note"] != "" || updated["title"] != "kept" {
		t.Fatalf("Update(false and empty text) = %#v, %v", updated, err)
	}
	nulled, err := records.Update(ctx, "nullable_records", id, map[string]json.RawMessage{
		"active": json.RawMessage(`null`),
		"note":   json.RawMessage(`null`),
	})
	if err != nil || nulled["active"] != nil || nulled["note"] != nil || nulled["title"] != "kept" {
		t.Fatalf("Update(nullable values) = %#v, %v", nulled, err)
	}

	_, err = records.Update(ctx, "nullable_records", id, map[string]json.RawMessage{"title": json.RawMessage(`null`)})
	var validationErr *ValidationError
	if !errors.As(err, &validationErr) {
		t.Fatalf("Update(required null) error = %v, want *ValidationError", err)
	}
	want := []ValidationDetail{{Field: "title", Code: "required"}}
	if len(validationErr.Details) != 1 || validationErr.Details[0] != want[0] {
		t.Fatalf("Update(required null) details = %#v, want %#v", validationErr.Details, want)
	}
}

func TestRecordServiceRefreshesStaleCatalogBeforeCompiling(t *testing.T) {
	pool, writer, _ := newRecordServiceTest(t)
	staleSchemas := schema.NewService(pool, &schema.Cache{}, schema.Planner{})
	if catalog, err := staleSchemas.Catalog(context.Background()); err != nil || catalog.Version != 0 {
		t.Fatalf("prime stale catalog = %+v, %v", catalog, err)
	}
	applyRecordCollection(t, writer, schema.CreateCollectionInput{Name: "fresh", Fields: []schema.FieldInput{{Name: "title", Type: schema.TypeText, Required: true}}})
	records := NewService(pool, staleSchemas, []byte("0123456789abcdef0123456789abcdef"))
	created, err := records.Create(context.Background(), "fresh", map[string]json.RawMessage{"title": json.RawMessage(`"current"`)})
	if err != nil || created["title"] != "current" {
		t.Fatalf("Create against refreshed catalog = %#v, %v", created, err)
	}
}

func TestRecordServiceHonorsExecutionCancellation(t *testing.T) {
	pool, schemas, records := newRecordServiceTest(t)
	applyRecordCollection(t, schemas, schema.CreateCollectionInput{Name: "blocked", Fields: []schema.FieldInput{{Name: "title", Type: schema.TypeText, Required: true}}})
	connection, err := pool.Acquire(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer connection.Release()
	lock, err := connection.Begin(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer lock.Rollback(context.Background())
	if _, err := lock.Exec(context.Background(), `lock table app.blocked in access exclusive mode`); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	_, err = records.Create(ctx, "blocked", map[string]json.RawMessage{"title": json.RawMessage(`"wait"`)})
	if database.Classify(err) != database.KindCanceled {
		t.Fatalf("Create(timeout) error=%v", err)
	}
}

func TestRecordServiceFiltersSortsAndPaginatesWithNullSafeKeyset(t *testing.T) {
	_, schemas, records := newRecordServiceTest(t)
	applyRecordCollection(t, schemas, schema.CreateCollectionInput{
		Name: "items",
		Fields: []schema.FieldInput{
			{Name: "title", Type: schema.TypeText, Required: true},
			{Name: "rank", Type: schema.TypeInteger},
		},
	})
	ctx := context.Background()
	create := func(title string, rank string) {
		input := map[string]json.RawMessage{"title": json.RawMessage(`"` + title + `"`)}
		if rank != "" {
			input["rank"] = json.RawMessage(rank)
		}
		if _, err := records.Create(ctx, "items", input); err != nil {
			t.Fatalf("create %s: %v", title, err)
		}
	}
	// Creation order a..f; rank NULL for a and d. UUIDv7 IDs preserve creation
	// order, so ties on rank resolve deterministically toward earlier records.
	create("a", "")
	create("b", "1")
	create("c", "2")
	create("d", "")
	create("e", "3")
	create("f", "1")

	titles := func(page RecordPage) []string {
		result := make([]string, 0, len(page.Items))
		for _, item := range page.Items {
			result = append(result, item["title"].(string))
		}
		return result
	}

	filtered, err := records.List(ctx, "items", "", 50, "rank >= 2", "")
	if err != nil {
		t.Fatalf("filter list: %v", err)
	}
	if got, want := titles(filtered), []string{"c", "e"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("filter result = %v, want %v", got, want)
	}

	nulls, err := records.List(ctx, "items", "", 50, "rank = null", "")
	if err != nil {
		t.Fatalf("null filter list: %v", err)
	}
	if got, want := titles(nulls), []string{"a", "d"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("null filter result = %v, want %v", got, want)
	}

	descending, err := records.List(ctx, "items", "", 50, "", "-rank")
	if err != nil {
		t.Fatalf("sort list: %v", err)
	}
	if got, want := titles(descending), []string{"e", "c", "b", "f", "a", "d"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("sort result = %v, want %v", got, want)
	}

	first, err := records.List(ctx, "items", "", 2, "", "-rank")
	if err != nil || first.NextCursor == "" {
		t.Fatalf("first page = %v, cursor %q, err %v", titles(first), first.NextCursor, err)
	}
	second, err := records.List(ctx, "items", first.NextCursor, 2, "", "-rank")
	if err != nil {
		t.Fatalf("second page: %v", err)
	}
	third, err := records.List(ctx, "items", second.NextCursor, 2, "", "-rank")
	if err != nil {
		t.Fatalf("third page: %v", err)
	}
	if got, want := titles(first), []string{"e", "c"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("first page = %v, want %v", got, want)
	}
	if got, want := titles(second), []string{"b", "f"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("second page = %v, want %v", got, want)
	}
	if got, want := titles(third), []string{"a", "d"}; !reflect.DeepEqual(got, want) || third.NextCursor != "" {
		t.Fatalf("third page = %v cursor %q, want %v without cursor", got, third.NextCursor, want)
	}

	if _, err := records.List(ctx, "items", first.NextCursor, 2, "", ""); !IsInvalidCursor(err) {
		t.Fatalf("cursor sort mismatch error = %v, want invalid cursor", err)
	}
	if _, err := records.List(ctx, "items", "", 50, "ghost = 1", ""); err == nil {
		t.Fatal("unknown filter field succeeded")
	} else {
		var validationErr *ValidationError
		if !errors.As(err, &validationErr) || validationErr.Details[0] != (ValidationDetail{Field: "filter", Code: "unknown_field"}) {
			t.Fatalf("unknown filter field error = %v", err)
		}
	}
}

func newRecordServiceTest(t *testing.T) (*database.Pool, *schema.Service, *Service) {
	t.Helper()
	ctx := context.Background()
	pool, err := database.Open(ctx, testutil.NewDatabase(t), 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	if err := migrate.New(pool, nil).Up(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `insert into pgbase_meta.admins
		(id, email, password_hash, active, created_at, updated_at)
		values ('019551d0-e9c0-7000-8000-000000000001', 'record-test@example.com', 'test', true, current_timestamp, current_timestamp)`); err != nil {
		t.Fatal(err)
	}
	schemas := schema.NewService(pool, &schema.Cache{}, schema.Planner{})
	return pool, schemas, NewService(pool, schemas, []byte("0123456789abcdef0123456789abcdef"))
}

func applyRecordCollection(t *testing.T, schemas *schema.Service, input schema.CreateCollectionInput) {
	t.Helper()
	plan, err := schemas.PlanCreate(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := schemas.ApplyCreate(context.Background(), input, plan.Checksum, plan.BaseVersion, uuid.MustParse("019551d0-e9c0-7000-8000-000000000001")); err != nil {
		t.Fatal(err)
	}
}

func recordInput(title, metadata string) map[string]json.RawMessage {
	return map[string]json.RawMessage{
		"active": json.RawMessage(`true`), "count": json.RawMessage(`2147483647`),
		"large_count":  json.RawMessage(`"9223372036854775807"`),
		"amount":       json.RawMessage(`"12345678901234567890.12345678901234567890"`),
		"title":        json.RawMessage(`"` + title + `"`),
		"published_at": json.RawMessage(`"2026-08-28T21:34:56.123456+09:00"`),
		"external_id":  json.RawMessage(`"12345678-1234-4567-8901-123456789abc"`),
		"metadata":     json.RawMessage(metadata),
	}
}

func recordInputWithoutTitle() map[string]json.RawMessage {
	input := recordInput("unused", `{}`)
	delete(input, "title")
	return input
}

func assertRecordJSON(t *testing.T, got any, want string) {
	t.Helper()
	encoded, err := json.Marshal(got)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(encoded, []byte(want)) {
		t.Fatalf("JSON=%s want=%s", encoded, want)
	}
}
