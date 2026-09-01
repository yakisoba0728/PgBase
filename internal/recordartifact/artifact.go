// Package recordartifact owns the durable revision and validator material used
// by both record reads and mutations.
package recordartifact

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sort"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/webisnoteasy/PgBase/internal/schema"
)

const codecVersion = 1

type Record struct {
	Data    map[string]any `json:"data"`
	ETag    string         `json:"-"`
	Version int64          `json:"-"`
}

func ReadRevision(ctx context.Context, tx pgx.Tx, collectionID, recordID uuid.UUID) (int64, []byte, bool, error) {
	var version int64
	var validator []byte
	err := tx.QueryRow(ctx, `SELECT version, validator FROM pgbase_meta.record_revisions
		WHERE collection_id = $1 AND record_id = $2`, collectionID, recordID).Scan(&version, &validator)
	if errors.Is(err, pgx.ErrNoRows) {
		return 0, nil, false, nil
	}
	if err != nil {
		return 0, nil, false, err
	}
	if version < 1 || len(validator) != 32 {
		return 0, nil, false, errors.New("invalid record revision state")
	}
	return version, validator, true, nil
}

func WriteRevision(
	ctx context.Context,
	tx pgx.Tx,
	collectionID, recordID uuid.UUID,
	previousVersion, newVersion int64,
	validator []byte,
	eventID uuid.UUID,
) error {
	tag, err := tx.Exec(ctx, `INSERT INTO pgbase_meta.record_revisions
		(collection_id, record_id, version, validator, last_event_id, updated_at)
		VALUES ($1, $2, $3, $4, $5, transaction_timestamp())
		ON CONFLICT (collection_id, record_id) DO UPDATE SET
			version = EXCLUDED.version,
			validator = EXCLUDED.validator,
			last_event_id = EXCLUDED.last_event_id,
			updated_at = EXCLUDED.updated_at
		WHERE pgbase_meta.record_revisions.version = $6`,
		collectionID, recordID, newVersion, validator, eventID, previousVersion)
	if err != nil {
		return err
	}
	if tag.RowsAffected() != 1 {
		return errors.New("record revision changed outside the transaction kernel")
	}
	return nil
}

func SchemaDigest(collection schema.Collection) ([32]byte, error) {
	type digestRelation struct {
		TargetCollectionID uuid.UUID                   `json:"targetCollectionId"`
		Cardinality        schema.RelationCardinality  `json:"cardinality"`
		MinSelect          int                         `json:"minSelect"`
		MaxSelect          int                         `json:"maxSelect"`
		OnDelete           schema.RelationDeleteAction `json:"onDelete"`
		ValueCodecVersion  int                         `json:"valueCodecVersion"`
	}
	type digestField struct {
		ID       uuid.UUID        `json:"id"`
		Name     string           `json:"name"`
		Type     schema.FieldType `json:"type"`
		Ordinal  int              `json:"ordinal"`
		Required bool             `json:"required"`
		Unique   bool             `json:"unique"`
		Indexed  bool             `json:"indexed"`
		Default  json.RawMessage  `json:"default,omitempty"`
		Relation *digestRelation  `json:"relation,omitempty"`
	}
	fields := append([]schema.Field(nil), collection.Fields...)
	sort.Slice(fields, func(left, right int) bool {
		if fields[left].Ordinal == fields[right].Ordinal {
			return fields[left].ID.String() < fields[right].ID.String()
		}
		return fields[left].Ordinal < fields[right].Ordinal
	})
	encodedFields := make([]digestField, 0, len(fields))
	for _, field := range fields {
		var relation *digestRelation
		if field.Type == schema.TypeRelation {
			if field.Relation == nil {
				return [32]byte{}, fmt.Errorf("encode schema digest: invalid relation")
			}
			relation = &digestRelation{
				TargetCollectionID: field.Relation.TargetCollectionID,
				Cardinality:        field.Relation.Cardinality,
				MinSelect:          field.Relation.MinSelect,
				MaxSelect:          field.Relation.MaxSelect,
				OnDelete:           field.Relation.OnDelete,
				ValueCodecVersion:  field.Relation.ValueCodecVersion,
			}
		} else if field.Relation != nil {
			return [32]byte{}, fmt.Errorf("encode schema digest: invalid relation")
		}
		defaultValue := append(json.RawMessage(nil), field.Default...)
		if len(defaultValue) > 0 {
			canonical, err := CanonicalJSON(defaultValue)
			if err != nil {
				return [32]byte{}, fmt.Errorf("encode schema digest: invalid default")
			}
			defaultValue = canonical
		}
		encodedFields = append(encodedFields, digestField{
			ID: field.ID, Name: field.Name, Type: field.Type, Ordinal: field.Ordinal,
			Required: field.Required, Unique: field.Unique, Indexed: field.Indexed,
			Default: defaultValue, Relation: relation,
		})
	}
	payload, err := json.Marshal(struct {
		CodecVersion int           `json:"codecVersion"`
		Fields       []digestField `json:"fields"`
	}{CodecVersion: codecVersion, Fields: encodedFields})
	if err != nil {
		return [32]byte{}, fmt.Errorf("encode schema digest: %w", err)
	}
	return Digest("pgbase-schema-v1", payload), nil
}

func ETag(
	collectionID, recordID uuid.UUID,
	schemaDigest [32]byte,
	record map[string]any,
	validator []byte,
	revisionPresent bool,
) (string, error) {
	if revisionPresent {
		return RevisionETag(collectionID, recordID, schemaDigest, validator), nil
	}
	body, err := CanonicalRecord(record)
	if err != nil {
		return "", err
	}
	digest := Digest("pgbase-record-etag-legacy-v1", collectionID[:], recordID[:], schemaDigest[:], body)
	return `"pgb0.` + base64.RawURLEncoding.EncodeToString(digest[:]) + `"`, nil
}

func RevisionETag(collectionID, recordID uuid.UUID, schemaDigest [32]byte, validator []byte) string {
	digest := Digest("pgbase-record-etag-v1", collectionID[:], recordID[:], validator, schemaDigest[:])
	return `"pgb1.` + base64.RawURLEncoding.EncodeToString(digest[:]) + `"`
}

func CanonicalRecord(record map[string]any) ([]byte, error) {
	encoded, err := json.Marshal(record)
	if err != nil {
		return nil, fmt.Errorf("encode canonical record: %w", err)
	}
	return encoded, nil
}

func CanonicalJSON(raw []byte) ([]byte, error) {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	var value any
	if err := decoder.Decode(&value); err != nil {
		return nil, err
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		if err == nil {
			return nil, errors.New("multiple JSON values")
		}
		return nil, err
	}
	return json.Marshal(value)
}

func Digest(domain string, parts ...[]byte) [32]byte {
	hash := sha256.New()
	writeDigestPart(hash, []byte(domain))
	for _, part := range parts {
		writeDigestPart(hash, part)
	}
	var digest [32]byte
	copy(digest[:], hash.Sum(nil))
	return digest
}

func writeDigestPart(destination io.Writer, part []byte) {
	var size [8]byte
	binary.BigEndian.PutUint64(size[:], uint64(len(part)))
	_, _ = destination.Write(size[:])
	_, _ = destination.Write(part)
}
