//go:build integration

package schema

import (
	"bytes"
	"context"
	"encoding/json"
	"reflect"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/yakisoba0728/PgBase/internal/passwordfield"
)

func TestAdvancedFieldRenamePreservesValuesViewsAndPortableOptions(t *testing.T) {
	ctx := context.Background()
	pool, service, admin := newSchemaServiceTest(t)
	minimum := 4
	maximum := int64(2048)
	fields := []FieldInput{
		{Name: "body", Type: TypeEditor, Indexed: true, Presentable: true, Help: "Rich text", Options: &FieldOptions{MaxSize: &maximum, ConvertURLs: true}},
		{Name: "place", Type: TypeGeoPoint, Indexed: true},
		{Name: "changed", Type: TypeAutoDate, Indexed: true, Options: &FieldOptions{OnCreate: true, OnUpdate: true}},
		{Name: "pin", Type: TypePassword, Hidden: true, Options: &FieldOptions{MinLength: &minimum}},
		{Name: "contact", Type: TypeText, Unique: true, Options: &FieldOptions{Format: "email", AllowedDomains: []string{"Example.COM."}, DeniedDomains: []string{"private.example.com"}, AutogeneratePattern: `user-[a-z]{6}@example[.]com`}},
	}
	created := applyFreshPlan(t, service, CreateCollectionInput{Name: "advanced_rename", Fields: fields}, admin)
	applyFreshPlan(t, service, CreateCollectionInput{Name: "advanced_view", View: &ViewInput{Source: created.Name, Projection: []string{"body", "changed"}}}, admin)
	digest, err := passwordfield.Hash("retained-password")
	if err != nil {
		t.Fatal(err)
	}
	id := uuid.New()
	changed := time.Date(2026, 9, 9, 1, 2, 3, 0, time.UTC)
	if _, err = pool.Exec(ctx, `insert into app.advanced_rename(id,created_at,updated_at,body,place,changed,pin) values($1,now(),now(),$2,$3,$4,$5)`, id, "<p>Retained</p>", `{"lon":127.5,"lat":37.5}`, changed, digest); err != nil {
		t.Fatal(err)
	}
	for index := range fields {
		fields[index].ID = &created.Fields[index].ID
		fields[index].Name += "_renamed"
	}
	renamed := applyChange(t, service, NewUpdateChange(UpdateCollectionInput{Name: created.Name, Fields: fields}), admin).Collection
	for index, field := range renamed.Fields {
		if field.ID != created.Fields[index].ID || field.ColumnName != created.Fields[index].ColumnName || field.Name != fields[index].Name {
			t.Fatalf("rename lost field identity: %#v", field)
		}
	}
	var body, storedDigest string
	var point []byte
	var timestamp time.Time
	if err = pool.QueryRow(ctx, `select body,place,changed,pin from app.advanced_rename where id=$1`, id).Scan(&body, &point, &timestamp, &storedDigest); err != nil {
		t.Fatal(err)
	}
	var coordinates map[string]float64
	if err = json.Unmarshal(point, &coordinates); err != nil || body != "<p>Retained</p>" || !timestamp.Equal(changed) || storedDigest != digest || !reflect.DeepEqual(coordinates, map[string]float64{"lon": 127.5, "lat": 37.5}) {
		t.Fatalf("rename changed stored values (JSON error: %v)", err)
	}
	if err = pool.QueryRow(ctx, `select body from app.advanced_view where id=$1`, id).Scan(&body); err != nil || body != "<p>Retained</p>" {
		t.Fatalf("dependent view lost its value: %v", err)
	}
	document, err := service.ExportSchema(ctx)
	if err != nil {
		t.Fatal(err)
	}
	_, target, targetAdmin := newSchemaServiceTest(t)
	plan, err := target.PreviewSchemaImport(ctx, document)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = target.ApplySchemaImport(ctx, SchemaImportIntent{Document: document, BaseVersion: plan.BaseVersion, Checksum: plan.Checksum, Actor: targetAdmin, OperationID: uuid.New(), IdempotencyKey: "advanced-rename-import-0001"}); err != nil {
		t.Fatal(err)
	}
	roundTrip, err := target.ExportSchema(ctx)
	if err != nil {
		t.Fatal(err)
	}
	// Rule documents are JSON objects: PostgreSQL JSONB may reorder their keys.
	// Preserve numeric lexemes while comparing the portable document semantics.
	decode := func(document SchemaDocument) any {
		t.Helper()
		encoded, err := json.Marshal(document)
		if err != nil {
			t.Fatal(err)
		}
		decoder := json.NewDecoder(bytes.NewReader(encoded))
		decoder.UseNumber()
		var value any
		if err := decoder.Decode(&value); err != nil {
			t.Fatal(err)
		}
		return value
	}
	if !reflect.DeepEqual(decode(document), decode(roundTrip)) {
		t.Fatal("advanced field options or dependent view changed in schema round trip")
	}
}
