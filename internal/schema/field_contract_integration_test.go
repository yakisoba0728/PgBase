//go:build integration

package schema

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"github.com/google/uuid"
	"testing"
)

func TestFieldIDRenamePreservesRowsIndexesAndMetadataReferences(t *testing.T) {
	ctx := context.Background()
	pool, service, admin := newSchemaServiceTest(t)
	created := applyFreshPlan(t, service, CreateCollectionInput{Name: "rename_items", Fields: []FieldInput{{Name: "title", Type: TypeText, Indexed: true}}}, admin)
	if _, err := pool.Exec(ctx, `insert into app.rename_items(id,created_at,updated_at,title) values(gen_random_uuid(),now(),now(),'kept')`); err != nil {
		t.Fatal(err)
	}
	id := created.Fields[0].ID
	result := applyChange(t, service, NewUpdateChange(UpdateCollectionInput{Name: "rename_items", Fields: []FieldInput{{ID: &id, Name: "headline", Type: TypeText, Indexed: true}}}), admin)
	if result.Collection.Fields[0].ID != id || result.Collection.Fields[0].ColumnName != "title" {
		t.Fatalf("renamed field=%#v", result.Collection.Fields[0])
	}
	var value string
	if err := pool.QueryRow(ctx, `select title from app.rename_items`).Scan(&value); err != nil || value != "kept" {
		t.Fatalf("value=%q err=%v", value, err)
	}
	var metadataName string
	if err := pool.QueryRow(ctx, `select name from pgbase_meta.fields where id=$1`, id).Scan(&metadataName); err != nil || metadataName != "headline" {
		t.Fatalf("name=%q err=%v", metadataName, err)
	}
}

func TestTightenedFieldOptionsRejectExistingInvalidRows(t *testing.T) {
	ctx := context.Background()
	pool, service, admin := newSchemaServiceTest(t)
	created := applyFreshPlan(t, service, CreateCollectionInput{Name: "option_items", Fields: []FieldInput{{Name: "title", Type: TypeText}}}, admin)
	if _, err := pool.Exec(ctx, `insert into app.option_items(id,created_at,updated_at,title) values(gen_random_uuid(),now(),now(),'x')`); err != nil {
		t.Fatal(err)
	}
	id := created.Fields[0].ID
	minimum := 2
	change := NewUpdateChange(UpdateCollectionInput{Name: "option_items", Fields: []FieldInput{{ID: &id, Name: "title", Type: TypeText, Options: &FieldOptions{MinLength: &minimum}}}})
	plan, err := service.Preview(ctx, change)
	if err != nil {
		t.Fatal(err)
	}
	_, err = service.Apply(ctx, LegacyApply(change, plan, admin))
	if !errors.Is(err, ErrDataConstraint) {
		t.Fatalf("apply error=%v", err)
	}
	catalog, err := service.Catalog(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if catalog.Version != 1 || len(catalog.Collections["option_items"].Fields[0].Options.AllowedValues) != 0 || catalog.Collections["option_items"].Fields[0].Options.MinLength != nil {
		encoded, _ := json.Marshal(catalog)
		t.Fatalf("catalog changed: %s", encoded)
	}
}

func TestFileFieldRenamePreservesMetadataAndDoesNotQueueObjectDeletion(t *testing.T) {
	ctx := context.Background()
	pool, service, admin := newSchemaServiceTest(t)
	created := applyFreshPlan(t, service, CreateCollectionInput{Name: "file_contracts", Fields: []FieldInput{{Name: "attachment", Type: TypeFile}, {Name: "note", Type: TypeText}}}, admin)
	recordID := uuid.New()
	eventID := uuid.New()
	fileID := uuid.New()
	objectID := uuid.New()
	if _, err := pool.Exec(ctx, `insert into app.file_contracts(id,created_at,updated_at,note) values($1,now(),now(),'kept')`, recordID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `insert into pgbase_meta.record_audit_events(event_id,actor_kind,actor_id,request_id,collection_id,collection_name,record_id,operation,schema_version,schema_digest,previous_version,new_version,previous_etag,new_etag,changed_fields,input_hash,idempotency_key_hash,compatibility,committed_at) values($1,'administrator',$2,'field-contract-file',$3,'file_contracts',$4,'create',1,decode(repeat('aa',32),'hex'),null,1,null,'"pgb1.file"',array['note'],decode(repeat('bb',32),'hex'),decode(repeat('cc',32),'hex'),'none',now())`, eventID, admin, created.ID, recordID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `insert into pgbase_meta.record_revisions(collection_id,record_id,version,validator,last_event_id,updated_at) values($1,$2,1,decode(repeat('dd',32),'hex'),$3,now())`, created.ID, recordID, eventID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `insert into pgbase_meta.record_files(id,collection_id,record_id,field_name,filename,content_type,size_bytes,sha256,object_key,status,created_at,ready_at) values($1,$2,$3,'attachment','kept.txt','text/plain',4,decode(repeat('ee',32),'hex'),'objects/'||$4,'ready',now(),now())`, fileID, created.ID, recordID, objectID.String()); err != nil {
		t.Fatal(err)
	}
	fileFieldID := created.Fields[0].ID
	noteID := created.Fields[1].ID
	applyChange(t, service, NewUpdateChange(UpdateCollectionInput{Name: "file_contracts", Fields: []FieldInput{{ID: &fileFieldID, Name: "document", Type: TypeFile}, {ID: &noteID, Name: "note", Type: TypeText, Indexed: true}}}), admin)
	var fieldName string
	var queued int
	if err := pool.QueryRow(ctx, `select field_name from pgbase_meta.record_files where id=$1`, fileID).Scan(&fieldName); err != nil || fieldName != "document" {
		t.Fatalf("file metadata=%q err=%v", fieldName, err)
	}
	if err := pool.QueryRow(ctx, `select count(*) from pgbase_meta.file_object_deletions`).Scan(&queued); err != nil || queued != 0 {
		t.Fatalf("queued deletions=%d err=%v", queued, err)
	}
}

func TestMultipleRelationRenameSupportsLaterEditAndDrop(t *testing.T) {
	ctx := context.Background()
	pool, service, admin := newSchemaServiceTest(t)
	applyFreshPlan(t, service, CreateCollectionInput{Name: "relation_targets", Fields: []FieldInput{{Name: "name", Type: TypeText}}}, admin)
	maximum := 4
	created := applyFreshPlan(t, service, CreateCollectionInput{Name: "relation_sources", Fields: []FieldInput{{Name: "members", Type: TypeRelation, Relation: &RelationInput{TargetCollection: "relation_targets", Cardinality: RelationMultiple, MaxSelect: &maximum}}}}, admin)
	id := created.Fields[0].ID
	physical := created.Fields[0].Relation.Storage.Name
	maximum = 6
	applyChange(t, service, NewUpdateChange(UpdateCollectionInput{Name: "relation_sources", Fields: []FieldInput{{ID: &id, Name: "reviewers", Type: TypeRelation, Relation: &RelationInput{TargetCollection: "relation_targets", Cardinality: RelationMultiple, MaxSelect: &maximum}}}}), admin)
	var exists bool
	if err := pool.QueryRow(ctx, `select to_regclass($1) is not null`, "pgbase_relation."+physical).Scan(&exists); err != nil || !exists {
		t.Fatalf("renamed edge exists=%t err=%v", exists, err)
	}
	applyChange(t, service, NewUpdateChange(UpdateCollectionInput{Name: "relation_sources", Fields: []FieldInput{}}), admin)
	if err := pool.QueryRow(ctx, `select to_regclass($1) is not null`, "pgbase_relation."+physical).Scan(&exists); err != nil || exists {
		t.Fatalf("dropped edge exists=%t err=%v", exists, err)
	}
}

func TestRenameUpdatesViewAndRuleFieldReferencesWithoutTouchingLiteralValues(t *testing.T) {
	ctx := context.Background()
	pool, service, admin := newSchemaServiceTest(t)
	source := applyFreshPlan(t, service, CreateCollectionInput{Name: "rename_rules", Fields: []FieldInput{{Name: "title", Type: TypeText}, {Name: "metadata", Type: TypeJSONB}}}, admin)
	if _, err := pool.Exec(ctx, `insert into app.rename_rules(id,created_at,updated_at,title,metadata) values(gen_random_uuid(),now(),now(),'visible','{"field":"metadata","nested":{"field":"metadata"}}')`); err != nil {
		t.Fatal(err)
	}
	filter := &ViewFilter{Op: "eq", Field: "title", Value: json.RawMessage(`"visible"`)}
	applyFreshPlan(t, service, CreateCollectionInput{Name: "rename_rule_view", View: &ViewInput{Source: "rename_rules", Projection: []string{"title"}, Filter: filter}}, admin)
	rules := `{"list":{"op":"eq","field":"metadata","value":{"field":"metadata","nested":{"field":"metadata"}}},"view":{"op":"allow"},"create":{"op":"allow"},"update":{"op":"allow"},"delete":{"op":"allow"}}`
	if _, err := pool.Exec(ctx, `insert into pgbase_meta.collection_rules(collection_id,rules) values($1,$2::jsonb)`, source.ID, rules); err != nil {
		t.Fatal(err)
	}
	id := source.Fields[0].ID
	metadataID := source.Fields[1].ID
	applyChange(t, service, NewUpdateChange(UpdateCollectionInput{Name: "rename_rules", Fields: []FieldInput{{ID: &id, Name: "headline", Type: TypeText}, {ID: &metadataID, Name: "payload", Type: TypeJSONB}}}), admin)
	var projection []string
	var viewFilter, storedRules []byte
	if err := pool.QueryRow(ctx, `select projection,filter_json from pgbase_meta.collection_views where source_collection_id=$1`, source.ID).Scan(&projection, &viewFilter); err != nil {
		t.Fatal(err)
	}
	if len(projection) != 1 || projection[0] != "headline" || !bytes.Contains(viewFilter, []byte(`"field": "headline"`)) {
		t.Fatalf("projection/filter=%v %s", projection, viewFilter)
	}
	if err := pool.QueryRow(ctx, `select rules from pgbase_meta.collection_rules where collection_id=$1`, source.ID).Scan(&storedRules); err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(storedRules, []byte(`"field": "payload"`)) || !bytes.Contains(storedRules, []byte(`"nested": {"field": "metadata"}`)) {
		t.Fatalf("rules=%s", storedRules)
	}
	var visible string
	if err := pool.QueryRow(ctx, `select title from app.rename_rule_view`).Scan(&visible); err != nil || visible != "visible" {
		t.Fatalf("renamed view row=%q err=%v", visible, err)
	}
	catalog, err := service.Catalog(ctx)
	if err != nil {
		t.Fatal(err)
	}
	view := catalog.Collections["rename_rule_view"]
	if view.Fields[0].Name != "headline" || view.Fields[0].ColumnName != "title" {
		t.Fatalf("renamed view catalog=%#v", view.Fields[0])
	}
}
