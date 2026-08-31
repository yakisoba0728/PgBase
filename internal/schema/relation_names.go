package schema

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"hash"
)

const (
	relationObjectNameDomain     = "pgbase:relation-object-name:v1"
	relationObjectNameHashLength = 32
)

// RelationPhysicalNames contains the deterministic bare PostgreSQL identifiers
// owned by one relation field. The names intentionally contain no collection or
// field text, so callers can safely render them with pgx.Identifier.
//
// A relation planner may use only the single-value or multi-value subset. The
// complete set lives here so planning and drift detection cannot derive
// different names for the same physical object.
type RelationPhysicalNames struct {
	SingleForeignKey         string
	SingleReverseIndex       string
	MultiTable               string
	MultiSourceForeignKey    string
	MultiTargetForeignKey    string
	MultiPrimaryKey          string
	MultiPositionUnique      string
	MultiPositionCheck       string
	PositionFunction         string
	PositionTrigger          string
	CardinalityFunction      string
	SourceCardinalityTrigger string
	EdgeCardinalityTrigger   string
	MultiReverseIndex        string
}

// NewRelationPhysicalNames returns stable PostgreSQL object names for a
// validated source collection and field name. It does not render or qualify the
// identifiers and performs no validation; schema planning owns validation.
func NewRelationPhysicalNames(sourceCollection, field string) RelationPhysicalNames {
	return RelationPhysicalNames{
		SingleForeignKey:         relationObjectName("rel_sfk_", "single-foreign-key", sourceCollection, field),
		SingleReverseIndex:       relationObjectName("rel_srev_", "single-reverse-index", sourceCollection, field),
		MultiTable:               relationObjectName("rel_tbl_", "multi-table", sourceCollection, field),
		MultiSourceForeignKey:    relationObjectName("rel_msfk_", "multi-source-foreign-key", sourceCollection, field),
		MultiTargetForeignKey:    relationObjectName("rel_mtfk_", "multi-target-foreign-key", sourceCollection, field),
		MultiPrimaryKey:          relationObjectName("rel_mpk_", "multi-primary-key", sourceCollection, field),
		MultiPositionUnique:      relationObjectName("rel_pos_uq_", "multi-position-unique", sourceCollection, field),
		MultiPositionCheck:       relationObjectName("rel_pos_ck_", "multi-position-check", sourceCollection, field),
		PositionFunction:         relationObjectName("rel_pos_fn_", "position-function", sourceCollection, field),
		PositionTrigger:          relationObjectName("rel_pos_trg_", "position-trigger", sourceCollection, field),
		CardinalityFunction:      relationObjectName("rel_card_fn_", "cardinality-function", sourceCollection, field),
		SourceCardinalityTrigger: relationObjectName("rel_card_src_trg_", "source-cardinality-trigger", sourceCollection, field),
		EdgeCardinalityTrigger:   relationObjectName("rel_card_edge_trg_", "edge-cardinality-trigger", sourceCollection, field),
		MultiReverseIndex:        relationObjectName("rel_mrev_", "multi-reverse-index", sourceCollection, field),
	}
}

func relationObjectName(prefix, role, sourceCollection, field string) string {
	hasher := sha256.New()
	writeRelationNamePart(hasher, relationObjectNameDomain)
	writeRelationNamePart(hasher, role)
	writeRelationNamePart(hasher, sourceCollection)
	writeRelationNamePart(hasher, field)
	digest := hex.EncodeToString(hasher.Sum(nil))
	return prefix + digest[:relationObjectNameHashLength]
}

// Length-prefixing keeps the hash input unambiguous even if this helper is
// exercised with unvalidated or future Unicode names.
func writeRelationNamePart(destination hash.Hash, value string) {
	var size [8]byte
	binary.BigEndian.PutUint64(size[:], uint64(len(value)))
	_, _ = destination.Write(size[:])
	_, _ = destination.Write([]byte(value))
}
