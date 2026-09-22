//go:build integration

package query

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/google/uuid"
	"github.com/yakisoba0728/PgBase/internal/accessrule"
	"github.com/yakisoba0728/PgBase/internal/identity"
	"github.com/yakisoba0728/PgBase/internal/recordaccess"
	"github.com/yakisoba0728/PgBase/internal/schema"
)

// All callers reach the production list/count kernels. An authorization rule
// may inspect a hidden field, but a caller-supplied filter must never do so.
func TestPublicQueryRejectsHiddenOperands(t *testing.T) {
	pool, schemas, records := newRecordServiceTest(t)
	ctx := context.Background()
	applyRecordCollection(t, schemas, schema.CreateCollectionInput{Name: "hidden_operand_people", Fields: []schema.FieldInput{
		{Name: "secret", Type: schema.TypeText, Hidden: true},
		{Name: "secret_lon", Type: schema.TypeInteger, Hidden: true},
	}})
	applyRecordCollection(t, schemas, schema.CreateCollectionInput{Name: "hidden_operand_records", Fields: []schema.FieldInput{
		{Name: "title", Type: schema.TypeText},
		{Name: "secret", Type: schema.TypeText, Hidden: true},
		{Name: "secret_time", Type: schema.TypeTimestampTZ, Hidden: true},
		{Name: "secret_point", Type: schema.TypeGeoPoint, Hidden: true},
		{Name: "secret_lon", Type: schema.TypeInteger, Hidden: true},
		{Name: "payload", Type: schema.TypeJSONB, Hidden: true},
		{Name: "owner", Type: schema.TypeRelation, Relation: &schema.RelationInput{
			TargetCollection: "hidden_operand_people", Cardinality: schema.RelationSingle, OnDelete: schema.RelationRestrict,
		}},
	}})
	person, err := records.Create(ctx, "hidden_operand_people", map[string]json.RawMessage{
		"secret": json.RawMessage(`"allowed"`), "secret_lon": json.RawMessage(`1`),
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, value := range []string{"allowed", "blocked"} {
		_, err := records.Create(ctx, "hidden_operand_records", map[string]json.RawMessage{
			"title": json.RawMessage(`"` + value + `"`), "secret": json.RawMessage(`"` + value + `"`),
			"secret_time":  json.RawMessage(`"2026-09-22T00:00:00Z"`),
			"secret_point": json.RawMessage(`{"lon":1,"lat":2}`), "secret_lon": json.RawMessage(`1`),
			"payload": json.RawMessage(`{"code":"allowed"}`),
			"owner":   json.RawMessage(`"` + person["id"].(string) + `"`),
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	user := Principal{Kind: identity.User, ID: uuid.New()}
	if _, err := pool.Exec(ctx, `insert into pgbase_meta.users(id,email,password_hash,active,created_at,updated_at)
		values($1,'hidden-operands@example.test','test',true,transaction_timestamp(),transaction_timestamp())`, user.ID); err != nil {
		t.Fatal(err)
	}
	catalog, err := schemas.Catalog(ctx)
	if err != nil {
		t.Fatal(err)
	}
	allow := accessrule.Rule{Op: "allow"}
	for _, name := range []string{"hidden_operand_people", "hidden_operand_records"} {
		if _, err := recordaccess.NewService(pool).Set(ctx, name, true); err != nil {
			t.Fatal(err)
		}
		listRule := allow
		if name == "hidden_operand_records" {
			listRule = accessrule.Rule{Op: "eq", Field: "secret", Value: json.RawMessage(`"allowed"`)}
		}
		rules, err := json.Marshal(accessrule.Rules{List: listRule, View: allow, Create: allow, Update: allow, Delete: allow})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := pool.Exec(ctx, `insert into pgbase_meta.collection_rules(collection_id,rules) values($1,$2::jsonb)`, catalog.Collections[name].ID, string(rules)); err != nil {
			t.Fatal(err)
		}
	}
	filters := []struct {
		name, expression string
		// Unsupported public query shapes still need an early hidden-field
		// rejection; only existing executable shapes are admin positive controls.
		adminSupported bool
	}{
		{"right_field", `title = secret`, true},
		{"right_modified_field", `title:lower = secret:lower`, true},
		{"time_function", `strftime('%Y', secret_time) = '2026'`, false},
		{"geo_function", `geoDistance(secret_point.lon, secret_point.lat, 1, 2) < 1`, false},
		{"numeric_function", `geoDistance(secret_lon, 2, 1, 2) < 1`, false},
		{"json_path", `payload.code = 'allowed'`, false},
		{"relation_path", `owner.secret = 'allowed'`, true},
		{"right_json_path", `title = payload.code`, false},
		{"right_relation_path", `title = owner.secret`, false},
		{"function_relation_path", `geoDistance(owner.secret_lon, 2, 1, 2) < 1`, false},
		{"grouped_right_field", `title = 'absent' || (title = secret && title != 'absent')`, true},
	}
	readers := []struct {
		name string
		read func(ListRequest) error
	}{
		{"native_list", func(request ListRequest) error { _, err := records.List(ctx, request); return err }},
		{"native_count", func(request ListRequest) error { _, err := Count(ctx, records, request); return err }},
		{"compatible_list", func(request ListRequest) error {
			request.PocketBaseCompatibility = true
			_, err := records.List(ctx, request)
			return err
		}},
		{"compatible_page", func(request ListRequest) error {
			_, err := ListPocketBase(ctx, records, PocketBaseListRequest{ListRequest: request, Page: 1, PerPage: 10})
			return err
		}},
		{"compatible_page_skip_total", func(request ListRequest) error {
			_, err := ListPocketBase(ctx, records, PocketBaseListRequest{ListRequest: request, Page: 1, PerPage: 10, SkipTotal: true})
			return err
		}},
	}
	for _, subject := range []struct {
		name      string
		principal Principal
	}{{"user", user}, {"anonymous", Principal{Kind: identity.Anonymous}}} {
		t.Run(subject.name, func(t *testing.T) {
			base := ListRequest{Principal: subject.principal, Collection: "hidden_operand_records", Limit: 10}
			page, err := records.List(ctx, base)
			if err != nil || len(page.Items) != 1 || page.Items[0]["title"] != "allowed" {
				t.Fatalf("stored hidden-field authorization rule: page=%#v err=%v", page, err)
			}
			if _, exposed := page.Items[0]["secret"]; exposed {
				t.Fatal("hidden field exposed in output")
			}
			for _, reader := range readers {
				t.Run(reader.name, func(t *testing.T) {
					for _, filter := range filters {
						t.Run(filter.name, func(t *testing.T) {
							request := base
							request.Filter = filter.expression
							assertValidationDetail(t, reader.read(request), "filter", "hidden_field")
						})
					}
				})
			}
		})
	}
	t.Run("administrator_can_query_hidden_values", func(t *testing.T) {
		for _, filter := range filters {
			if !filter.adminSupported {
				continue
			}
			t.Run(filter.name, func(t *testing.T) {
				page, err := records.List(ctx, ListRequest{Principal: testReadPrincipal, Collection: "hidden_operand_records", Filter: filter.expression, Limit: 10})
				if err != nil || len(page.Items) == 0 {
					t.Fatalf("admin hidden query: page=%#v err=%v", page, err)
				}
				if _, exposed := page.Items[0]["secret"]; !exposed {
					t.Fatal("administrator hidden output removed")
				}
			})
		}
	})
}
