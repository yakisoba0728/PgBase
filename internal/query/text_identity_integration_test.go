//go:build integration

package query

import (
	"context"
	"encoding/json"
	"reflect"
	"testing"

	"github.com/google/uuid"
	"github.com/yakisoba0728/PgBase/internal/schema"
)

func TestPocketBaseListUsesExactTextIdentityForFiltersAndSorts(t *testing.T) {
	pool, schemas, records := newRecordServiceTest(t)
	ctx := context.Background()
	policy := &schema.RecordIDPolicy{Kind: schema.RecordIDText, MinLength: 1, MaxLength: 32, Pattern: `^[A-Za-z0-9]+$`}
	applyRecordCollection(t, schemas, schema.CreateCollectionInput{
		Name: "query_text_identities", RecordID: policy,
		Fields: []schema.FieldInput{{Name: "title", Type: schema.TypeText, Required: true, Indexed: true}},
	})
	ids := make(map[string]uuid.UUID)
	for alias, title := range map[string]string{"Zulu": "last", "Alpha": "first"} {
		record, err := records.Create(ctx, "query_text_identities", map[string]json.RawMessage{"title": json.RawMessage(`"` + title + `"`)})
		if err != nil {
			t.Fatal(err)
		}
		ids[alias] = uuid.MustParse(record["id"].(string))
		if _, err := pool.Exec(ctx, `insert into pgbase_meta.record_identities(collection_id,collection_kind,scope_id,internal_id,external_id) select id,'base',id,$2,$3 from pgbase_meta.collections where name=$1`, "query_text_identities", ids[alias], alias); err != nil {
			t.Fatal(err)
		}
	}
	principal := Principal{Kind: PrincipalAdministrator, ID: uuid.New()}
	filtered, err := ListPocketBase(ctx, records, PocketBaseListRequest{ListRequest: ListRequest{
		Principal: principal, Collection: "query_text_identities", Filter: `id = "Alpha"`, Sort: "id",
	}, Page: 1, PerPage: 10})
	if err != nil || !reflect.DeepEqual(recordIDs(filtered.Items), []string{ids["Alpha"].String()}) {
		t.Fatalf("exact id filter = %v, err=%v", recordIDs(filtered.Items), err)
	}
	nativeFallback, err := ListPocketBase(ctx, records, PocketBaseListRequest{ListRequest: ListRequest{
		Principal: principal, Collection: "query_text_identities", Filter: `id = "` + ids["Alpha"].String() + `"`, Sort: "id",
	}, Page: 1, PerPage: 10})
	if err != nil || len(nativeFallback.Items) != 0 {
		t.Fatalf("internal UUID fallback items=%v err=%v", recordIDs(nativeFallback.Items), err)
	}
	ordered, err := ListPocketBase(ctx, records, PocketBaseListRequest{ListRequest: ListRequest{
		Principal: principal, Collection: "query_text_identities", Sort: "id",
	}, Page: 1, PerPage: 10})
	if err != nil || !reflect.DeepEqual(recordIDs(ordered.Items), []string{ids["Alpha"].String(), ids["Zulu"].String()}) {
		t.Fatalf("external id order = %v, err=%v", recordIDs(ordered.Items), err)
	}
}

func TestPocketBaseRelationIdentityFilterAndSortUseTargetAliases(t *testing.T) {
	pool, schemas, records := newRecordServiceTest(t)
	ctx := context.Background()
	policy := &schema.RecordIDPolicy{Kind: schema.RecordIDText, MinLength: 1, MaxLength: 32, Pattern: `^[A-Za-z0-9]+$`}
	applyRecordCollection(t, schemas, schema.CreateCollectionInput{Name: "query_identity_people", RecordID: policy})
	applyRecordCollection(t, schemas, schema.CreateCollectionInput{
		Name: "query_identity_posts", RecordID: policy,
		Fields: []schema.FieldInput{{Name: "owner", Type: schema.TypeRelation, Indexed: true, Relation: &schema.RelationInput{TargetCollection: "query_identity_people", Cardinality: schema.RelationSingle}}},
	})
	people := make(map[string]uuid.UUID)
	for _, alias := range []string{"Zulu", "Alpha"} {
		record, err := records.Create(ctx, "query_identity_people", nil)
		if err != nil {
			t.Fatal(err)
		}
		people[alias] = uuid.MustParse(record["id"].(string))
		if _, err := pool.Exec(ctx, `insert into pgbase_meta.record_identities(collection_id,collection_kind,scope_id,internal_id,external_id) select id,'base',id,$2,$3 from pgbase_meta.collections where name=$1`, "query_identity_people", people[alias], alias); err != nil {
			t.Fatal(err)
		}
	}
	posts := make(map[string]uuid.UUID)
	for alias, owner := range map[string]string{"PostZulu": "Zulu", "PostAlpha": "Alpha"} {
		record, err := records.Create(ctx, "query_identity_posts", map[string]json.RawMessage{"owner": json.RawMessage(`"` + people[owner].String() + `"`)})
		if err != nil {
			t.Fatal(err)
		}
		posts[alias] = uuid.MustParse(record["id"].(string))
		if _, err := pool.Exec(ctx, `insert into pgbase_meta.record_identities(collection_id,collection_kind,scope_id,internal_id,external_id) select id,'base',id,$2,$3 from pgbase_meta.collections where name=$1`, "query_identity_posts", posts[alias], alias); err != nil {
			t.Fatal(err)
		}
	}
	principal := Principal{Kind: PrincipalAdministrator, ID: uuid.New()}
	filtered, err := ListPocketBase(ctx, records, PocketBaseListRequest{ListRequest: ListRequest{
		Principal: principal, Collection: "query_identity_posts", Filter: `owner.id = "Zulu"`, Sort: "owner.id",
	}, Page: 1, PerPage: 10})
	if err != nil || !reflect.DeepEqual(recordIDs(filtered.Items), []string{posts["PostZulu"].String()}) {
		t.Fatalf("relation identity filter = %v, err=%v", recordIDs(filtered.Items), err)
	}
	ordered, err := ListPocketBase(ctx, records, PocketBaseListRequest{ListRequest: ListRequest{
		Principal: principal, Collection: "query_identity_posts", Sort: "owner.id",
	}, Page: 1, PerPage: 10})
	if err != nil || !reflect.DeepEqual(recordIDs(ordered.Items), []string{posts["PostAlpha"].String(), posts["PostZulu"].String()}) {
		t.Fatalf("relation identity order = %v, err=%v", recordIDs(ordered.Items), err)
	}
}
