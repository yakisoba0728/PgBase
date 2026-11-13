//go:build integration

package query

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"

	"github.com/google/uuid"
	"github.com/yakisoba0728/PgBase/internal/accessrule"
	"github.com/yakisoba0728/PgBase/internal/identity"
	"github.com/yakisoba0728/PgBase/internal/recordaccess"
	"github.com/yakisoba0728/PgBase/internal/schema"
)

func TestClosureScalarDescriptorsAndPageCaps(t *testing.T) {
	pool, schemas, records := newRecordServiceTest(t)
	ctx := context.Background()
	maximumEditor := int64(100)
	input := schema.CreateCollectionInput{Name: "closure_scalar", Fields: []schema.FieldInput{
		{Name: "title", Type: schema.TypeText, Indexed: true}, {Name: "payload", Type: schema.TypeJSONB}, {Name: "body", Type: schema.TypeEditor, Indexed: true, Options: &schema.FieldOptions{MaxSize: &maximumEditor}}, {Name: "published", Type: schema.TypeAutoDate, Indexed: true, Options: &schema.FieldOptions{OnCreate: true}},
	}}
	if _, err := schemas.Preview(ctx, schema.NewCreateChange(input)); err != nil {
		t.Fatalf("preview: %#v", err)
	}
	applyRecordCollection(t, schemas, input)
	_, err := pool.Exec(ctx, `INSERT INTO app.closure_scalar(id,created_at,updated_at,title,payload,body,published) SELECT md5(n::text)::uuid,transaction_timestamp(),transaction_timestamp(),lpad(n::text,4,'0'),'{"score":2,"public":9007199254740993,"private":0}'::jsonb,lpad(n::text,4,'0'),'2026-01-01'::timestamptz+n*interval '1 second' FROM generate_series(1,501) n`)
	if err != nil {
		t.Fatal(err)
	}
	syncMembershipOrdinals(t, ctx, pool)
	principal := Principal{Kind: PrincipalAdministrator, ID: uuid.New()}
	for _, tc := range []struct{ filter, sort string }{{"title:length >= 4", "title"}, {"payload.score >= 2", "title"}, {"payload.missing = null", "title"}, {"", "body"}, {"", "published"}} {
		t.Run(tc.filter+"/"+tc.sort, func(t *testing.T) {
			request := ListRequest{Principal: principal, Collection: "closure_scalar", Filter: tc.filter, Sort: tc.sort, Limit: 1}
			first, err := records.List(ctx, request)
			if err != nil || len(first.Items) != 1 || first.NextCursor == "" {
				t.Fatalf("%+v first=%+v err=%v", tc, first, err)
			}
			request.Cursor = first.NextCursor
			second, err := records.List(ctx, request)
			if err != nil || len(second.Items) != 1 || second.Items[0]["id"] == first.Items[0]["id"] {
				t.Fatalf("%+v second=%+v err=%v", tc, second, err)
			}
			if tc.filter == "payload.missing = null" {
				request.Filter = "payload.other = null"
				if _, err := records.List(ctx, request); !IsInvalidCursor(err) {
					t.Fatalf("JSON path cursor reused: %v", err)
				}
			}
		})
	}
	seen := map[string]bool{}
	for n := 1; n <= 3; n++ {
		page, err := ListPocketBase(ctx, records, PocketBaseListRequest{ListRequest: ListRequest{Principal: principal, Collection: "closure_scalar", Sort: "title"}, Page: n, PerPage: 500})
		if err != nil {
			t.Fatal(err)
		}
		if page.PerPage != maximumPageLimit {
			t.Fatalf("perPage=%d", page.PerPage)
		}
		for _, r := range page.Items {
			id := r["id"].(string)
			if seen[id] {
				t.Fatal("duplicate page row")
			}
			seen[id] = true
		}
	}
	if len(seen) != 501 {
		t.Fatalf("page gaps: got %d", len(seen))
	}
}

func TestClosureRelationRequestsFilesFunctionsAndSequentialOrders(t *testing.T) {
	pool, schemas, records := newRecordServiceTest(t)
	ctx := context.Background()
	applyRecordCollection(t, schemas, schema.CreateCollectionInput{Name: "closure_people", Fields: []schema.FieldInput{{Name: "name", Type: schema.TypeText, Indexed: true}}})
	person, err := records.Create(ctx, "closure_people", map[string]json.RawMessage{"name": json.RawMessage(`"Alice"`)})
	if err != nil {
		t.Fatal(err)
	}
	maxSelect := 2
	applyRecordCollection(t, schemas, schema.CreateCollectionInput{Name: "closure_roots", RecordID: &schema.RecordIDPolicy{Kind: schema.RecordIDText, MinLength: 1, MaxLength: 32, Pattern: `^[A-Za-z0-9]+$`}, Fields: []schema.FieldInput{
		{Name: "owner", Type: schema.TypeRelation, Relation: &schema.RelationInput{TargetCollection: "closure_people", Cardinality: schema.RelationSingle}},
		{Name: "links", Type: schema.TypeRelation, Relation: &schema.RelationInput{TargetCollection: "closure_people", Cardinality: schema.RelationMultiple, MaxSelect: &maxSelect}},
		{Name: "attachment", Type: schema.TypeFile},
	}})
	catalog, err := schemas.Catalog(ctx)
	if err != nil {
		t.Fatal(err)
	}
	collection := catalog.Collections["closure_roots"]
	for i := 1; i <= 18; i++ {
		r, err := records.Create(ctx, "closure_roots", map[string]json.RawMessage{"owner": json.RawMessage(`"` + person["id"].(string) + `"`)})
		if err != nil {
			t.Fatal(err)
		}
		if _, err = pool.Exec(ctx, `INSERT INTO pgbase_meta.record_identities(collection_id,collection_kind,scope_id,internal_id,external_id) VALUES($1,'base',$1,$2,$3)`, collection.ID, r["id"], fmt.Sprintf("zzzz%02d", i)); err != nil {
			t.Fatal(err)
		}
	}
	principal := Principal{Kind: PrincipalAdministrator, ID: uuid.New()}
	for _, tc := range []struct{ filter, sort string }{{"", "id"}, {"owner.name = 'Alice'", "id"}, {"", "@random"}, {"strftime('%Y',created_at) != '1900'", "owner.name"}} {
		t.Run(tc.filter+"/"+tc.sort, func(t *testing.T) {
			page, err := ListPocketBase(ctx, records, PocketBaseListRequest{ListRequest: ListRequest{Principal: principal, Collection: collection.Name, Filter: tc.filter, Sort: tc.sort}, PerPage: 20})
			if err != nil {
				t.Fatalf("%+v: %v", tc, err)
			}
			seen := map[string]bool{}
			for _, r := range page.Items {
				id := r["id"].(string)
				if seen[id] {
					t.Fatalf("%+v duplicate %s", tc, id)
				}
				seen[id] = true
			}
			if len(seen) != 18 {
				t.Fatalf("%+v rows=%d", tc, len(seen))
			}
		})
	}
	randomPage, err := records.List(ctx, ListRequest{Principal: principal, Collection: collection.Name, Sort: "@random", Limit: 20})
	if err != nil || len(randomPage.Items) != 18 || randomPage.NextCursor != "" {
		t.Fatalf("random page=%+v err=%v", randomPage, err)
	}
	allow := accessrule.Rule{Op: "allow"}
	view, err := ParseAccessRule("@request.method = 'GET'")
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"closure_people", "closure_roots"} {
		if _, err := recordaccess.NewService(pool).Set(ctx, name, true); err != nil {
			t.Fatal(err)
		}
		rule := allow
		if name == "closure_people" {
			rule = view
		}
		raw, _ := json.Marshal(accessrule.Rules{List: allow, View: rule, Create: allow, Update: allow, Delete: allow})
		if _, err := pool.Exec(ctx, `INSERT INTO pgbase_meta.collection_rules(collection_id,rules) VALUES($1,$2)`, catalog.Collections[name].ID, raw); err != nil {
			t.Fatal(err)
		}
	}
	for _, tc := range []struct{ filter, sort string }{{"owner.name = 'Alice'", ""}, {"", "owner.name"}, {"", "links.name"}} {
		request := ListRequest{Principal: Principal{Kind: identity.Anonymous}, Collection: collection.Name, Filter: tc.filter, Sort: tc.sort, Limit: 1, RuleContext: &accessrule.RequestContext{Method: "GET"}}
		first, err := records.List(ctx, request)
		if err != nil {
			t.Fatalf("request %+v: %v", tc, err)
		}
		request.Cursor = first.NextCursor
		second, err := records.List(ctx, request)
		if err != nil || len(first.Items) != 1 || len(second.Items) != 1 || first.Items[0]["id"] == second.Items[0]["id"] {
			t.Fatalf("request continuation %+v: first=%+v second=%+v err=%v", tc, first, second, err)
		}
		request.Cursor = ""
		request.RuleContext.Method = "POST"
		page, err := records.List(ctx, request)
		if err != nil {
			t.Fatal(err)
		}
		if tc.filter != "" && len(page.Items) != 0 {
			t.Fatal("denied relation matched")
		}
	}
}

func TestClosureExpandedRelationRedactionMatchesDirectRead(t *testing.T) {
	pool, schemas, records := newRecordServiceTest(t)
	ctx := context.Background()
	applyRecordCollection(t, schemas, schema.CreateCollectionInput{Name: "closure_secret"})
	for _, tc := range []struct{ name, field, target string }{{"closure_author", "secret", "closure_secret"}, {"closure_post", "author", "closure_author"}} {
		applyRecordCollection(t, schemas, schema.CreateCollectionInput{Name: tc.name, Fields: []schema.FieldInput{{Name: tc.field, Type: schema.TypeRelation, Relation: &schema.RelationInput{TargetCollection: tc.target, Cardinality: schema.RelationSingle}}}})
	}
	secret, err := records.Create(ctx, "closure_secret", nil)
	if err != nil {
		t.Fatal(err)
	}
	author, err := records.Create(ctx, "closure_author", map[string]json.RawMessage{"secret": json.RawMessage(`"` + secret["id"].(string) + `"`)})
	if err != nil {
		t.Fatal(err)
	}
	post, err := records.Create(ctx, "closure_post", map[string]json.RawMessage{"author": json.RawMessage(`"` + author["id"].(string) + `"`)})
	if err != nil {
		t.Fatal(err)
	}
	catalog, err := schemas.Catalog(ctx)
	if err != nil {
		t.Fatal(err)
	}
	allow := accessrule.Rule{Op: "allow"}
	for _, name := range []string{"closure_secret", "closure_author", "closure_post"} {
		if _, err := recordaccess.NewService(pool).Set(ctx, name, true); err != nil {
			t.Fatal(err)
		}
		view := allow
		if name == "closure_secret" {
			view = accessrule.Rule{Op: "deny"}
		}
		raw, _ := json.Marshal(accessrule.Rules{List: allow, View: view, Create: allow, Update: allow, Delete: allow})
		if _, err := pool.Exec(ctx, `INSERT INTO pgbase_meta.collection_rules(collection_id,rules) VALUES($1,$2)`, catalog.Collections[name].ID, raw); err != nil {
			t.Fatal(err)
		}
	}
	principal := Principal{Kind: identity.Anonymous}
	direct, err := records.Get(ctx, GetRequest{Principal: principal, Collection: "closure_author", ID: uuid.MustParse(author["id"].(string))})
	if err != nil {
		t.Fatal(err)
	}
	expanded, err := records.Get(ctx, GetRequest{Principal: principal, Collection: "closure_post", ID: uuid.MustParse(post["id"].(string)), Expand: "author"})
	if err != nil {
		t.Fatal(err)
	}
	nested := expanded.Data["expand"].(map[string]any)["author"].(map[string]any)
	if direct.Data["secret"] != nil || nested["secret"] != nil {
		t.Fatalf("direct=%v expanded=%v", direct.Data, nested)
	}
}
