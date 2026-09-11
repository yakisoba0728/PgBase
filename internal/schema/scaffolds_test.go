package schema

import (
	"encoding/json"
	"testing"
)

const scaffoldSnapshot = `{"base":{"name":"","type":"base","validationMode":"postgres","userAccess":false,"rules":{"list":{"op":"owner"},"view":{"op":"owner"},"create":{"op":"owner"},"update":{"op":"owner"},"delete":{"op":"owner"}}},"auth":{"collection":{"name":"","type":"auth","validationMode":"postgres","fields":[{"name":"password","type":"password","required":false,"unique":false,"indexed":false,"hidden":true,"system":true},{"name":"token_key","type":"text","required":false,"unique":false,"indexed":false,"hidden":true,"system":true},{"name":"email","type":"text","required":false,"unique":true,"indexed":false,"system":true},{"name":"email_visibility","type":"boolean","required":false,"unique":false,"indexed":false,"system":true},{"name":"verified","type":"boolean","required":false,"unique":false,"indexed":false,"system":true}],"userAccess":false,"rules":{"list":{"op":"owner"},"view":{"op":"owner"},"create":{"op":"owner"},"update":{"op":"owner"},"delete":{"op":"owner"}}},"authCollection":{"name":"","registrationEnabled":false,"rules":{"list":null,"view":"@request.auth.id = id","create":"","update":"@request.auth.id = id","delete":"@request.auth.id = id","auth":"","manage":"@request.auth.id = id"},"policy":{"passwordEnabled":true,"minimumPasswordBytes":12,"maximumPasswordBytes":1024,"passwordPattern":"","identityFields":["email"],"mfaEnabled":false,"mfaDurationSeconds":1800,"mfaRule":"","otpEnabled":false,"otpDurationSeconds":300,"otpMaxAttempts":5}}},"view":{"name":"","type":"view","validationMode":"postgres","view":{"source":"","projection":[]},"userAccess":false,"rules":{"list":{"op":"owner"},"view":{"op":"owner"},"create":{"op":"owner"},"update":{"op":"owner"},"delete":{"op":"owner"}}}}`

func TestCollectionScaffoldsSnapshot(t *testing.T) {
	raw, err := json.Marshal((&Service{}).Scaffolds())
	if err != nil {
		t.Fatal(err)
	}
	if string(raw) != scaffoldSnapshot {
		t.Fatalf("scaffold snapshot changed:\n%s", raw)
	}
}

func TestCollectionScaffoldsCompileAfterReplacingPlaceholders(t *testing.T) {
	values := (&Service{}).Scaffolds()
	for _, item := range []SchemaDocumentCollection{values.Base, values.Auth.Collection} {
		item.Name = "scaffold_items"
		if _, err := CompilePlan(planTestCatalog(1), NewCreateChange(CreateCollectionInput{Name: item.Name, Type: item.Type, ValidationMode: item.ValidationMode, Fields: item.Fields, Indexes: item.Indexes})); err != nil {
			t.Fatalf("%s scaffold: %v", item.Type, err)
		}
	}
	source := updateTestCollection()
	view := values.View
	view.Name = "scaffold_view"
	view.View.Source = source.Name
	view.View.Projection = []string{"title"}
	if _, err := CompilePlan(planTestCatalog(1, source), NewCreateChange(CreateCollectionInput{Name: view.Name, Type: view.Type, ValidationMode: view.ValidationMode, View: view.View})); err != nil {
		t.Fatalf("view scaffold: %v", err)
	}
}

func TestAuthCollectionScaffoldHasAValidRealmCompanion(t *testing.T) {
	value := (&Service{}).Scaffolds().Auth
	value.AuthCollection.Name = "scaffold_auth"
	if value.Collection.Type != CollectionTypeAuth || !value.Collection.Fields[0].Hidden || value.AuthCollection.Policy.IdentityFields[0] != "email" {
		t.Fatalf("auth scaffold = %#v", value)
	}
	if err := validateDocumentAuthCollections([]SchemaDocumentAuthCollection{value.AuthCollection}); err != nil {
		t.Fatal(err)
	}
}
