package recordops

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/yakisoba0728/PgBase/internal/query"
	"github.com/yakisoba0728/PgBase/internal/recordartifact"
	"github.com/yakisoba0728/PgBase/internal/schema"
	"io"
	"sort"
)

func readRecord(ctx context.Context, tx pgx.Tx, collection schema.Collection, id uuid.UUID, lock bool) (map[string]any, error) {
	statement := query.CompileSelectByID(collection, id)
	if lock {
		statement.SQL += " FOR UPDATE"
	}
	record, err := query.EncodeRecord(collection, tx.QueryRow(ctx, statement.SQL, statement.Args...))
	return record, query.NormalizeRecordError(err)
}

func decodeStoredRecord(body []byte) (map[string]any, error) {
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.UseNumber()
	var data map[string]any
	if err := decoder.Decode(&data); err != nil {
		return nil, err
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		if err == nil {
			return nil, errors.New("multiple stored JSON values")
		}
		return nil, err
	}
	return data, nil
}

func canonicalInput(input map[string]json.RawMessage) ([]byte, error) {
	canonical := make(map[string]json.RawMessage, len(input))
	for name, raw := range input {
		encoded, err := recordartifact.CanonicalJSON(raw)
		if err != nil {
			return nil, &query.ValidationError{Details: []query.ValidationDetail{{Field: name, Code: "type_mismatch"}}}
		}
		canonical[name] = encoded
	}
	encoded, err := json.Marshal(canonical)
	if err != nil {
		return nil, fmt.Errorf("encode canonical record input: %w", err)
	}
	return encoded, nil
}

func sortedInputFields(input map[string]json.RawMessage) []string {
	fields := make([]string, 0, len(input))
	for name := range input {
		fields = append(fields, name)
	}
	sort.Strings(fields)
	return fields
}

func committedUserFields(collection schema.Collection) []string {
	fields := make([]schema.Field, len(collection.Fields))
	copy(fields, collection.Fields)
	sort.Slice(fields, func(left, right int) bool {
		if fields[left].Ordinal == fields[right].Ordinal {
			return fields[left].Name < fields[right].Name
		}
		return fields[left].Ordinal < fields[right].Ordinal
	})
	names := make([]string, 0, len(fields))
	for _, field := range fields {
		names = append(names, field.Name)
	}
	// Audit consumers expect changed field names to be lexically stable rather
	// than tied to physical field ordinal.
	sort.Strings(names)
	return names
}

func mutationChanges(record map[string]any, fields []string) map[string]any {
	if len(fields) == 0 {
		return nil
	}
	changes := make(map[string]any, len(fields))
	for _, field := range fields {
		changes[field] = record[field]
	}
	return changes
}
