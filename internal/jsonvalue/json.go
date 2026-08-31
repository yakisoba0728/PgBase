// Package jsonvalue provides strict JSON validation shared by HTTP, schema,
// and record boundaries before values reach PostgreSQL or encoding/json.
package jsonvalue

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"strconv"
	"strings"
	"unicode/utf8"
)

const (
	postgresNumericMaxIntegerDigits  = 131072
	postgresNumericMaxFractionDigits = 16383
	maximumUsefulExponent            = postgresNumericMaxIntegerDigits + postgresNumericMaxFractionDigits
)

var ErrInvalid = errors.New("invalid JSON value")

// ValidateUnicode rejects byte-invalid UTF-8, NUL, and unpaired UTF-16
// surrogate escapes before encoding/json can replace them with U+FFFD.
func ValidateUnicode(raw []byte) error {
	if !utf8.Valid(raw) {
		return ErrInvalid
	}
	inString := false
	for index := 0; index < len(raw); {
		character := raw[index]
		if !inString {
			if character == '"' {
				inString = true
			}
			index++
			continue
		}
		switch character {
		case '"':
			inString = false
			index++
		case '\\':
			if index+1 >= len(raw) {
				return ErrInvalid
			}
			if raw[index+1] != 'u' {
				index += 2
				continue
			}
			value, ok := unicodeEscape(raw, index)
			if !ok || value == 0 {
				return ErrInvalid
			}
			if value >= 0xd800 && value <= 0xdbff {
				if index+12 > len(raw) || raw[index+6] != '\\' || raw[index+7] != 'u' {
					return ErrInvalid
				}
				low, ok := unicodeEscape(raw, index+6)
				if !ok || low < 0xdc00 || low > 0xdfff {
					return ErrInvalid
				}
				index += 12
				continue
			}
			if value >= 0xdc00 && value <= 0xdfff {
				return ErrInvalid
			}
			index += 6
		default:
			if character < 0x20 {
				return ErrInvalid
			}
			index++
		}
	}
	if inString {
		return ErrInvalid
	}
	return nil
}

// Valid reports whether raw is one strict JSON value whose strings and
// numbers can be represented by PostgreSQL jsonb.
func Valid(raw []byte) bool {
	_, err := decode(raw)
	return err == nil
}

// Canonical returns encoding/json's deterministic representation after strict
// PostgreSQL-compatible validation.
func Canonical(raw []byte) ([]byte, error) {
	value, err := decode(raw)
	if err != nil {
		return nil, err
	}
	canonical, err := json.Marshal(value)
	if err != nil {
		return nil, ErrInvalid
	}
	return canonical, nil
}

// String decodes exactly one strict JSON string.
func String(raw []byte) (string, error) {
	if err := ValidateUnicode(raw); err != nil || len(raw) < 2 || raw[0] != '"' {
		return "", ErrInvalid
	}
	var value string
	if err := json.Unmarshal(raw, &value); err != nil || strings.IndexByte(value, 0) >= 0 {
		return "", ErrInvalid
	}
	return value, nil
}

// ValidPostgresNumber reports whether a JSON number lexeme is within
// PostgreSQL numeric/jsonb's documented digit limits.
func ValidPostgresNumber(raw string) bool {
	if raw == "" {
		return false
	}
	unsigned := strings.TrimPrefix(raw, "-")
	exponent := 0
	if position := strings.IndexAny(unsigned, "eE"); position >= 0 {
		parsed, err := strconv.Atoi(unsigned[position+1:])
		if err != nil || parsed > maximumUsefulExponent || parsed < -maximumUsefulExponent {
			return false
		}
		exponent = parsed
		unsigned = unsigned[:position]
	}
	parts := strings.SplitN(unsigned, ".", 2)
	whole := parts[0]
	fraction := ""
	if len(parts) == 2 {
		fraction = parts[1]
	}
	digits := whole + fraction
	first := strings.IndexAny(digits, "123456789")
	decimalPosition := len(whole) + exponent
	integerDigits := 0
	if first >= 0 {
		integerDigits = decimalPosition - first
		if integerDigits < 0 {
			integerDigits = 0
		}
	}
	// PostgreSQL preserves the lexical display scale when it parses numeric
	// input. Trailing zeroes and even an all-zero value therefore still count
	// toward the 16,383 fractional-digit limit.
	fractionDigits := len(fraction) - exponent
	if fractionDigits < 0 {
		fractionDigits = 0
	}
	return integerDigits <= postgresNumericMaxIntegerDigits && fractionDigits <= postgresNumericMaxFractionDigits
}

func decode(raw []byte) (any, error) {
	if err := ValidateUnicode(raw); err != nil {
		return nil, err
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	var value any
	if err := decoder.Decode(&value); err != nil {
		return nil, ErrInvalid
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		return nil, ErrInvalid
	}
	if !postgresCompatible(value) {
		return nil, ErrInvalid
	}
	return value, nil
}

func postgresCompatible(value any) bool {
	switch value := value.(type) {
	case nil, bool:
		return true
	case string:
		return strings.IndexByte(value, 0) < 0
	case json.Number:
		return ValidPostgresNumber(value.String())
	case []any:
		for _, item := range value {
			if !postgresCompatible(item) {
				return false
			}
		}
		return true
	case map[string]any:
		for key, item := range value {
			if strings.IndexByte(key, 0) >= 0 || !postgresCompatible(item) {
				return false
			}
		}
		return true
	default:
		return false
	}
}

func unicodeEscape(raw []byte, slash int) (uint16, bool) {
	if slash+6 > len(raw) || raw[slash] != '\\' || raw[slash+1] != 'u' {
		return 0, false
	}
	var value uint16
	for _, character := range raw[slash+2 : slash+6] {
		value <<= 4
		switch {
		case character >= '0' && character <= '9':
			value += uint16(character - '0')
		case character >= 'a' && character <= 'f':
			value += uint16(character-'a') + 10
		case character >= 'A' && character <= 'F':
			value += uint16(character-'A') + 10
		default:
			return 0, false
		}
	}
	return value, true
}
