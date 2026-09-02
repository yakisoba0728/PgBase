package cursor

import (
	"bytes"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"io"
	"regexp"
	"strconv"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/webisnoteasy/PgBase/internal/jsonvalue"
)

var decimal = regexp.MustCompile(`^-?[0-9]+(?:\.[0-9]+)?$`)

type v5OrderEntry struct {
	Path  []string      `json:"path"`
	Type  string        `json:"type"`
	Desc  bool          `json:"desc"`
	Nulls NullPlacement `json:"nulls"`
}

type v5Payload struct {
	Version              int               `json:"v"`
	CollectionID         string            `json:"collectionId"`
	CatalogVersion       string            `json:"catalogVersion"`
	MembershipGeneration string            `json:"membershipGeneration"`
	QueryHash            string            `json:"queryHash"`
	ScopeHash            string            `json:"scopeHash"`
	Order                []v5OrderEntry    `json:"order"`
	HighWater            string            `json:"highWater"`
	Values               []json.RawMessage `json:"values,omitempty"`
	AnchorID             string            `json:"anchorId,omitempty"`
	AnchorHash           string            `json:"anchorHash,omitempty"`
	ExpiresAt            string            `json:"expiresAt"`
}

func (c *Codec) Encode(page Page) (string, error) {
	if page.V5 == nil || page.CollectionID == uuid.Nil || page.ExpiresAt.IsZero() {
		return "", invalid()
	}
	_, expiryOffset := page.ExpiresAt.Zone()
	if expiryOffset != 0 || page.V5.CatalogVersion <= 0 || page.V5.MembershipGeneration < 0 || page.V5.HighWater < 0 ||
		!validDigest(page.V5.QueryHash) || !validDigest(page.V5.ScopeHash) {
		return "", invalid()
	}
	order, ok := encodeV5Order(page.V5.Order)
	if !ok {
		return "", invalid()
	}
	hasValues := len(page.Values) == len(order)
	hasAnchorOnly := len(page.Values) == 0 && page.PrivateValues && page.AnchorID != uuid.Nil && validDigest(page.AnchorHash)
	if (!hasValues && !hasAnchorOnly) || (hasValues && len(page.AnchorHash) != 0) {
		return "", invalid()
	}
	values := cloneValues(page.Values)
	if hasValues && !validV5Values(values, page.V5.Order) {
		return "", invalid()
	}
	wire := v5Payload{
		Version: Version5, CollectionID: page.CollectionID.String(),
		CatalogVersion:       strconv.FormatInt(page.V5.CatalogVersion, 10),
		MembershipGeneration: strconv.FormatInt(page.V5.MembershipGeneration, 10),
		QueryHash:            base64.RawURLEncoding.EncodeToString(page.V5.QueryHash),
		ScopeHash:            base64.RawURLEncoding.EncodeToString(page.V5.ScopeHash),
		Order:                order,
		HighWater:            strconv.FormatInt(page.V5.HighWater, 10),
		Values:               values,
		ExpiresAt:            page.ExpiresAt.UTC().Format(time.RFC3339Nano),
	}
	if hasAnchorOnly {
		wire.AnchorID = page.AnchorID.String()
		wire.AnchorHash = base64.RawURLEncoding.EncodeToString(page.AnchorHash)
	}
	payload, err := json.Marshal(wire)
	if err != nil {
		return "", invalid()
	}
	if hasValues && (page.PrivateValues || len(payload)+sha256.Size > maxDecodedSize) {
		if page.AnchorID == uuid.Nil {
			return "", invalid()
		}
		digest, digestErr := c.ValuesHash(values)
		if digestErr != nil {
			return "", invalid()
		}
		wire.Values = nil
		wire.AnchorID = page.AnchorID.String()
		wire.AnchorHash = base64.RawURLEncoding.EncodeToString(digest)
		payload, err = json.Marshal(wire)
		if err != nil {
			return "", invalid()
		}
	}
	return c.signAndEncode(payload)
}

// Decode authenticates the envelope before it parses anything, so an
// unsigned or tampered token never reaches the wire format. A token in any
// earlier format fails the same way a corrupt one does: this binary knows one
// format, and everything else is simply an invalid cursor.
func (c *Codec) Decode(token string, collectionID uuid.UUID, now time.Time) (Page, error) {
	payload, err := c.authenticatedPayload(token)
	if err != nil {
		return Page{}, err
	}
	decoder := json.NewDecoder(bytes.NewReader(payload))
	decoder.DisallowUnknownFields()
	var wire v5Payload
	if decoder.Decode(&wire) != nil {
		return Page{}, invalid()
	}
	var extra any
	if decoder.Decode(&extra) != io.EOF {
		return Page{}, invalid()
	}
	canonical, marshalErr := json.Marshal(wire)
	if marshalErr != nil || !bytes.Equal(canonical, payload) || wire.Version != Version5 {
		return Page{}, invalid()
	}
	payloadCollectionID, ok := canonicalUUID(wire.CollectionID)
	if !ok || payloadCollectionID != collectionID {
		return Page{}, invalid()
	}
	catalogVersion, ok := decimalInt64(wire.CatalogVersion, true)
	if !ok {
		return Page{}, invalid()
	}
	membershipGeneration, ok := decimalInt64(wire.MembershipGeneration, false)
	if !ok {
		return Page{}, invalid()
	}
	highWater, ok := decimalInt64(wire.HighWater, false)
	if !ok {
		return Page{}, invalid()
	}
	queryHash, ok := decodeDigest(wire.QueryHash)
	if !ok {
		return Page{}, invalid()
	}
	scopeHash, ok := decodeDigest(wire.ScopeHash)
	if !ok {
		return Page{}, invalid()
	}
	order, ok := decodeV5Order(wire.Order)
	if !ok {
		return Page{}, invalid()
	}
	anchorID, anchorHash, hasAnchorOnly, ok := decodeAnchor(wire.AnchorID, wire.AnchorHash, len(wire.Values) == 0, true)
	if !ok || (len(wire.Values) != len(order) && !hasAnchorOnly) || (len(wire.Values) == len(order) && hasAnchorOnly) {
		return Page{}, invalid()
	}
	values := cloneValues(wire.Values)
	if len(values) != 0 && !validV5Values(values, order) {
		return Page{}, invalid()
	}
	expiresAt, ok := canonicalExpiry(wire.ExpiresAt, now)
	if !ok {
		return Page{}, invalid()
	}
	return Page{
		Version: Version5, CollectionID: payloadCollectionID, Values: values,
		AnchorID: anchorID, AnchorHash: anchorHash, PrivateValues: hasAnchorOnly, ExpiresAt: expiresAt,
		V5: &V5Descriptor{
			CatalogVersion: catalogVersion, MembershipGeneration: membershipGeneration,
			QueryHash: queryHash, ScopeHash: scopeHash, Order: order, HighWater: highWater,
		},
	}, nil
}

func ValidateV5Order(terms []V5OrderTerm) bool {
	_, ok := encodeV5Order(terms)
	return ok
}

// Matches compares every query-planning identity carried by a v5 descriptor
// without making the first mismatching field observable through comparison
// timing. Membership generation and high-water are durable-state bindings and
// are validated by the membership owner in the caller's transaction.
func (descriptor *V5Descriptor) Matches(
	catalogVersion int64,
	queryHash, scopeHash []byte,
	order []V5OrderTerm,
) bool {
	if descriptor == nil {
		return false
	}
	wantOrder, wantErr := json.Marshal(order)
	gotOrder, gotErr := json.Marshal(descriptor.Order)
	if wantErr != nil || gotErr != nil {
		return false
	}
	wantOrderHash, gotOrderHash := sha256.Sum256(wantOrder), sha256.Sum256(gotOrder)
	matches := subtle.ConstantTimeEq(int32(descriptor.CatalogVersion), int32(catalogVersion)) &
		subtle.ConstantTimeEq(int32(descriptor.CatalogVersion>>32), int32(catalogVersion>>32))
	matches &= subtle.ConstantTimeCompare(descriptor.QueryHash, queryHash)
	matches &= subtle.ConstantTimeCompare(descriptor.ScopeHash, scopeHash)
	matches &= subtle.ConstantTimeCompare(gotOrderHash[:], wantOrderHash[:])
	return matches == 1
}

func encodeV5Order(terms []V5OrderTerm) ([]v5OrderEntry, bool) {
	if len(terms) == 0 || len(terms) > maxOrderTerms {
		return nil, false
	}
	result := make([]v5OrderEntry, len(terms))
	for index, term := range terms {
		if !validPath(term.Path) || !validOrderType(term.Type) || !validSystemType(term) ||
			(term.Nulls != NullsFirst && term.Nulls != NullsLast) {
			return nil, false
		}
		result[index] = v5OrderEntry{
			Path: append([]string(nil), term.Path...), Type: term.Type, Desc: term.Desc, Nulls: term.Nulls,
		}
	}
	last := terms[len(terms)-1]
	if len(last.Path) != 1 || last.Path[0] != "$id" || last.Type != "uuid" {
		return nil, false
	}
	return result, true
}

func decodeV5Order(entries []v5OrderEntry) ([]V5OrderTerm, bool) {
	terms := make([]V5OrderTerm, len(entries))
	for index, entry := range entries {
		terms[index] = V5OrderTerm{Path: entry.Path, Type: entry.Type, Desc: entry.Desc, Nulls: entry.Nulls}
	}
	if _, ok := encodeV5Order(terms); !ok {
		return nil, false
	}
	for index := range terms {
		terms[index].Path = append([]string(nil), terms[index].Path...)
	}
	return terms, true
}

func validPath(path []string) bool {
	if len(path) == 0 || len(path) > maxPathParts {
		return false
	}
	for index, segment := range path {
		switch segment {
		case "$id", "$created_at", "$updated_at":
			if index != len(path)-1 {
				return false
			}
			continue
		}
		if _, ok := canonicalUUID(segment); !ok {
			return false
		}
	}
	return true
}

func validSystemType(term V5OrderTerm) bool {
	switch term.Path[len(term.Path)-1] {
	case "$id":
		return term.Type == "uuid"
	case "$created_at", "$updated_at":
		return term.Type == "timestamptz"
	default:
		return true
	}
}

func validOrderType(fieldType string) bool {
	switch fieldType {
	case "boolean", "integer", "bigint", "numeric", "text", "timestamptz", "uuid":
		return true
	default:
		return false
	}
}

func validV5Values(values []json.RawMessage, order []V5OrderTerm) bool {
	if len(values) != len(order) {
		return false
	}
	for index, raw := range values {
		if !canonicalTypedValue(raw, order[index].Type) {
			return false
		}
	}
	return true
}

func canonicalTypedValue(raw json.RawMessage, fieldType string) bool {
	if bytes.Equal(raw, []byte("null")) {
		return true
	}
	var canonical []byte
	var err error
	switch fieldType {
	case "boolean":
		var value bool
		if json.Unmarshal(raw, &value) != nil {
			return false
		}
		canonical, err = json.Marshal(value)
	case "integer":
		value, parseErr := strconv.ParseInt(string(raw), 10, 32)
		if parseErr != nil {
			return false
		}
		canonical = []byte(strconv.FormatInt(value, 10))
	case "bigint":
		value, ok := jsonString(raw)
		if !ok {
			return false
		}
		parsed, parseErr := strconv.ParseInt(value, 10, 64)
		if parseErr != nil {
			return false
		}
		canonical, err = json.Marshal(strconv.FormatInt(parsed, 10))
	case "numeric":
		value, ok := jsonString(raw)
		if !ok || !decimal.MatchString(value) || !jsonvalue.ValidPostgresNumber(value) {
			return false
		}
		var numeric pgtype.Numeric
		if numeric.Scan(value) != nil || !numeric.Valid || numeric.NaN || numeric.InfinityModifier != pgtype.Finite {
			return false
		}
		stored, valueErr := numeric.Value()
		if valueErr != nil {
			return false
		}
		canonical, err = json.Marshal(stored)
	case "text":
		value, ok := jsonString(raw)
		if !ok {
			return false
		}
		canonical, err = json.Marshal(value)
	case "timestamptz":
		value, ok := jsonString(raw)
		if !ok {
			return false
		}
		parsed, parseErr := time.Parse(time.RFC3339Nano, value)
		if parseErr != nil {
			return false
		}
		canonical, err = json.Marshal(parsed.UTC().Format(time.RFC3339Nano))
	case "uuid":
		value, ok := jsonString(raw)
		if !ok {
			return false
		}
		parsed, parseErr := uuid.Parse(value)
		if parseErr != nil || parsed.String() != value {
			return false
		}
		canonical, err = json.Marshal(parsed.String())
	default:
		return false
	}
	return err == nil && bytes.Equal(canonical, raw)
}

func jsonString(raw []byte) (string, bool) {
	value, err := jsonvalue.String(raw)
	if err != nil {
		return "", false
	}
	return value, true
}

func decimalInt64(value string, positive bool) (int64, bool) {
	parsed, err := strconv.ParseInt(value, 10, 64)
	if err != nil || strconv.FormatInt(parsed, 10) != value || parsed < 0 || (positive && parsed == 0) {
		return 0, false
	}
	return parsed, true
}

func decodeDigest(value string) ([]byte, bool) {
	decoded, err := base64.RawURLEncoding.DecodeString(value)
	return decoded, err == nil && validDigest(decoded) && base64.RawURLEncoding.EncodeToString(decoded) == value
}

func validDigest(value []byte) bool {
	if len(value) != sha256.Size {
		return false
	}
	for _, current := range value {
		if current != 0 {
			return true
		}
	}
	return false
}
