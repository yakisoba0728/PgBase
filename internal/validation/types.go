// Package validation defines stable, caller-addressable domain validation.
package validation

type Detail struct {
	Field string
	Code  string
}

type Error struct {
	Details []Detail
}

func (e *Error) Error() string { return "validation failed" }
