// Package identity defines the authenticated subject shared by application
// boundaries.
package identity

import "github.com/google/uuid"

type Kind string

const Administrator Kind = "administrator"

const User Kind = "user"
const Service Kind = "service"
const Anonymous Kind = "anonymous"

// Subject is the stable identity supplied by authentication to domain
// operations.
type Subject struct {
	Kind Kind
	ID   uuid.UUID
}

func (subject Subject) Valid() bool {
	return ((subject.Kind == Administrator || subject.Kind == User || subject.Kind == Service) && subject.ID != uuid.Nil) || (subject.Kind == Anonymous && subject.ID == uuid.Nil)
}
