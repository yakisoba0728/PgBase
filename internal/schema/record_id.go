package schema

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strings"

	"github.com/yakisoba0728/PgBase/internal/fieldvalue"
)

type RecordIDKind string

const (
	RecordIDUUID RecordIDKind = "uuid"
	RecordIDText RecordIDKind = "text"

	defaultRecordIDPattern      = `^[a-z0-9]+$`
	defaultRecordIDAutogenerate = `[a-z0-9]{15}`
	maximumRecordIDLength       = 255
)

// RecordIDPolicy describes the record identity visible at collection
// interfaces. UUID policies expose the physical UUID. Text policies keep that
// UUID internal and expose a durable, exact text identity.
type RecordIDPolicy struct {
	Kind                RecordIDKind `json:"kind"`
	MinLength           int          `json:"minLength,omitempty"`
	MaxLength           int          `json:"maxLength,omitempty"`
	Pattern             string       `json:"pattern,omitempty"`
	AutogeneratePattern string       `json:"autogeneratePattern,omitempty"`
}

func DefaultRecordIDPolicy() RecordIDPolicy { return RecordIDPolicy{Kind: RecordIDUUID} }

func normalizedRecordIDJSON(collection *Collection) ([]byte, error) {
	policy, err := NormalizeRecordIDPolicy(&collection.RecordID)
	if err != nil {
		return nil, err
	}
	collection.RecordID = policy
	encoded, err := json.Marshal(policy)
	if err != nil {
		return nil, fmt.Errorf("encode record identity policy: %w", err)
	}
	return encoded, nil
}

func DefaultTextRecordIDPolicy() RecordIDPolicy {
	return RecordIDPolicy{
		Kind: RecordIDText, MinLength: 15, MaxLength: 15,
		Pattern: defaultRecordIDPattern, AutogeneratePattern: defaultRecordIDAutogenerate,
	}
}

func NormalizeRecordIDPolicy(policy *RecordIDPolicy) (RecordIDPolicy, error) {
	if policy == nil || policy.Kind == "" || policy.Kind == RecordIDUUID {
		if policy != nil && (policy.MinLength != 0 || policy.MaxLength != 0 || policy.Pattern != "" || policy.AutogeneratePattern != "") {
			return RecordIDPolicy{}, validation("recordId", "uuid_options_not_allowed")
		}
		return DefaultRecordIDPolicy(), nil
	}
	if policy.Kind != RecordIDText {
		return RecordIDPolicy{}, validation("recordId.kind", "invalid")
	}
	normalized := *policy
	if normalized.MinLength == 0 && normalized.MaxLength == 0 && normalized.Pattern == "" && normalized.AutogeneratePattern == "" {
		normalized = DefaultTextRecordIDPolicy()
	}
	if normalized.MinLength < 1 || normalized.MaxLength < normalized.MinLength || normalized.MaxLength > maximumRecordIDLength {
		return RecordIDPolicy{}, validation("recordId", "invalid_length")
	}
	if normalized.Pattern == "" {
		return RecordIDPolicy{}, validation("recordId.pattern", "required")
	}
	if _, err := regexp.Compile(normalized.Pattern); err != nil || len(normalized.Pattern) > 1024 {
		return RecordIDPolicy{}, validation("recordId.pattern", "invalid")
	}
	if normalized.AutogeneratePattern != "" {
		if err := fieldvalue.ValidateRandomPattern(normalized.AutogeneratePattern, maximumRecordIDLength); err != nil {
			return RecordIDPolicy{}, validation("recordId.autogeneratePattern", "invalid")
		}
	}
	return normalized, nil
}

func ValidateRecordIDValue(policy RecordIDPolicy, value string) string {
	if policy.Kind != RecordIDText || value == "" {
		return "required"
	}
	length := len([]rune(value))
	if length < policy.MinLength {
		return "min_length"
	}
	if length > policy.MaxLength {
		return "max_length"
	}
	pattern, err := regexp.Compile(policy.Pattern)
	if err != nil || !pattern.MatchString(value) {
		return "pattern"
	}
	forbidden := []string{".", "/", `\`, "|", `"`, "'", "`", "<", ">", ":", "?", "*", "%", "$", "\x00", "\t", "\n", "\r", " "}
	for _, part := range forbidden {
		if strings.Contains(value, part) {
			return "forbidden_character"
		}
	}
	if length <= 4 {
		upper := strings.ToUpper(value)
		reserved := map[string]bool{"CON": true, "PRN": true, "AUX": true, "NUL": true}
		if len(upper) == 4 && (strings.HasPrefix(upper, "COM") || strings.HasPrefix(upper, "LPT")) && upper[3] >= '1' && upper[3] <= '9' {
			return "reserved"
		}
		if reserved[upper] {
			return "reserved"
		}
	}
	return ""
}
