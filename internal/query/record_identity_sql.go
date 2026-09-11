package query

import (
	"github.com/google/uuid"
	"github.com/yakisoba0728/PgBase/internal/schema"
)

const (
	internalRowIDSort = "@rowid"
	externalIDSort    = "@external_id"
)

func textIdentityField(collection schema.Collection, field schema.Field) (schema.Field, uuid.UUID) {
	if field.Name != systemFieldID || collection.RecordID.Kind != schema.RecordIDText {
		return field, uuid.Nil
	}
	field.Type = schema.TypeText
	return field, collection.AccessCollectionID()
}

func externalIdentityCursorField(collection schema.Collection) schema.Field {
	return schema.Field{
		ID:   uuid.NewSHA1(collection.AccessCollectionID(), []byte("external-record-id")),
		Name: "external_record_id", ColumnName: systemFieldID, Type: schema.TypeText, Indexed: true,
	}
}

func recordIdentityExpression(collectionID uuid.UUID, recordIDExpression string) string {
	return `(SELECT "rq_identity"."external_id" FROM "pgbase_meta"."record_identities" AS "rq_identity" WHERE "rq_identity"."collection_kind"='base' AND "rq_identity"."collection_id"='` + collectionID.String() + `'::uuid AND "rq_identity"."internal_id"=` + recordIDExpression + `)`
}

func pocketBaseSortKeys(collection schema.Collection, requested []SortKey) []SortKey {
	if collection.RecordID.Kind != schema.RecordIDText {
		return requested
	}
	keys := append([]SortKey(nil), requested...)
	for i := range keys {
		if keys[i].Field == systemFieldID {
			keys[i].Field = externalIDSort
		}
	}
	return keys
}
