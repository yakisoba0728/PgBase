package query

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
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
	page1, err := records.List(ctx, "records", "", 2)
	if err != nil || len(page1.Items) != 2 || page1.NextCursor == "" {
		t.Fatalf("List(page1) = %#v, %v", page1, err)
	}
	page2, err := records.List(ctx, "records", page1.NextCursor, 2)
	if err != nil || len(page2.Items) != 2 || page2.NextCursor != "" || page1.Items[1]["id"] == page2.Items[0]["id"] {
		t.Fatalf("List(page2) = %#v, %v", page2, err)
	}
	if _, err := records.List(ctx, "records", "not-a-signed-cursor", 2); !IsInvalidCursor(err) {
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
