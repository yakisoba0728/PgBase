package cursor

import (
	"bytes"
	"encoding/json"
	"strconv"
)

// Keyset cursors continue a newest-first listing ordered by one positive,
// strictly decreasing sequence value inside a named scope, such as the run
// history of one scheduled job. They carry no expiry because the sequence is
// immutable: a continuation can only skip rows that retention removed.
const (
	keysetDomain        = "cursor-keyset-v1\x00"
	maxKeysetScopeBytes = 256
)

type keysetPayload struct {
	Scope  string `json:"scope"`
	Before string `json:"before"`
}

// EncodeKeyset signs a continuation that resumes strictly below before.
func (c *Codec) EncodeKeyset(scope string, before int64) (string, error) {
	if scope == "" || len(scope) > maxKeysetScopeBytes || before <= 0 {
		return "", invalid()
	}
	payload, err := json.Marshal(keysetPayload{Scope: scope, Before: strconv.FormatInt(before, 10)})
	if err != nil {
		return "", invalid()
	}
	return c.signAndEncodeIn(keysetDomain, payload)
}

// DecodeKeyset accepts only the canonical token this codec minted for the same
// scope. Forged, re-encoded, and cross-scope tokens are all invalid.
func (c *Codec) DecodeKeyset(scope, token string) (int64, error) {
	if scope == "" || len(scope) > maxKeysetScopeBytes {
		return 0, invalid()
	}
	payload, err := c.authenticatedPayloadIn(keysetDomain, token)
	if err != nil {
		return 0, err
	}
	var decoded keysetPayload
	decoder := json.NewDecoder(bytes.NewReader(payload))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&decoded); err != nil || decoded.Scope != scope {
		return 0, invalid()
	}
	before, err := strconv.ParseInt(decoded.Before, 10, 64)
	if err != nil || before <= 0 || strconv.FormatInt(before, 10) != decoded.Before {
		return 0, invalid()
	}
	canonical, err := c.EncodeKeyset(scope, before)
	if err != nil || canonical != token {
		return 0, invalid()
	}
	return before, nil
}
