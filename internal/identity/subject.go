// Package identity defines the authenticated subject shared by application
// boundaries.
package identity

import "github.com/google/uuid"

type Kind string

const Administrator Kind = "administrator"

const User Kind = "user"

// Subject is the stable identity supplied by authentication to domain
// operations.
type Subject struct {
	Kind Kind
	ID   uuid.UUID
}

func (subject Subject) Valid() bool {
	return (subject.Kind == Administrator || subject.Kind == User) && subject.ID != uuid.Nil
}
