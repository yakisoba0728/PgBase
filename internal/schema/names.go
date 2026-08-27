package schema

import (
	"regexp"
	"strings"
)

var collectionOrFieldName = regexp.MustCompile(`^[a-z][a-z0-9_]{0,47}$`)

var reservedNames = map[string]struct{}{
	"id":         {},
	"created_at": {},
	"updated_at": {},
}

func validateName(field, name string) error {
	if !collectionOrFieldName.MatchString(name) {
		return validation(field, "invalid_name")
	}
	if _, reserved := reservedNames[name]; reserved || strings.HasPrefix(name, "pgbase_") {
		return validation(field, "reserved_name")
	}
	return nil
}

func validation(field, code string) error {
	return &ValidationError{Details: []ValidationDetail{{Field: field, Code: code}}}
}
