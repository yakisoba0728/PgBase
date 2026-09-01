// Package identity defines the authenticated subject shared by application
// boundaries.
package identity

import "github.com/google/uuid"

// Kind identifies the class of an authenticated subject.
type Kind string

// Administrator is the authenticated administrator subject kind.
const Administrator Kind = "administrator"

// Subject is the stable identity supplied by authentication to domain
// operations.
type Subject struct {
	Kind Kind
	ID   uuid.UUID
}

// Valid reports whether the subject has a supported kind and a non-zero ID.
func (subject Subject) Valid() bool {
	return subject.Kind == Administrator && subject.ID != uuid.Nil
}
