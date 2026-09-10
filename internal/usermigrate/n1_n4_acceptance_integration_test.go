//go:build integration

package usermigrate

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strconv"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/yakisoba0728/PgBase/internal/database"
	"github.com/yakisoba0728/PgBase/internal/schema"
)

func TestN1N4ConcreteFieldOptionsSnapshotSourceUpDown(t *testing.T) {
	ctx := context.Background()
	source := userMigrationPool(t)
	actor := uuid.MustParse("0199ab10-0000-7000-8000-000000000510")
	insertSnapshotAdministrator(t, source, actor, "n1-n4-source@example.test")
	service := schema.NewService(source, &schema.Cache{}, nil)
	empty := exportSnapshot(t, source)

	minimumText, maximumText := 3, 20
	minimumPassword, maximumPassword := 8, 64
	minimumSelect, maximumSelect := 1, 2
	maximumFiles := 3
	minimumAmount, maximumAmount := "0.5", "100.25"
	maximumFileSize, maximumEditorSize := int64(4096), int64(8192)
	authors := applyN1N4SchemaChange(t, service, actor, schema.NewCreateChange(schema.CreateCollectionInput{
		Name: "n1_n4_authors", Fields: []schema.FieldInput{{Name: "name", Type: schema.TypeText, Required: true, Default: json.RawMessage(`"unknown"`)}},
	}))
	fields := []schema.FieldInput{
		{Name: "status", Type: schema.TypeText, Required: true, Default: json.RawMessage(`"draft"`), Presentable: true, Help: "Publication state", Options: &schema.FieldOptions{
			MinLength: &minimumText, MaxLength: &maximumText, Pattern: `^[a-z]+$`, AllowedValues: []string{"published", "draft"},
		}},
		{Name: "slug", Type: schema.TypeText, Required: true, Unique: true, Options: &schema.FieldOptions{
			Pattern: `^item-[a-z0-9]{8}$`, AutogeneratePattern: `item-[a-z0-9]{8}`,
		}},
		{Name: "contact", Type: schema.TypeText, Hidden: true, Help: "Private contact", Options: &schema.FieldOptions{
			Format: "email", AllowedDomains: []string{"Example.COM.", "api.example.com"}, DeniedDomains: []string{"Private.Example.com."},
		}},
		{Name: "amount", Type: schema.TypeNumeric, Default: json.RawMessage(`"1.5"`), Options: &schema.FieldOptions{Min: &minimumAmount, Max: &maximumAmount}},
		{Name: "labels", Type: schema.TypeJSONB, Default: json.RawMessage(`["alpha"]`), Options: &schema.FieldOptions{
			AllowedValues: []string{"beta", "alpha"}, MinSelect: &minimumSelect, MaxSelect: &maximumSelect,
		}},
		{Name: "attachment", Type: schema.TypeFile, Options: &schema.FieldOptions{
			MaxFileSize: &maximumFileSize, MaxSelect: &maximumFiles, MimeTypes: []string{"image/*", "application/pdf"}, Thumbs: []string{"200x100t", "100x100"},
		}},
		{Name: "body", Type: schema.TypeEditor, Help: "Rich body", Options: &schema.FieldOptions{MaxSize: &maximumEditorSize, ConvertURLs: true}},
		{Name: "changed", Type: schema.TypeAutoDate, Indexed: true, Options: &schema.FieldOptions{OnCreate: true, OnUpdate: true}},
		{Name: "credential", Type: schema.TypePassword, Hidden: true, Options: &schema.FieldOptions{
			MinLength: &minimumPassword, MaxLength: &maximumPassword, Pattern: `^[a-zA-Z0-9-]+$`,
		}},
		{Name: "author", Type: schema.TypeRelation, Indexed: true, Relation: &schema.RelationInput{
			TargetCollection: authors.Name, Cardinality: schema.RelationSingle, MinSelect: intTestPointer(0), MaxSelect: intTestPointer(1), OnDelete: schema.RelationSetNull,
		}},
	}
	applyN1N4SchemaChange(t, service, actor, schema.NewCreateChange(schema.CreateCollectionInput{
		Name: "n1_n4_records", Fields: fields,
		Indexes: []schema.IndexInput{{Name: "ordered_lookup", Fields: []string{"status", "amount", "contact"}}},
	}))
	first := exportSnapshot(t, source)
	if again := exportSnapshot(t, source); !bytes.Equal(first, again) {
		t.Fatalf("canonical snapshot bytes changed:\n%s\n%s", first, again)
	}
	firstCollection := n1N4SnapshotCollection(t, first, "n1_n4_records")
	assertN1N4ConcreteContract(t, firstCollection, "status", []string{"status", "amount", "contact"}, []string{"api.example.com", "example.com"}, "100.25")

	firstGenerated, err := GenerateSnapshotSource("20260910010000_n1_n4_options", "migrations", empty, first)
	if err != nil {
		t.Fatal(err)
	}
	firstAgain, err := GenerateSnapshotSource("20260910010000_n1_n4_options", "migrations", empty, first)
	if err != nil || !bytes.Equal(firstGenerated.Source, firstAgain.Source) {
		t.Fatalf("generated source is not deterministic: %v", err)
	}
	assertGeneratedSnapshots(t, firstGenerated.Source, empty, first)
	if !strings.Contains(strings.Join(firstGenerated.Impact.Operations, "\n"), "create collection n1_n4_records") {
		t.Fatalf("initial diff=%+v", firstGenerated.Impact)
	}

	target := userMigrationPool(t)
	firstMigration := generatedMigration(firstGenerated, empty, first)
	runner := snapshotRunner(t, target, []Migration{firstMigration})
	if results, err := runner.Up(ctx); err != nil || len(results) != 1 {
		t.Fatalf("fresh Up=%+v err=%v", results, err)
	}
	requireEquivalentSnapshot(t, target, first)
	authorID := uuid.MustParse("0199ab10-0000-7000-8000-000000000511")
	recordID := uuid.MustParse("0199ab10-0000-7000-8000-000000000512")
	if _, err := target.Exec(ctx, `insert into app.n1_n4_authors(id,created_at,updated_at,name) values($1,current_timestamp,current_timestamp,'Ada')`, authorID); err != nil {
		t.Fatal(err)
	}
	if _, err := target.Exec(ctx, `insert into app.n1_n4_records(id,created_at,updated_at,status,slug,contact,amount,labels,body,author) values($1,current_timestamp,current_timestamp,'published','item-abcd1234','reader@example.com',42.5,'["alpha","beta"]','<p>kept</p>',$2)`, recordID, authorID); err != nil {
		t.Fatal(err)
	}

	updatedFields := n1N4CloneFields(t, firstCollection.Fields)
	for index := range updatedFields {
		switch updatedFields[index].Name {
		case "status":
			updatedFields[index].Name = "state"
			updatedFields[index].Help = "Renamed publication state"
		case "contact":
			options := *updatedFields[index].Options
			options.AllowedDomains = append(append([]string(nil), options.AllowedDomains...), "docs.example.com")
			updatedFields[index].Options = &options
		case "amount":
			options := *updatedFields[index].Options
			nextMaximum := "200.5"
			options.Max = &nextMaximum
			updatedFields[index].Options = &options
		}
	}
	secondIndexes := []schema.IndexInput{{Name: "ordered_lookup", Fields: []string{"contact", "state", "amount"}}}
	applyN1N4SchemaChange(t, service, actor, schema.NewUpdateChange(schema.UpdateCollectionInput{Name: "n1_n4_records", Fields: updatedFields, Indexes: &secondIndexes}))
	second := exportSnapshot(t, source)
	if equivalent, err := schema.MigrationSnapshotsEquivalent(first, second); err != nil || equivalent {
		t.Fatalf("changed snapshots equivalent=%t err=%v", equivalent, err)
	}
	secondCollection := n1N4SnapshotCollection(t, second, "n1_n4_records")
	assertN1N4ConcreteContract(t, secondCollection, "state", []string{"contact", "state", "amount"}, []string{"api.example.com", "docs.example.com", "example.com"}, "200.5")
	secondGenerated, err := GenerateSnapshotSource("20260910010001_n1_n4_rename", "migrations", first, second)
	if err != nil {
		t.Fatal(err)
	}
	if len(secondGenerated.Impact.Irreversible) != 0 || !strings.Contains(strings.Join(secondGenerated.Impact.Operations, "\n"), "rename field n1_n4_records.status to state") {
		t.Fatalf("reversible diff=%+v", secondGenerated.Impact)
	}
	assertGeneratedSnapshots(t, secondGenerated.Source, first, second)

	secondMigration := generatedMigration(secondGenerated, first, second)
	runner = snapshotRunner(t, target, []Migration{firstMigration, secondMigration})
	if results, err := runner.Up(ctx); err != nil || len(results) != 1 {
		t.Fatalf("transition Up=%+v err=%v", results, err)
	}
	requireEquivalentSnapshot(t, target, second)
	assertN1N4StoredRecord(t, target, recordID, authorID)
	if result, err := runner.Down(ctx); err != nil || result.Migration.ID != secondMigration.ID || result.Direction != "down" || result.Applied {
		t.Fatalf("supported Down=%+v err=%v", result, err)
	}
	requireEquivalentSnapshot(t, target, first)
	assertN1N4StoredRecord(t, target, recordID, authorID)
}

func applyN1N4SchemaChange(t *testing.T, service *schema.Service, actor uuid.UUID, change schema.Change) schema.Collection {
	t.Helper()
	plan, err := service.Preview(context.Background(), change)
	if err != nil {
		var validation *schema.ValidationError
		if errors.As(err, &validation) {
			t.Fatalf("preview schema change: %v details=%+v", err, validation.Details)
		}
		t.Fatal(err)
	}
	result, err := service.Apply(context.Background(), schema.LegacyApply(change, plan, actor))
	if err != nil {
		var validation *schema.ValidationError
		if errors.As(err, &validation) {
			t.Fatalf("apply schema change: %v details=%+v", err, validation.Details)
		}
		t.Fatal(err)
	}
	return result.Collection
}

func n1N4SnapshotCollection(t *testing.T, raw []byte, name string) schema.MigrationSnapshotCollection {
	t.Helper()
	var snapshot schema.MigrationSnapshot
	if err := json.Unmarshal(raw, &snapshot); err != nil {
		t.Fatal(err)
	}
	for _, collection := range snapshot.Collections {
		if collection.Name == name {
			return collection
		}
	}
	t.Fatalf("missing snapshot collection %s", name)
	return schema.MigrationSnapshotCollection{}
}

func n1N4CloneFields(t *testing.T, fields []schema.FieldInput) []schema.FieldInput {
	t.Helper()
	raw, err := json.Marshal(fields)
	if err != nil {
		t.Fatal(err)
	}
	var cloned []schema.FieldInput
	if err := json.Unmarshal(raw, &cloned); err != nil {
		t.Fatal(err)
	}
	return cloned
}

func assertN1N4ConcreteContract(t *testing.T, collection schema.MigrationSnapshotCollection, statusName string, indexFields, allowedDomains []string, maximumAmount string) {
	t.Helper()
	if len(collection.Indexes) != 1 || collection.Indexes[0].Name != "ordered_lookup" || collection.Indexes[0].Unique || !reflect.DeepEqual(collection.Indexes[0].Fields, indexFields) {
		t.Fatalf("ordered index=%+v", collection.Indexes)
	}
	fields := make(map[string]schema.FieldInput, len(collection.Fields))
	order := make([]string, 0, len(collection.Fields))
	for _, field := range collection.Fields {
		fields[field.Name] = field
		order = append(order, field.Name)
	}
	expectedOrder := []string{statusName, "slug", "contact", "amount", "labels", "attachment", "body", "changed", "credential", "author"}
	if !reflect.DeepEqual(order, expectedOrder) {
		t.Fatalf("field order=%v", order)
	}
	status := fields[statusName]
	if !status.Required || !status.Presentable || status.Help == "" || string(status.Default) != `"draft"` || status.Options == nil ||
		status.Options.MinLength == nil || *status.Options.MinLength != 3 || status.Options.MaxLength == nil || *status.Options.MaxLength != 20 ||
		status.Options.Pattern != `^[a-z]+$` || !reflect.DeepEqual(status.Options.AllowedValues, []string{"draft", "published"}) {
		t.Fatalf("status contract=%+v", status)
	}
	slug := fields["slug"]
	if !slug.Required || !slug.Unique || len(slug.Default) != 0 || slug.Options == nil || slug.Options.Pattern != `^item-[a-z0-9]{8}$` || slug.Options.AutogeneratePattern != `item-[a-z0-9]{8}` {
		t.Fatalf("slug contract=%+v", slug)
	}
	contact := fields["contact"]
	if !contact.Hidden || contact.Help != "Private contact" || contact.Options == nil || contact.Options.Format != "email" ||
		!reflect.DeepEqual(contact.Options.AllowedDomains, allowedDomains) || !reflect.DeepEqual(contact.Options.DeniedDomains, []string{"private.example.com"}) {
		t.Fatalf("contact contract=%+v", contact)
	}
	amount := fields["amount"]
	if string(amount.Default) != `"1.5"` || amount.Options == nil || amount.Options.Min == nil || *amount.Options.Min != "0.5" || amount.Options.Max == nil || *amount.Options.Max != maximumAmount {
		t.Fatalf("amount contract=%+v", amount)
	}
	labels := fields["labels"]
	var labelDefault []string
	if err := json.Unmarshal(labels.Default, &labelDefault); err != nil {
		t.Fatalf("labels default=%s: %v", labels.Default, err)
	}
	if !reflect.DeepEqual(labelDefault, []string{"alpha"}) || labels.Options == nil || !reflect.DeepEqual(labels.Options.AllowedValues, []string{"alpha", "beta"}) ||
		labels.Options.MinSelect == nil || *labels.Options.MinSelect != 1 || labels.Options.MaxSelect == nil || *labels.Options.MaxSelect != 2 {
		t.Fatalf("labels contract=%+v", labels)
	}
	attachment := fields["attachment"]
	if attachment.Options == nil || attachment.Options.MaxFileSize == nil || *attachment.Options.MaxFileSize != 4096 || attachment.Options.MaxSelect == nil || *attachment.Options.MaxSelect != 3 ||
		!reflect.DeepEqual(attachment.Options.MimeTypes, []string{"application/pdf", "image/*"}) || !reflect.DeepEqual(attachment.Options.Thumbs, []string{"100x100", "200x100t"}) {
		t.Fatalf("file contract=%+v", attachment)
	}
	body := fields["body"]
	if body.Help != "Rich body" || body.Options == nil || body.Options.MaxSize == nil || *body.Options.MaxSize != 8192 || !body.Options.ConvertURLs {
		t.Fatalf("editor contract=%+v", body)
	}
	changed := fields["changed"]
	if !changed.Indexed || changed.Options == nil || !changed.Options.OnCreate || !changed.Options.OnUpdate {
		t.Fatalf("autodate contract=%+v", changed)
	}
	credential := fields["credential"]
	if !credential.Hidden || credential.Options == nil || credential.Options.MinLength == nil || *credential.Options.MinLength != 8 ||
		credential.Options.MaxLength == nil || *credential.Options.MaxLength != 64 || credential.Options.Pattern != `^[a-zA-Z0-9-]+$` {
		t.Fatalf("password contract=%+v", credential)
	}
	author := fields["author"]
	if !author.Indexed || author.Relation == nil || author.Relation.TargetCollection != "n1_n4_authors" || author.Relation.Cardinality != schema.RelationSingle ||
		author.Relation.MinSelect == nil || *author.Relation.MinSelect != 0 || author.Relation.MaxSelect == nil || *author.Relation.MaxSelect != 1 || author.Relation.OnDelete != schema.RelationSetNull {
		t.Fatalf("relation contract=%+v", author)
	}
}

func assertGeneratedSnapshots(t *testing.T, source, before, after []byte) {
	t.Helper()
	for name, snapshot := range map[string][]byte{"before": before, "after": after} {
		canonical, err := schema.CanonicalMigrationSnapshot(snapshot)
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(canonical, snapshot) || !bytes.Contains(source, []byte(strconv.Quote(string(snapshot)))) {
			t.Fatalf("generated source lost canonical %s snapshot", name)
		}
	}
}

func assertN1N4StoredRecord(t *testing.T, target database.DBTX, recordID, authorID uuid.UUID) {
	t.Helper()
	var status, slug, contact, amount, labels, body string
	var storedAuthor uuid.UUID
	if err := target.QueryRow(context.Background(), `select status,slug,contact,amount::text,labels::text,body,author from app.n1_n4_records where id=$1`, recordID).
		Scan(&status, &slug, &contact, &amount, &labels, &body, &storedAuthor); err != nil {
		t.Fatal(err)
	}
	if status != "published" || slug != "item-abcd1234" || contact != "reader@example.com" || amount != "42.5" || labels != `["alpha", "beta"]` || body != "<p>kept</p>" || storedAuthor != authorID {
		t.Fatalf("stored record status=%q slug=%q contact=%q amount=%q labels=%q body=%q author=%s", status, slug, contact, amount, labels, body, storedAuthor)
	}
}
