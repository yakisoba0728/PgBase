package recordops

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"time"
	"unicode/utf8"

	"github.com/jackc/pgx/v5"
	"github.com/yakisoba0728/PgBase/internal/fieldvalue"
	"github.com/yakisoba0728/PgBase/internal/passwordfield"
	"github.com/yakisoba0728/PgBase/internal/schema"
	validationdomain "github.com/yakisoba0728/PgBase/internal/validation"
)

const defaultPasswordMaxRunes = 71
const maximumAutogenerateAttempts = 16

func (s *Service) prepareGeneratedFields(ctx context.Context, tx pgx.Tx, collection schema.Collection,
	input map[string]json.RawMessage, create bool,
) (map[string]json.RawMessage, error) {
	result := cloneRawInput(input)
	needsClock := false
	for _, field := range collection.Fields {
		switch field.Type {
		case schema.TypeText:
			raw, supplied := input[field.Name]
			if supplied {
				if field.Unique {
					if value, ok := textInput(raw); ok {
						if err := lockUniqueTextValue(ctx, tx, collection, field, value); err != nil {
							return nil, err
						}
					}
				}
				continue
			}
			if !create || field.Options.AutogeneratePattern == "" {
				continue
			}
			value, err := generateTextValue(ctx, tx, collection, field, s.random)
			if err != nil {
				return nil, err
			}
			result[field.Name], _ = json.Marshal(value)
		case schema.TypeAutoDate:
			if _, supplied := input[field.Name]; supplied {
				return nil, fieldError(field.Name, "field_read_only")
			}
			if (create && field.Options.OnCreate) || (!create && field.Options.OnUpdate) {
				needsClock = true
			}
		case schema.TypePassword:
			raw, supplied := input[field.Name]
			if !supplied {
				continue
			}
			encoded, err := preparePassword(field, raw)
			if err != nil {
				return nil, err
			}
			result[field.Name] = encoded
		}
	}
	if needsClock {
		var now time.Time
		if err := tx.QueryRow(ctx, `select statement_timestamp()`).Scan(&now); err != nil {
			return nil, err
		}
		encoded, _ := json.Marshal(now.UTC().Format(time.RFC3339Nano))
		for _, field := range collection.Fields {
			if field.Type == schema.TypeAutoDate && ((create && field.Options.OnCreate) || (!create && field.Options.OnUpdate)) {
				result[field.Name] = encoded
			}
		}
	}
	return result, nil
}

// generatedValidationCollection models fields satisfied by trusted create
// generation without generating values, taking locks, or reading the clock.
func generatedValidationCollection(collection schema.Collection, input map[string]json.RawMessage, create bool) (schema.Collection, error) {
	result := collection
	result.Fields = append([]schema.Field(nil), collection.Fields...)
	for index := range result.Fields {
		field := &result.Fields[index]
		if field.Type == schema.TypeAutoDate {
			if _, supplied := input[field.Name]; supplied {
				return schema.Collection{}, fieldError(field.Name, "field_read_only")
			}
			if create && field.Options.OnCreate {
				field.Required = false
			}
		}
		if create && field.Type == schema.TypeText && field.Options.AutogeneratePattern != "" {
			field.Required = false
		}
	}
	return result, nil
}

func generateTextValue(ctx context.Context, tx pgx.Tx, collection schema.Collection, field schema.Field, reader io.Reader) (string, error) {
	options := fieldvalue.Options{
		MinLength: field.Options.MinLength, MaxLength: field.Options.MaxLength,
		Pattern: field.Options.Pattern, Format: field.Options.Format,
		AllowedDomains: field.Options.AllowedDomains, DeniedDomains: field.Options.DeniedDomains,
		AllowedValues: field.Options.AllowedValues,
	}
	for range maximumAutogenerateAttempts {
		candidate, err := fieldvalue.RandomStringByPattern(reader, field.Options.AutogeneratePattern)
		if err != nil {
			return "", fmt.Errorf("generate field value: %w", err)
		}
		if fieldvalue.ValidateText(candidate, options) != "" {
			continue
		}
		if !field.Unique {
			return candidate, nil
		}
		if err := lockUniqueTextValue(ctx, tx, collection, field, candidate); err != nil {
			return "", err
		}
		available, err := uniqueTextValueAvailable(ctx, tx, collection, field, candidate)
		if err != nil {
			return "", err
		}
		if available {
			return candidate, nil
		}
	}
	return "", fieldError(field.Name, "autogenerate_exhausted")
}

func textInput(raw json.RawMessage) (string, bool) {
	var value string
	return value, json.Unmarshal(raw, &value) == nil
}

func lockUniqueTextValue(ctx context.Context, tx pgx.Tx, collection schema.Collection, field schema.Field, value string) error {
	digest := sha256.Sum256(append(append(append([]byte("pgbase-unique-text-v1\x00"), collection.ID[:]...), field.ID[:]...), []byte(value)...))
	key := int64(binary.BigEndian.Uint64(digest[:8]))
	_, err := tx.Exec(ctx, `select pg_advisory_xact_lock($1)`, key)
	return err
}

func uniqueTextValueAvailable(ctx context.Context, tx pgx.Tx, collection schema.Collection, field schema.Field, value string) (bool, error) {
	query := "select not exists(select 1 from " + pgx.Identifier{"app", collection.TableName}.Sanitize() + " where " + pgx.Identifier{field.ColumnName}.Sanitize() + "=$1)"
	var available bool
	err := tx.QueryRow(ctx, query, value).Scan(&available)
	return available, err
}

func preparePassword(field schema.Field, raw json.RawMessage) (json.RawMessage, error) {
	if bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		if field.Required {
			return nil, fieldError(field.Name, "required")
		}
		return json.RawMessage("null"), nil
	}
	var plaintext string
	if json.Unmarshal(raw, &plaintext) != nil || !utf8.ValidString(plaintext) {
		return nil, fieldError(field.Name, "type_mismatch")
	}
	if plaintext == "" {
		if field.Required {
			return nil, fieldError(field.Name, "required")
		}
		return json.RawMessage("null"), nil
	}
	maximum := field.Options.MaxLength
	if maximum == nil {
		value := defaultPasswordMaxRunes
		maximum = &value
	}
	if code := fieldvalue.ValidateText(plaintext, fieldvalue.Options{
		MinLength: field.Options.MinLength, MaxLength: maximum, Pattern: field.Options.Pattern,
	}); code != "" {
		return nil, fieldError(field.Name, code)
	}
	hash, err := passwordfield.Hash(plaintext)
	if err != nil {
		return nil, err
	}
	encoded, _ := json.Marshal(hash)
	return encoded, nil
}

func cloneRawInput(input map[string]json.RawMessage) map[string]json.RawMessage {
	result := make(map[string]json.RawMessage, len(input))
	for name, raw := range input {
		result[name] = append(json.RawMessage(nil), raw...)
	}
	return result
}

// generatedReplayInput reconstructs the request body that authorized the
// committed create. Generated values come from the idempotency receipt rather
// than being generated again, so a replay observes the same :isset/body values
// without taking uniqueness locks or consulting a later clock.
func generatedReplayInput(collection schema.Collection, input map[string]json.RawMessage, stored map[string]any) (map[string]json.RawMessage, error) {
	result := cloneRawInput(input)
	for _, field := range collection.Fields {
		generated := field.Type == schema.TypeAutoDate && field.Options.OnCreate ||
			field.Type == schema.TypeText && field.Options.AutogeneratePattern != ""
		if !generated {
			continue
		}
		if _, supplied := input[field.Name]; supplied {
			continue
		}
		value, ok := stored[field.Name]
		if !ok {
			return nil, fmt.Errorf("reconstruct generated replay field %q", field.Name)
		}
		encoded, err := json.Marshal(value)
		if err != nil {
			return nil, fmt.Errorf("reconstruct generated replay field %q: %w", field.Name, err)
		}
		result[field.Name] = encoded
	}
	return result, nil
}

func fieldError(field, code string) error {
	return &validationdomain.Error{Details: []validationdomain.Detail{{Field: field, Code: code}}}
}
