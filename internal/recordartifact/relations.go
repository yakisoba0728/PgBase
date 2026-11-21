package recordartifact

import (
	"github.com/google/uuid"
	"github.com/yakisoba0728/PgBase/internal/schema"
)

// RelationSnapshot binds an event's public relation values, in their original
// order, to stable internal target identities. The envelope keys these values
// by immutable field ID, so a replacement field or reused public ID cannot
// change which historical target is checked during replay.
type RelationSnapshot struct {
	Cardinality        schema.RelationCardinality `json:"cardinality"`
	TargetCollectionID uuid.UUID                  `json:"targetCollectionId"`
	IDs                []uuid.UUID                `json:"ids"`
}
