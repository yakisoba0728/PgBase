package schema

import (
	"errors"
	"testing"

	"github.com/google/uuid"
)

func TestValidateCatalogIntegrityChecksRelationIncomingExactlyAndKeepsScalarCompatibility(t *testing.T) {
	usersID := uuid.MustParse("0199aa16-e3dd-7000-8000-000000000001")
	postsID := uuid.MustParse("0199aa16-e3dd-7000-8000-000000000002")
	titleID := uuid.MustParse("0199aa16-e3dd-7000-8000-000000000003")
	ownerID := uuid.MustParse("0199aa16-e3dd-7000-8000-000000000004")
	spec := RelationSpec{
		TargetCollectionID: usersID,
		Cardinality:        RelationSingle,
		MinSelect:          0,
		MaxSelect:          1,
		OnDelete:           RelationRestrict,
		ValueCodecVersion:  RelationValueCodecVersion,
		Storage:            RelationStorage{Kind: RelationStorageInlineFKV1, Name: "owner"},
	}
	valid := Catalog{
		Version: 2,
		Collections: map[string]Collection{
			"users": {ID: usersID, Name: "users", TableName: "users"},
			"posts": {
				ID: postsID, Name: "posts", TableName: "posts",
				Fields: []Field{
					{ID: titleID, Name: "title", ColumnName: "title", Type: TypeText, Ordinal: 0},
					{ID: ownerID, Name: "owner", ColumnName: "owner", Type: TypeRelation, Indexed: true, Relation: &spec, Ordinal: 1},
				},
			},
		},
		Incoming: map[uuid.UUID][]RelationRef{
			usersID: {{SourceCollectionID: postsID, SourceFieldID: ownerID, Spec: spec}},
		},
	}
	if err := validateCatalogIntegrity(valid); err != nil {
		t.Fatalf("valid relation catalog: %v", err)
	}
	scalar := Catalog{Collections: map[string]Collection{
		"pages": {
			ID: uuid.MustParse("0199aa16-e3dd-7000-8000-000000000005"), Name: "pages", TableName: "pages",
			Fields: []Field{{ID: uuid.MustParse("0199aa16-e3dd-7000-8000-000000000006"), Name: "body", ColumnName: "body", Type: TypeText}},
		},
	}}
	if err := validateCatalogIntegrity(scalar); err != nil {
		t.Fatalf("scalar catalog compatibility: %v", err)
	}

	tests := []struct {
		name   string
		mutate func(*Catalog)
	}{
		{
			name: "missing incoming",
			mutate: func(catalog *Catalog) {
				delete(catalog.Incoming, usersID)
			},
		},
		{
			name: "unexpected empty incoming",
			mutate: func(catalog *Catalog) {
				catalog.Incoming[postsID] = []RelationRef{}
			},
		},
		{
			name: "wrong incoming spec",
			mutate: func(catalog *Catalog) {
				catalog.Incoming[usersID][0].Spec.OnDelete = RelationCascade
			},
		},
		{
			name: "duplicate incoming",
			mutate: func(catalog *Catalog) {
				catalog.Incoming[usersID] = append(catalog.Incoming[usersID], catalog.Incoming[usersID][0])
			},
		},
		{
			name: "missing target",
			mutate: func(catalog *Catalog) {
				delete(catalog.Collections, "users")
			},
		},
		{
			name: "duplicate collection ID",
			mutate: func(catalog *Catalog) {
				posts := catalog.Collections["posts"]
				posts.ID = usersID
				catalog.Collections["posts"] = posts
			},
		},
		{
			name: "duplicate field ID",
			mutate: func(catalog *Catalog) {
				posts := catalog.Collections["posts"]
				posts.Fields[1].ID = titleID
				catalog.Collections["posts"] = posts
			},
		},
		{
			name: "scalar carries relation",
			mutate: func(catalog *Catalog) {
				posts := catalog.Collections["posts"]
				posts.Fields[0].Relation = &spec
				catalog.Collections["posts"] = posts
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			catalog := cloneCatalog(valid)
			test.mutate(&catalog)
			if err := validateCatalogIntegrity(catalog); !errors.Is(err, ErrConflict) {
				t.Fatalf("validateCatalogIntegrity() error = %v, want ErrConflict", err)
			}
		})
	}
}
