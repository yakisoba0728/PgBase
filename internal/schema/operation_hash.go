package schema

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"hash"
	"sort"

	"github.com/yakisoba0728/PgBase/internal/jsonvalue"
)

const (
	schemaResourceKeyHashDomain = "pgbase-schema-resource-key-v1"
	schemaIdempotencyHashDomain = "pgbase-schema-idempotency-key-v1"
	schemaRequestHashDomain     = "pgbase-schema-request-v1"
)

var errInvalidSchemaOperationIdentity = errors.New("invalid schema operation identity")

func schemaResourceKeyHash(change Change) ([sha256.Size]byte, error) {
	name, ok := schemaOperationResourceName(change)
	if !ok || name == "" {
		return [sha256.Size]byte{}, errInvalidSchemaOperationIdentity
	}
	return schemaOperationDigest(schemaResourceKeyHashDomain, []byte("collection"), []byte(name)), nil
}

func schemaIdempotencyKeyHash(key string) [sha256.Size]byte {
	return schemaOperationDigest(schemaIdempotencyHashDomain, []byte(key))
}

func schemaRequestHash(change Change) ([sha256.Size]byte, error) {
	operation, payload, err := canonicalSchemaOperationRequest(change)
	if err != nil {
		return [sha256.Size]byte{}, err
	}
	return schemaOperationDigest(schemaRequestHashDomain, []byte(operation), payload), nil
}

func canonicalSchemaOperationRequest(change Change) (string, []byte, error) {
	operation, ok := schemaOperation(change)
	if !ok {
		return "", nil, errInvalidSchemaOperationIdentity
	}
	var request any
	switch change.kind {
	case changeCreate:
		input := change.create
		fields, err := canonicalSchemaOperationFields(input.Fields)
		if err != nil {
			return "", nil, err
		}
		input.Fields = fields
		request = input
	case changeUpdate:
		input := change.update
		fields, err := canonicalSchemaOperationFields(input.Fields)
		if err != nil {
			return "", nil, err
		}
		input.Fields = fields
		request = input
	case changeDelete:
		request = struct {
			Name string `json:"name"`
		}{Name: change.deleteName}
	}
	encoded, err := json.Marshal(request)
	if err != nil {
		return "", nil, fmt.Errorf("%w: encode request", errInvalidSchemaOperationIdentity)
	}
	canonical, err := jsonvalue.Canonical(encoded)
	if err != nil {
		return "", nil, fmt.Errorf("%w: canonicalize request", errInvalidSchemaOperationIdentity)
	}
	return string(operation), canonical, nil
}

func canonicalSchemaOperationFields(fields []FieldInput) ([]FieldInput, error) {
	canonical := cloneFieldInputs(fields)
	for index := range canonical {
		if len(canonical[index].Default) == 0 {
			continue
		}
		value, err := jsonvalue.Canonical(canonical[index].Default)
		if err != nil {
			return nil, fmt.Errorf("%w: canonicalize field default", errInvalidSchemaOperationIdentity)
		}
		canonical[index].Default = json.RawMessage(value)
	}
	return canonical, nil
}

func schemaOperationResourceName(change Change) (string, bool) {
	switch change.kind {
	case changeCreate:
		return change.create.Name, true
	case changeUpdate:
		return change.update.Name, true
	case changeDelete:
		return change.deleteName, true
	default:
		return "", false
	}
}

func schemaOperationDigest(domain string, parts ...[]byte) [sha256.Size]byte {
	digest := sha256.New()
	writeSchemaOperationDigestPart(digest, []byte(domain))
	for _, part := range parts {
		writeSchemaOperationDigestPart(digest, part)
	}
	var result [sha256.Size]byte
	copy(result[:], digest.Sum(nil))
	return result
}

func writeSchemaOperationDigestPart(destination hash.Hash, part []byte) {
	var length [8]byte
	binary.BigEndian.PutUint64(length[:], uint64(len(part)))
	_, _ = destination.Write(length[:])
	_, _ = destination.Write(part)
}

func planChecksumWithCompilerVersion(plan Plan, compilerVersion string) (string, error) {
	if compilerVersion == "" {
		return "", errors.New("schema plan compiler version is required")
	}
	definition := plan.Definition
	definition.Fields = append([]PlannedField(nil), definition.Fields...)
	sort.Slice(definition.Fields, func(i, j int) bool {
		if definition.Fields[i].Ordinal != definition.Fields[j].Ordinal {
			return definition.Fields[i].Ordinal < definition.Fields[j].Ordinal
		}
		return definition.Fields[i].Name < definition.Fields[j].Name
	})
	canonical := struct {
		CompilerVersion string
		Operation       string
		BaseVersion     int64
		Definition      PlannedCollection
		Statements      []string
	}{
		CompilerVersion: compilerVersion,
		Operation:       plan.Operation,
		BaseVersion:     plan.BaseVersion,
		Definition:      definition,
		Statements:      plan.Statements,
	}
	encoded, err := json.Marshal(canonical)
	if err != nil {
		return "", fmt.Errorf("marshal schema plan checksum: %w", err)
	}
	digest := sha256.Sum256(encoded)
	return hex.EncodeToString(digest[:]), nil
}
