package realtime

import (
	"errors"
	"testing"

	"github.com/google/uuid"
	"github.com/yakisoba0728/PgBase/internal/identity"
	"github.com/yakisoba0728/PgBase/internal/query"
	"github.com/yakisoba0728/PgBase/internal/schema"
)

func TestValidateEventFieldsRejectsUnknownAndHiddenFieldsForPublicPrincipals(t *testing.T) {
	collection := schema.Collection{Fields: []schema.Field{
		{Name: "title"},
		{Name: "secret", Hidden: true},
	}}
	user := identity.Subject{Kind: identity.User, ID: uuid.New()}
	for _, test := range []struct {
		fields string
		code   string
	}{{"missing", "unknown_field"}, {"secret", "hidden_field"}} {
		err := validateEventFields(collection, user, test.fields)
		var validation *query.ValidationError
		if !errors.As(err, &validation) || len(validation.Details) != 1 || validation.Details[0].Code != test.code {
			t.Fatalf("validateEventFields(%q) = %#v, want %s", test.fields, err, test.code)
		}
	}
	if err := validateEventFields(collection, identity.Subject{Kind: identity.Administrator, ID: uuid.New()}, "secret"); err != nil {
		t.Fatalf("administrator hidden field projection: %v", err)
	}
}

func TestPublicSnapshotMapsPhysicalNamesAndDropsHiddenFields(t *testing.T) {
	collection := schema.Collection{ID: uuid.New(), Name: "articles", Fields: []schema.Field{{Name: "title", ColumnName: "fld_title"}, {Name: "secret", ColumnName: "fld_secret", Hidden: true}}}
	record, err := publicSnapshot(collection, []byte(`{"id":"018f3b2e-4200-7000-8000-000000000001","created_at":"2026-09-10T00:00:00Z","updated_at":"2026-09-10T00:00:00Z","fld_title":"shown","fld_secret":"hidden"}`))
	if err != nil {
		t.Fatal(err)
	}
	if record["title"] != "shown" || record["created"] == nil || record["collectionName"] != "articles" {
		t.Fatalf("record=%v", record)
	}
	if _, ok := record["secret"]; ok {
		t.Fatalf("hidden field leaked: %v", record)
	}
}
