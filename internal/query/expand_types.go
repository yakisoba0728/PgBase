package query

import (
	"encoding/json"
	"errors"

	"github.com/google/uuid"
	"github.com/yakisoba0728/PgBase/internal/identity"
)

type Principal = identity.Subject

type GetRequest struct {
	Principal               Principal
	Collection              string
	ID                      uuid.UUID
	Expand                  string
	Fields                  string
	PocketBaseCompatibility bool
}

type ListRequest struct {
	Principal               Principal
	Collection              string
	Cursor                  string
	Limit                   int
	Filter                  string
	Sort                    string
	Expand                  string
	Fields                  string
	PocketBaseCompatibility bool
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
