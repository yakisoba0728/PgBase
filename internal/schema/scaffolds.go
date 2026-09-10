package schema

import "encoding/json"

// CollectionScaffolds contains complete, portable starting points for each
// collection kind. Callers replace the empty name (and the view source) before
// submitting the template through the normal preview/apply flow.
type CollectionScaffolds struct {
	Base SchemaDocumentCollection `json:"base"`
	Auth SchemaDocumentCollection `json:"auth"`
	View SchemaDocumentCollection `json:"view"`
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
			{Name: "tokenKey", Type: TypeText, Hidden: true, System: boolPointer(true)},
			{Name: "email", Type: TypeText, Unique: true, System: boolPointer(true)},
			{Name: "emailVisibility", Type: TypeBoolean, System: boolPointer(true)},
			{Name: "verified", Type: TypeBoolean, System: boolPointer(true)},
		},
		Indexes: []IndexInput{}, Rules: append(json.RawMessage(nil), rules...),
	}
	view := SchemaDocumentCollection{
		Type: CollectionTypeView, ValidationMode: ValidationModePostgres,
		Fields: []FieldInput{}, Indexes: []IndexInput{}, View: &ViewInput{Projection: []string{}},
		Rules: append(json.RawMessage(nil), rules...),
	}
	return CollectionScaffolds{Base: base, Auth: auth, View: view}
}

func boolPointer(value bool) *bool { return &value }
