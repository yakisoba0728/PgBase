package recordops

import (
	"bytes"
	"context"
	"encoding/json"
	"time"
	"unicode/utf8"

	"github.com/jackc/pgx/v5"
	"github.com/webisnoteasy/PgBase/internal/fieldvalue"
	"github.com/webisnoteasy/PgBase/internal/passwordfield"
	"github.com/webisnoteasy/PgBase/internal/schema"
	validationdomain "github.com/webisnoteasy/PgBase/internal/validation"
)

const defaultPasswordMaxRunes = 71

func prepareGeneratedFields(ctx context.Context, tx pgx.Tx, collection schema.Collection,
	input map[string]json.RawMessage, create bool,
) (map[string]json.RawMessage, error) {
	result := cloneRawInput(input)
	needsClock := false
	for _, field := range collection.Fields {
		switch field.Type {
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

func fieldError(field, code string) error {
	return &validationdomain.Error{Details: []validationdomain.Detail{{Field: field, Code: code}}}
}
