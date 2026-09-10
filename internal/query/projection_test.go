package query

import (
	"testing"

	"github.com/google/uuid"
	"github.com/yakisoba0728/PgBase/internal/identity"
	"github.com/yakisoba0728/PgBase/internal/schema"
)

func TestCompileRecordProjectionSelectsSchemaAndSystemFields(t *testing.T) {
	collection := schema.Collection{Fields: []schema.Field{{Name: "title"}, {Name: "rank"}}}
	projection, err := compileRecordProjection(collection, Principal{Kind: identity.Administrator, ID: uuid.New()}, "title,id,updated_at")
	if err != nil {
		t.Fatal(err)
	}
	got := projection.apply(map[string]any{"id": "one", "title": "hello", "rank": 2, "updated_at": "now"})
	if len(got) != 3 || got["id"] != "one" || got["title"] != "hello" || got["updated_at"] != "now" {
		t.Fatalf("projection=%#v", got)
	}
}

func TestCompileRecordProjectionLeavesDefaultResponseUntouched(t *testing.T) {
	projection, err := compileRecordProjection(schema.Collection{}, Principal{Kind: identity.Administrator, ID: uuid.New()}, "")
	if err != nil {
		t.Fatal(err)
	}
	record := map[string]any{"id": "one", "title": "hello"}
	if got := projection.apply(record); len(got) != 2 || got["title"] != "hello" {
		t.Fatalf("default projection=%#v", got)
	}
}

func TestCompileRecordProjectionRejectsUnsafeFields(t *testing.T) {
	collection := schema.Collection{Fields: []schema.Field{{Name: "title"}, {Name: "secret", Hidden: true}}}
	user := Principal{Kind: identity.User, ID: uuid.New()}
	for _, fields := range []string{"*", "title.*", "title,,id", "unknown", "secret"} {
		t.Run(fields, func(t *testing.T) {
			_, err := compileRecordProjection(collection, user, fields)
			if err == nil {
				t.Fatal("expected validation error")
			}
		})
	}
}
