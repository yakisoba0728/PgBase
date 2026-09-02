// Package cursor owns the bounded, signed wire representation of record-page
// continuations. It deliberately has no dependency on query planning or
// catalog types; callers provide stable descriptor identities and canonical
// values at the boundary.
package cursor

import (
	"encoding/json"
	"errors"
	"time"

	"github.com/google/uuid"
)

const (
	Version5       = 5
	maxDecodedSize = 4096
	maxOrderTerms  = 9
	maxPathParts   = 3
)

type SortKey struct {
	Field string
	Desc  bool
}

type Page struct {
	Version       int
	CollectionID  uuid.UUID
	Values        []json.RawMessage
	AnchorID      uuid.UUID
	AnchorHash    []byte
	PrivateValues bool
	ExpiresAt     time.Time
	V5            *V5Descriptor
}

type NullPlacement string

const (
	NullsFirst NullPlacement = "first"
	NullsLast  NullPlacement = "last"
)

type V5OrderTerm struct {
	Path  []string
	Type  string
	Desc  bool
	Nulls NullPlacement
}

type V5Descriptor struct {
	CatalogVersion       int64
	MembershipGeneration int64
	QueryHash            []byte
	ScopeHash            []byte
	Order                []V5OrderTerm
	// HighWater fixes which record creations belong to a traversal. It does
	// not snapshot later changes to the sort values of records already inside
	// that membership boundary.
	HighWater int64
}

type InvalidError struct{}

func (*InvalidError) Error() string { return "invalid cursor" }

func IsInvalid(err error) bool {
	var target *InvalidError
	return errors.As(err, &target)
}

func invalid() error { return &InvalidError{} }
