package schema

import (
	"encoding/json"
	"errors"
	"testing"
)

func TestValidateDocumentAuthCollectionsRejectsInvalidIdentityFields(t *testing.T) {
	valid := schemaDocumentAuthCollectionForTest()
	cases := []struct {
		name       string
		identities []string
		fields     []FieldInput
	}{
		{name: "empty", identities: []string{}},
		{name: "duplicate", identities: []string{"email", "email"}},
		{name: "invalid name", identities: []string{"Invalid"}},
		{name: "missing custom field", identities: []string{"handle"}},
		{name: "hidden custom field", identities: []string{"handle"}, fields: []FieldInput{{Name: "handle", Type: TypeText, Hidden: true}}},
		{name: "non-text custom field", identities: []string{"handle"}, fields: []FieldInput{{Name: "handle", Type: TypeInteger}}},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			value := valid
			value.Policy.IdentityFields = test.identities
			value.Fields = test.fields
			err := validateDocumentAuthCollections([]SchemaDocumentAuthCollection{value})
			var validationErr *ValidationError
			if !errors.As(err, &validationErr) || len(validationErr.Details) != 1 || validationErr.Details[0].Field != "authCollections.users.policy.identityFields" {
				t.Fatalf("validation error=%#v", err)
			}
		})
	}
}

func TestValidateDocumentAuthCollectionsAcceptsVisibleTextIdentity(t *testing.T) {
	value := schemaDocumentAuthCollectionForTest()
	value.Policy.IdentityFields = []string{"email", "handle"}
	value.Fields = []FieldInput{{Name: "handle", Type: TypeText}}
	if err := validateDocumentAuthCollections([]SchemaDocumentAuthCollection{value}); err != nil {
		t.Fatal(err)
	}
}

func schemaDocumentAuthCollectionForTest() SchemaDocumentAuthCollection {
	return SchemaDocumentAuthCollection{
		Name:  "users",
		Rules: json.RawMessage(`{}`),
		Policy: SchemaDocumentAuthPolicy{
			MinimumPasswordBytes: 12,
			MaximumPasswordBytes: 128,
			IdentityFields:       []string{"email"},
			MFADurationSeconds:   300,
			OTPDurationSeconds:   300,
			OTPMaxAttempts:       5,
		},
	}
}
