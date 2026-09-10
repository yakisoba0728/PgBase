package recordops

import (
	"encoding/json"
	"testing"

	"github.com/google/uuid"
	"github.com/yakisoba0728/PgBase/internal/identity"
	"github.com/yakisoba0728/PgBase/internal/schema"
)

func TestHiddenInputIsAdministratorOnly(t *testing.T) {
	collection := schema.Collection{Fields: []schema.Field{{Name: "secret", Hidden: true}}}
	input := map[string]json.RawMessage{"secret": json.RawMessage(`"value"`)}
	if err := rejectHiddenInput(collection, Principal{Kind: identity.User, ID: uuid.New()}, input); err == nil {
		t.Fatal("user hidden input accepted")
	}
	if err := rejectHiddenInput(collection, Principal{Kind: identity.Administrator, ID: uuid.New()}, input); err != nil {
		t.Fatalf("administrator hidden input: %v", err)
	}
}
