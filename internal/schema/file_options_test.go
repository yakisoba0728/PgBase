package schema

import (
	"testing"

	"github.com/google/uuid"
)

func TestFileFieldOptions(t *testing.T) {
	maximum, count := int64(1024), 3
	valid := FieldOptions{MaxFileSize: &maximum, MaxSelect: &count, MimeTypes: []string{"image/*", "application/pdf"}, Thumbs: []string{"100x100", "100x50t", "100x50b", "100x50f", "0x80", "80x0"}}
	if err := validateFieldOptions(TypeFile, &valid); err != nil {
		t.Fatalf("valid file options: %v", err)
	}
	for name, options := range map[string]FieldOptions{
		"zero bytes":     {MaxFileSize: func() *int64 { v := int64(0); return &v }()},
		"zero files":     {MaxSelect: func() *int { v := 0; return &v }()},
		"bad thumb":      {Thumbs: []string{"0x0"}},
		"duplicate mime": {MimeTypes: []string{"image/png", "image/png"}},
	} {
		t.Run(name, func(t *testing.T) {
			if validateFieldOptions(TypeFile, &options) == nil {
				t.Fatal("accepted invalid options")
			}
		})
	}
	if validateFieldOptions(TypeText, &FieldOptions{Thumbs: []string{"20x20"}}) == nil {
		t.Fatal("accepted file options on text")
	}
}

func TestRequiredFileFieldPlans(t *testing.T) {
	maximum, count := int64(128), 2
	_, err := CompilePlan(Catalog{Collections: map[string]Collection{}}, NewCreateChange(CreateCollectionInput{Name: "docs", Fields: []FieldInput{{Name: "attachment", Type: TypeFile, Required: true, Options: &FieldOptions{MaxFileSize: &maximum, MaxSelect: &count, MimeTypes: []string{"text/plain"}}}}}))
	if err != nil {
		t.Fatalf("required file plan: %v", err)
	}
}

func TestCatalogIntegrityAcceptsRequiredMultiFileOptions(t *testing.T) {
	maximum, count := int64(1024), 3
	collectionID, fieldID := uuid.New(), uuid.New()
	catalog := Catalog{Collections: map[string]Collection{"docs": {
		ID: collectionID, Name: "docs", TableName: "docs",
		Fields: []Field{{ID: fieldID, Name: "attachment", ColumnName: "attachment", Type: TypeFile, Required: true, Ordinal: 0,
			Options: FieldOptions{MaxFileSize: &maximum, MaxSelect: &count, MimeTypes: []string{"text/plain"}}}},
	}}}
	if err := validateCatalogIntegrity(catalog); err != nil {
		t.Fatalf("valid file catalog rejected: %v", err)
	}
}

func TestFileMinSelectIsNotASeparateContract(t *testing.T) {
	minimum := 1
	if err := validateFieldOptions(TypeFile, &FieldOptions{MinSelect: &minimum}); err == nil {
		t.Fatal("file minSelect accepted; required is the file minimum contract")
	}
}
