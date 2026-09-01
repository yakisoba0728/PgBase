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
	// PostgreSQL system columns cannot be shadowed by user columns.
	"ctid":     {},
	"xmin":     {},
	"xmax":     {},
	"cmin":     {},
	"cmax":     {},
	"tableoid": {},
	// Filter grammar literals are not valid field operands.
	"true":  {},
	"false": {},
	"null":  {},
}

// reservedInputNames are refused for new and updated schemas but tolerated in
// catalogs written before the reservation. Adding one of these to
// reservedNames would make validateCatalogIntegrity reject every read of a
// database that already holds the name, locking the whole catalog rather than
// the one collection that carries it.
var reservedInputNames = map[string]struct{}{
	// Expansion envelopes reserve this key, so a field of the same name makes
	// every expand request on the collection fail as an invalid path.
	"expand": {},
}

// validateName reports whether a name is structurally usable as a pgBase
// identifier. It is the catalog-integrity check, so it only refuses names that
// no valid catalog can contain.
func validateName(field, name string) error {
	if !collectionOrFieldName.MatchString(name) {
		return validation(field, "invalid_name")
	}
	if _, reserved := reservedNames[name]; reserved || strings.HasPrefix(name, "pgbase_") {
		return validation(field, "reserved_name")
	}
	return nil
}

// validateInputName applies validateName plus the names that are closed to new
// schemas. Plan and apply use it; catalog integrity does not.
func validateInputName(field, name string) error {
	if err := validateName(field, name); err != nil {
		return err
	}
	if _, reserved := reservedInputNames[name]; reserved {
		return validation(field, "reserved_name")
	}
	return nil
}

func validation(field, code string) error {
	return &ValidationError{Details: []ValidationDetail{{Field: field, Code: code}}}
}
