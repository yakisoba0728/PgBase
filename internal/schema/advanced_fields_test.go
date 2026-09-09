package schema

import (
	"errors"
	"testing"
)

func TestAdvancedFieldOptionsAndShapes(t *testing.T) {
	fiveMiB := int64(5 << 20)
	short := int64(MaximumTextBytes)
	tooLarge := int64(MaximumRecordBytes + 1)
	minimum := 8
	negative := -1
	maximum := 7
	valid := []FieldInput{
		{Name: "body", Type: TypeEditor, Options: &FieldOptions{MaxSize: &fiveMiB, ConvertURLs: true}},
		{Name: "slug", Type: TypeEditor, Indexed: true, Options: &FieldOptions{MaxSize: &short}},
		{Name: "point", Type: TypeGeoPoint, Indexed: true},
		{Name: "created", Type: TypeAutoDate, Indexed: true, Options: &FieldOptions{OnCreate: true}},
		{Name: "password", Type: TypePassword, Required: true, Options: &FieldOptions{MinLength: &minimum}},
	}
	if _, err := CompilePlan(Catalog{Collections: map[string]Collection{}}, NewCreateChange(CreateCollectionInput{Name: "advanced", Fields: valid})); err != nil {
		t.Fatal(err)
	}
	invalid := []FieldInput{
		{Name: "body", Type: TypeEditor, Indexed: true, Options: &FieldOptions{MaxSize: &fiveMiB}},
		{Name: "body", Type: TypeEditor, Options: &FieldOptions{MaxSize: &tooLarge}},
		{Name: "point", Type: TypeGeoPoint, Unique: true},
		{Name: "created", Type: TypeAutoDate},
		{Name: "password", Type: TypePassword, Presentable: true, Options: &FieldOptions{MinLength: &minimum}},
		{Name: "password", Type: TypePassword, Options: &FieldOptions{MinLength: &negative}},
		{Name: "password", Type: TypePassword, Options: &FieldOptions{MinLength: &minimum, MaxLength: &maximum}},
		{Name: "password", Type: TypePassword, Options: &FieldOptions{Pattern: "["}},
	}
	for _, field := range invalid {
		_, err := CompilePlan(Catalog{Collections: map[string]Collection{}}, NewCreateChange(CreateCollectionInput{Name: "invalid", Fields: []FieldInput{field}}))
		var detail *ValidationError
		if !errors.As(err, &detail) {
			t.Fatalf("field=%+v error=%v", field, err)
		}
	}
}
