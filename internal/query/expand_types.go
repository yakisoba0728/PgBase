package query

import (
	"context"
	"encoding/json"
	"errors"
	"sort"

	"github.com/google/uuid"
	"github.com/webisnoteasy/PgBase/internal/identity"
	"github.com/webisnoteasy/PgBase/internal/schema"
)

const (
	PrincipalAdministrator PrincipalKind = identity.Administrator
	ReadActionExpand       ReadAction    = "expand"
)

type PrincipalKind = identity.Kind

// Principal is the authenticated identity supplied to ProjectionPolicy. It is
// shared across application boundaries so callers can pass one typed subject
// without translation.
type Principal = identity.Subject

type ReadAction string

// Predicate is a validated, typed policy restriction. Its representation is
// private so SQL can only be produced by this package.
type Predicate struct {
	kind predicateKind
	ids  []uuid.UUID
}

type predicateKind uint8

const (
	predicateInvalid predicateKind = iota
	predicateAllowAll
	predicateDenyAll
	predicateRecordIDs
)

func AllowAllRecords() Predicate { return Predicate{kind: predicateAllowAll} }
func DenyAllRecords() Predicate  { return Predicate{kind: predicateDenyAll} }

func AllowRecordIDs(ids ...uuid.UUID) (Predicate, error) {
	seen := make(map[uuid.UUID]struct{}, len(ids))
	normalized := make([]uuid.UUID, 0, len(ids))
	for _, id := range ids {
		if id == uuid.Nil {
			return Predicate{}, errors.New("invalid projection predicate")
		}
		if _, duplicate := seen[id]; duplicate {
			continue
		}
		seen[id] = struct{}{}
		normalized = append(normalized, id)
	}
	sort.Slice(normalized, func(left, right int) bool {
		return normalized[left].String() < normalized[right].String()
	})
	return Predicate{kind: predicateRecordIDs, ids: normalized}, nil
}

// FieldSet is a typed target projection. System fields are always retained;
// named fields refer only to catalog field names.
type FieldSet struct {
	all    bool
	fields map[string]struct{}
}

func AllFields() FieldSet { return FieldSet{all: true} }

func ProjectFields(fields ...string) FieldSet {
	selected := make(map[string]struct{}, len(fields))
	for _, field := range fields {
		selected[field] = struct{}{}
	}
	return FieldSet{fields: selected}
}

type ProjectionPolicy interface {
	ReadScope(context.Context, Principal, schema.Collection, ReadAction) (Predicate, FieldSet, error)
}

// AdminAllowAll is the first ProjectionPolicy adapter. Authentication remains
// owned by the caller; this adapter fails closed unless it receives a verified
// administrator identity.
type AdminAllowAll struct{}

func (AdminAllowAll) ReadScope(_ context.Context, principal Principal, _ schema.Collection, action ReadAction) (Predicate, FieldSet, error) {
	if !principal.Valid() {
		return Predicate{}, FieldSet{}, ErrProjectionDenied
	}
	switch action {
	case ReadActionExpand, ReadActionRelationFilter, ReadActionRelationSort:
	default:
		return Predicate{}, FieldSet{}, ErrProjectionDenied
	}
	return AllowAllRecords(), AllFields(), nil
}

var ErrProjectionDenied = errors.New("record projection denied")

type GetRequest struct {
	Principal  Principal
	Collection string
	ID         uuid.UUID
	Expand     string
}

type ListRequest struct {
	Principal  Principal
	Collection string
	Cursor     string
	Limit      int
	Filter     string
	Sort       string
	Expand     string
}

type ExpandRequest struct {
	Principal Principal
	Expand    string
}

// ExpandedRecord keeps expansion outside Root so record validators, ETags,
// cursor sort values, and cursor hashes cannot accidentally include targets.
// MarshalJSON emits the PocketBase-compatible flat record plus sibling expand.
type ExpandedRecord struct {
	Root   map[string]any `json:"-"`
	Expand map[string]any `json:"-"`

	querySteps int
	oversized  bool
}

func (r ExpandedRecord) MarshalJSON() ([]byte, error) {
	response, err := r.Flatten()
	if err != nil {
		return nil, err
	}
	return json.Marshal(response)
}

// Flatten returns the external PocketBase-style representation as a
// caller-owned deep copy. Root fields remain siblings and requested targets
// are attached under "expand" without mutating ETag/cursor material.
func (r ExpandedRecord) Flatten() (map[string]any, error) {
	if r.oversized {
		return nil, validation("response", "response_too_large")
	}
	response := cloneRecord(r.Root)
	if response == nil {
		response = make(map[string]any)
	}
	if len(r.Expand) != 0 {
		if _, collision := response["expand"]; collision {
			return nil, errors.New("flatten expanded record: reserved field collision")
		}
		response["expand"] = cloneRecord(r.Expand)
	}
	return response, nil
}

type ExpandedRecordPage struct {
	Items      []ExpandedRecord `json:"items"`
	NextCursor string           `json:"nextCursor,omitempty"`

	querySteps int
}

type expandLimits struct {
	maxBytes       int
	maxDepth       int
	maxNodes       int
	maxRows        int
	maxReverseRows int
	maxQuerySteps  int
}

var defaultExpandLimits = expandLimits{
	maxBytes:       maximumRecordPageBytes,
	maxDepth:       6,
	maxNodes:       20,
	maxRows:        10_000,
	maxReverseRows: 1_000,
	maxQuerySteps:  64,
}
