package schema

import "encoding/json"

// CollectionScaffolds contains complete, portable starting points for each
// collection kind. Callers replace the empty name (and the view source) before
// submitting the template through the normal preview/apply flow.
type CollectionScaffolds struct {
	Base SchemaDocumentCollection `json:"base"`
	Auth AuthCollectionScaffold   `json:"auth"`
	View SchemaDocumentCollection `json:"view"`
}

// AuthCollectionScaffold keeps the catalog collection and the authentication
// realm together. Both names are placeholders that callers replace with the
// same value before sending one schema import preview/apply request.
type AuthCollectionScaffold struct {
	Collection     SchemaDocumentCollection     `json:"collection"`
	AuthCollection SchemaDocumentAuthCollection `json:"authCollection"`
}

func (s *Service) Scaffolds() CollectionScaffolds {
	rules := json.RawMessage(`{"list":{"op":"owner"},"view":{"op":"owner"},"create":{"op":"owner"},"update":{"op":"owner"},"delete":{"op":"owner"}}`)
	base := SchemaDocumentCollection{
		Type: CollectionTypeBase, ValidationMode: ValidationModePostgres,
		Fields: []FieldInput{}, Indexes: []IndexInput{}, Rules: append(json.RawMessage(nil), rules...),
	}
	auth := SchemaDocumentCollection{
		Type: CollectionTypeAuth, ValidationMode: ValidationModePostgres,
		Fields: []FieldInput{
			{Name: "password", Type: TypePassword, Hidden: true, System: boolPointer(true)},
			{Name: "token_key", Type: TypeText, Hidden: true, System: boolPointer(true)},
			{Name: "email", Type: TypeText, Unique: true, System: boolPointer(true)},
			{Name: "email_visibility", Type: TypeBoolean, System: boolPointer(true)},
			{Name: "verified", Type: TypeBoolean, System: boolPointer(true)},
		},
		Indexes: []IndexInput{}, Rules: append(json.RawMessage(nil), rules...),
	}
	authRealm := SchemaDocumentAuthCollection{
		Fields: []FieldInput{},
		Rules:  json.RawMessage(`{"list":null,"view":"@request.auth.id = id","create":"","update":"@request.auth.id = id","delete":"@request.auth.id = id","auth":"","manage":"@request.auth.id = id"}`),
		Policy: SchemaDocumentAuthPolicy{
			PasswordEnabled: true, MinimumPasswordBytes: 12, MaximumPasswordBytes: 1024,
			IdentityFields: []string{"email"}, MFADurationSeconds: 1800,
			OTPDurationSeconds: 300, OTPMaxAttempts: 5,
		},
		TokenTTLs: map[string]int{},
	}
	view := SchemaDocumentCollection{
		Type: CollectionTypeView, ValidationMode: ValidationModePostgres,
		Fields: []FieldInput{}, Indexes: []IndexInput{}, View: &ViewInput{Projection: []string{}},
		Rules: append(json.RawMessage(nil), rules...),
	}
	return CollectionScaffolds{Base: base, Auth: AuthCollectionScaffold{Collection: auth, AuthCollection: authRealm}, View: view}
}

func boolPointer(value bool) *bool { return &value }
