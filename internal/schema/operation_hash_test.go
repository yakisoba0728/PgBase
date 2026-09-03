package schema

import (
	"encoding/hex"
	"encoding/json"
	"testing"
)

func TestSchemaOperationResourceAndIdempotencyHashesAreDomainSeparated(t *testing.T) {
	t.Parallel()

	create := NewCreateChange(CreateCollectionInput{Name: "articles"})
	update := NewUpdateChange(UpdateCollectionInput{Name: "articles"})
	deleted := NewDeleteChange("articles")

	createResource, err := schemaResourceKeyHash(create)
	if err != nil {
		t.Fatalf("hash create resource key: %v", err)
	}
	updateResource, err := schemaResourceKeyHash(update)
	if err != nil {
		t.Fatalf("hash update resource key: %v", err)
	}
	deleteResource, err := schemaResourceKeyHash(deleted)
	if err != nil {
		t.Fatalf("hash delete resource key: %v", err)
	}
	if createResource != updateResource || createResource != deleteResource {
		t.Fatalf("same collection resource hashes differ: create=%x update=%x delete=%x",
			createResource, updateResource, deleteResource)
	}
	if got, want := hex.EncodeToString(createResource[:]), "5efba6358cd50e5e3d58b0d4f19adac6cd55b8b8ec9a15fbc9a93c13a83b3cbd"; got != want {
		t.Fatalf("resource key hash = %s, want %s", got, want)
	}

	idempotency := schemaIdempotencyKeyHash("guarded-key-01")
	if got, want := hex.EncodeToString(idempotency[:]), "c1de2c4ec53875a8a089536a10be201634e69c674d00812e173ba30763a4ae01"; got != want {
		t.Fatalf("idempotency key hash = %s, want %s", got, want)
	}
	if idempotency == schemaIdempotencyKeyHash("guarded-key-02") {
		t.Fatal("different idempotency keys produced the same identity")
	}
	if idempotency == createResource {
		t.Fatal("resource and idempotency domains produced the same identity")
	}
}

func TestSchemaOperationDigestFramesEveryComponent(t *testing.T) {
	t.Parallel()

	left := schemaOperationDigest("domain", []byte("ab"), []byte("c"))
	right := schemaOperationDigest("domain", []byte("a"), []byte("bc"))
	if left == right {
		t.Fatal("length-ambiguous components produced the same digest")
	}
	if left == schemaOperationDigest("domainab", []byte("c")) {
		t.Fatal("domain and first component were not framed independently")
	}
}

func TestSchemaRequestHashCanonicalizesTheFullSealedChange(t *testing.T) {
	t.Parallel()

	first := NewCreateChange(CreateCollectionInput{
		Name: "articles",
		Fields: []FieldInput{{
			Name: "payload", Type: TypeJSONB,
			Default: json.RawMessage(` { "b" : 2, "a" : 1 } `),
		}},
	})
	second := NewCreateChange(CreateCollectionInput{
		Name: "articles",
		Fields: []FieldInput{{
			Name: "payload", Type: TypeJSONB,
			Default: json.RawMessage(`{"a":1,"b":2}`),
		}},
	})

	firstHash, err := schemaRequestHash(first)
	if err != nil {
		t.Fatalf("hash first request: %v", err)
	}
	secondHash, err := schemaRequestHash(second)
	if err != nil {
		t.Fatalf("hash canonical request: %v", err)
	}
	if firstHash != secondHash {
		t.Fatalf("equivalent JSON defaults changed request identity: first=%x second=%x", firstHash, secondHash)
	}
	if got, want := hex.EncodeToString(firstHash[:]), "8eb8e08426125ea603293e09fab72e8a2f0207ebaf6d4d04cbd4f0468a59c5b5"; got != want {
		t.Fatalf("request hash = %s, want %s", got, want)
	}

	update := NewUpdateChange(UpdateCollectionInput{Name: "articles", Fields: second.create.Fields})
	updateHash, err := schemaRequestHash(update)
	if err != nil {
		t.Fatalf("hash update request: %v", err)
	}
	if updateHash == secondHash {
		t.Fatal("create and update request domains shared an identity")
	}

	related := NewCreateChange(CreateCollectionInput{
		Name: "articles",
		Fields: []FieldInput{{
			Name: "owner", Type: TypeRelation,
			Relation: &RelationInput{TargetCollection: "admins", OnDelete: RelationRestrict},
		}},
	})
	changedRelation := NewCreateChange(CreateCollectionInput{
		Name: "articles",
		Fields: []FieldInput{{
			Name: "owner", Type: TypeRelation,
			Relation: &RelationInput{TargetCollection: "admins", OnDelete: RelationCascade},
		}},
	})
	relatedHash, err := schemaRequestHash(related)
	if err != nil {
		t.Fatalf("hash relation request: %v", err)
	}
	changedRelationHash, err := schemaRequestHash(changedRelation)
	if err != nil {
		t.Fatalf("hash changed relation request: %v", err)
	}
	if relatedHash == changedRelationHash {
		t.Fatal("relation configuration was omitted from the request identity")
	}
}

func TestSchemaRequestHashRejectsLossyOrInvalidChanges(t *testing.T) {
	t.Parallel()

	invalidDefault := NewCreateChange(CreateCollectionInput{
		Name:   "articles",
		Fields: []FieldInput{{Name: "payload", Type: TypeJSONB, Default: json.RawMessage(`{"broken":`)}},
	})
	if _, err := schemaRequestHash(invalidDefault); err == nil {
		t.Fatal("invalid embedded JSON was hashed")
	}
	if _, err := schemaRequestHash(Change{}); err == nil {
		t.Fatal("unsealed change was hashed")
	}
	if _, err := schemaResourceKeyHash(Change{}); err == nil {
		t.Fatal("unsealed change resource was hashed")
	}

	deleted, err := schemaRequestHash(NewDeleteChange("articles"))
	if err != nil {
		t.Fatalf("hash delete request: %v", err)
	}
	if got, want := hex.EncodeToString(deleted[:]), "3e2a1acb4fc02a42fb026b6064e3b0df01583c6d00465bd12fdad1118cc21ca0"; got != want {
		t.Fatalf("delete request hash = %s, want %s", got, want)
	}
}
