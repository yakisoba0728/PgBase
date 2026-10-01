//go:build integration

package query

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/yakisoba0728/PgBase/internal/accessrule"
	"github.com/yakisoba0728/PgBase/internal/identity"
	"github.com/yakisoba0728/PgBase/internal/recordaccess"
	"github.com/yakisoba0728/PgBase/internal/schema"
)

func TestPublicFunctionFiltersContinueBoundCursor(t *testing.T) {
	pool, schemas, records := newRecordServiceTest(t)
	ctx := context.Background()
	const name = "function_cursor_records"
	applyRecordCollection(t, schemas, schema.CreateCollectionInput{Name: name, Fields: []schema.FieldInput{
		{Name: "title", Type: schema.TypeText, Indexed: true},
		{Name: "published", Type: schema.TypeTimestampTZ},
		{Name: "reviewed", Type: schema.TypeTimestampTZ},
		{Name: "place", Type: schema.TypeGeoPoint},
		{Name: "backup", Type: schema.TypeGeoPoint},
	}})
	for _, title := range []string{"alpha", "beta", "excluded"} {
		date, point := `"2026-09-22T00:00:00Z"`, `{"lon":1,"lat":2}`
		if title == "excluded" {
			date, point = `"2025-09-22T00:00:00Z"`, `{"lon":0,"lat":0}`
		}
		if _, err := records.Create(ctx, name, map[string]json.RawMessage{
			"title":     json.RawMessage(`"` + title + `"`),
			"published": json.RawMessage(date), "reviewed": json.RawMessage(date),
			"place": json.RawMessage(point), "backup": json.RawMessage(point),
		}); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := recordaccess.NewService(pool).Set(ctx, name, true); err != nil {
		t.Fatal(err)
	}
	catalog, err := schemas.Catalog(ctx)
	if err != nil {
		t.Fatal(err)
	}
	allow := accessrule.Rule{Op: "allow"}
	rules, err := json.Marshal(accessrule.Rules{List: allow, View: allow, Create: allow, Update: allow, Delete: allow})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `insert into pgbase_meta.collection_rules(collection_id,rules) values($1,$2::jsonb)`, catalog.Collections[name].ID, string(rules)); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name, filter string
		changed      []string
	}{
		{"strftime", `strftime('%Y', published) = '2026'`, []string{
			`strftime('%Y', reviewed) = '2026'`,
			`strftime('%Y-%m', published) = '2026'`,
			`strftime('%Y', published) = '2025'`,
			`strftime('%Y', published) != '2026'`,
		}},
		{"geoDistance", `geoDistance(place.lon, place.lat, 1, 2) < 1`, []string{
			`geoDistance(backup.lon, backup.lat, 1, 2) < 1`,
			`geoDistance(place.lat, place.lon, 1, 2) < 1`,
			`geoDistance(place.lon, place.lat, 2, 1) < 1`,
			`geoDistance(place.lon, place.lat, 1, 2) < 2`,
			`geoDistance(place.lon, place.lat, 1, 2) <= 1`,
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			request := ListRequest{Principal: Principal{Kind: identity.Anonymous}, Collection: name, Filter: tc.filter, Sort: "title", Limit: 1}
			first, err := records.List(ctx, request)
			if err != nil || len(first.Items) != 1 || first.Items[0]["title"] != "alpha" || first.NextCursor == "" {
				t.Fatalf("cursorless function page: page=%#v err=%v", first, err)
			}
			request.Cursor = first.NextCursor
			second, err := records.List(ctx, request)
			if err != nil || len(second.Items) != 1 || second.Items[0]["title"] != "beta" || second.NextCursor != "" {
				t.Fatalf("function continuation: page=%#v err=%v", second, err)
			}
			for _, changed := range tc.changed {
				request.Filter = changed
				if _, err := records.List(ctx, request); !IsInvalidCursor(err) {
					t.Errorf("changed filter %q reused cursor: %v", changed, err)
				}
			}
		})
	}
	t.Run("invalid_time_literal", func(t *testing.T) {
		for _, filter := range []string{`strftime('%Y', 'not-a-date') = '2026'`, `strftime('%Y', '2026-99-99') = '2026'`} {
			request := ListRequest{Principal: Principal{Kind: identity.Anonymous}, Collection: name, Filter: filter, Limit: 1}
			t.Run(filter+"/list", func(t *testing.T) {
				_, err := records.List(ctx, request)
				assertValidationDetail(t, err, "filter", "type_mismatch")
			})
			t.Run(filter+"/count", func(t *testing.T) {
				_, err := Count(ctx, records, request)
				assertValidationDetail(t, err, "filter", "type_mismatch")
			})
		}
	})
}
