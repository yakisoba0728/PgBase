//go:build integration

package schema

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"testing"
)

func TestSampleViewEncodesUUIDsAsCanonicalStrings(t *testing.T) {
	ctx := context.Background()
	pool, service, admin := newSchemaServiceTest(t)
	applyFreshPlan(t, service, CreateCollectionInput{Name: "sample_uuid_source", Fields: []FieldInput{{Name: "ref", Type: TypeUUID}, {Name: "amount", Type: TypeNumeric}}}, admin)
	if _, err := pool.Exec(ctx, `insert into app.sample_uuid_source(id,created_at,updated_at,ref,amount) values('6f1c7c3e-6f5a-4c39-9c1e-2b9f6c1d0a11',now(),now(),'00000000-0000-0000-0000-0000000000ab',1.50)`); err != nil {
		t.Fatal(err)
	}
	sample, err := service.SampleView(ctx, "select id,created_at,ref,amount from app.sample_uuid_source")
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := json.Marshal(sample.Items)
	if err != nil {
		t.Fatal(err)
	}
	var items []map[string]any
	if err := json.Unmarshal(encoded, &items); err != nil || len(items) != 1 {
		t.Fatalf("items=%s err=%v", encoded, err)
	}
	if items[0]["id"] != "6f1c7c3e-6f5a-4c39-9c1e-2b9f6c1d0a11" || items[0]["ref"] != "00000000-0000-0000-0000-0000000000ab" || items[0]["amount"] != 1.5 {
		t.Fatalf("sample items=%s", encoded)
	}
	if _, ok := items[0]["created_at"].(string); !ok {
		t.Fatalf("created_at=%#v", items[0]["created_at"])
	}
}

func TestSampleViewExplainsInvalidSQL(t *testing.T) {
	ctx := context.Background()
	_, service, admin := newSchemaServiceTest(t)
	applyFreshPlan(t, service, CreateCollectionInput{Name: "sample_error_source", Fields: []FieldInput{{Name: "title", Type: TypeText}}}, admin)
	cases := []struct {
		query   string
		details map[string][]string
	}{
		// The connection search_path excludes app, so an unqualified table fails analysis.
		{"select id, title from sample_error_source", map[string][]string{"view.sql": {"invalid_sql"}, "view.sql.sqlState": {"42P01"}, "view.sql.position": {"23"}, "view.sql.message": {`relation "sample_error_source" does not exist`}}},
		{"  select id, titl from app.sample_error_source", map[string][]string{"view.sql": {"invalid_sql"}, "view.sql.sqlState": {"42703"}, "view.sql.position": {"14"}}},
		{"select title from app.sample_error_source", map[string][]string{"view.sql": {"id_required"}}},
		{"select id, title::bytea as raw from app.sample_error_source", map[string][]string{"view.sql": {"unsupported_column"}, "view.sql.column": {"raw"}}},
		{"select id, name as title from pgbase_meta.collections", map[string][]string{"view.sql": {"relation_not_collection"}, "view.sql.relation": {"pgbase_meta.collections"}}},
	}
	for _, test := range cases {
		_, err := service.SampleView(ctx, test.query)
		var validation *ValidationError
		if !errors.Is(err, ErrConflict) || !errors.As(err, &validation) {
			t.Fatalf("SampleView(%q) error=%v", test.query, err)
		}
		got := map[string][]string{}
		for _, detail := range validation.Details {
			got[detail.Field] = append(got[detail.Field], detail.Code)
		}
		for field, want := range test.details {
			if !slices.Equal(got[field], want) {
				t.Fatalf("SampleView(%q) details=%v, want %s=%v", test.query, got, field, want)
			}
		}
	}
}
