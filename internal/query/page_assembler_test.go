package query

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/webisnoteasy/PgBase/internal/cursor"
	"github.com/webisnoteasy/PgBase/internal/schema"
)

func TestPageAssemblerMeasuresEachWireOnceAcrossGrowingPrefixes(t *testing.T) {
	t.Parallel()

	collectionID := uuid.MustParse("bbbbbbbb-cccc-4ddd-8eee-ffffffffffff")
	expiresAt := time.Date(2026, 9, 2, 12, 0, 0, 0, time.UTC)
	codec := cursor.NewCodec([]byte("0123456789abcdef0123456789abcdef"))
	assembler := pageAssembler{
		cursors:      codec,
		order:        pageOrderForTest(collectionID, []SortKey{{Field: systemFieldID}}),
		expiresAt:    expiresAt,
		maximumBytes: 1 << 20,
		cursorV5:     pageDescriptorForTest([]SortKey{{Field: systemFieldID}}),
	}

	const measuredCandidates = 4
	marshalCounts := make([]int, measuredCandidates+1)
	candidates := make([]pageCandidate, len(marshalCounts))
	for index := range candidates {
		id := uuid.NewSHA1(collectionID, []byte{byte(index)})
		candidates[index] = pageCandidate{
			id:           id,
			root:         map[string]any{systemFieldID: id.String()},
			wire:         countingJSONWire{count: &marshalCounts[index], value: id.String()},
			cursorValues: []json.RawMessage{json.RawMessage(strconv.Quote(id.String()))},
		}
	}
	candidates[measuredCandidates].oversized = true

	for prefix := 1; prefix <= len(candidates); prefix++ {
		keep, cursor, err := assembler.finish(candidates[:prefix], true)
		if err != nil {
			t.Fatalf("finish(%d candidates) error = %v", prefix, err)
		}
		wantKeep := prefix
		if wantKeep > measuredCandidates {
			wantKeep = measuredCandidates
		}
		if keep != wantKeep {
			t.Fatalf("finish(%d candidates) kept %d, want %d", prefix, keep, wantKeep)
		}

		decoded, err := codec.Decode(cursor, collectionID, expiresAt.Add(-time.Minute))
		if err != nil {
			t.Fatalf("decode cursor from finish(%d candidates): %v", prefix, err)
		}
		wantAnchor := strconv.Quote(candidates[wantKeep-1].root[systemFieldID].(string))
		if len(decoded.Values) != 1 || string(decoded.Values[0]) != wantAnchor {
			t.Fatalf("finish(%d candidates) cursor values = %q, want [%s]", prefix, decoded.Values, wantAnchor)
		}
	}

	for index := range measuredCandidates {
		if marshalCounts[index] != 1 {
			t.Errorf("candidate %d marshaled %d times, want exactly once", index, marshalCounts[index])
		}
	}
	if marshalCounts[measuredCandidates] != 0 {
		t.Errorf("oversized candidate marshaled %d times, want zero", marshalCounts[measuredCandidates])
	}
}

func TestPageAssemblerFitCanRecoverWhenTerminalCursorDisappears(t *testing.T) {
	t.Parallel()

	collectionID := uuid.MustParse("aaaaaaaa-bbbb-4ccc-8ddd-eeeeeeeeeeee")
	expiresAt := time.Date(2026, 9, 2, 12, 0, 0, 0, time.UTC)
	assembler := pageAssembler{
		cursors:   cursor.NewCodec([]byte("0123456789abcdef0123456789abcdef")),
		order:     pageOrderForTest(collectionID, []SortKey{{Field: systemFieldID}}),
		expiresAt: expiresAt, maximumBytes: 1 << 20,
		cursorV5: pageDescriptorForTest([]SortKey{{Field: systemFieldID}}),
	}
	candidates := make([]pageCandidate, 2)
	for index := range candidates {
		id := uuid.NewSHA1(collectionID, []byte{byte(index)})
		wire := json.RawMessage(`{}`)
		if index == 0 {
			wire = json.RawMessage(strconv.Quote(strings.Repeat("x", 1024)))
		}
		candidates[index] = pageCandidate{
			id: id, root: map[string]any{systemFieldID: id.String()}, wire: wire,
			cursorValues: []json.RawMessage{json.RawMessage(strconv.Quote(id.String()))},
		}
	}
	firstBytes, err := candidates[0].measureWire()
	if err != nil {
		t.Fatal(err)
	}
	secondBytes, err := candidates[1].measureWire()
	if err != nil {
		t.Fatal(err)
	}
	_, firstWithCursor, err := assembler.measureResponse(candidates[:1], firstBytes, true)
	if err != nil {
		t.Fatal(err)
	}
	_, terminalPair, err := assembler.measureResponse(candidates, firstBytes+secondBytes, false)
	if err != nil {
		t.Fatal(err)
	}
	if terminalPair >= firstWithCursor {
		t.Fatalf("fixture terminal pair/cursor prefix bytes = %d/%d, want terminal pair smaller", terminalPair, firstWithCursor)
	}
	assembler.maximumBytes = terminalPair
	_, _, err = assembler.finish(candidates[:1], true)
	assertValidationDetail(t, err, "response", "response_too_large")
	keep, cursor, err := assembler.finish(candidates, false)
	if err != nil {
		t.Fatalf("terminal pair: %v", err)
	}
	if keep != 2 || cursor != "" {
		t.Fatalf("terminal pair kept/cursor = %d/%q, want 2/empty", keep, cursor)
	}
}

func TestPageAssemblerFitCanRecoverWhenNextAnchorCursorShrinks(t *testing.T) {
	t.Parallel()

	collectionID := uuid.MustParse("cccccccc-dddd-4eee-8fff-aaaaaaaaaaaa")
	rankOrder := []SortKey{{Field: "rank"}, {Field: systemFieldID}}
	assembler := pageAssembler{
		cursors:   cursor.NewCodec([]byte("0123456789abcdef0123456789abcdef")),
		order:     pageOrderForTest(collectionID, rankOrder),
		expiresAt: time.Date(2026, 9, 2, 12, 0, 0, 0, time.UTC), maximumBytes: 1 << 20,
		cursorV5: pageDescriptorForTest(rankOrder),
	}
	longValue := json.RawMessage(strconv.Quote(strings.Repeat("a", schema.MaximumTextBytes)))
	shortValue := json.RawMessage(`"b"`)
	candidates := []pageCandidate{
		{
			id:   uuid.NewSHA1(collectionID, []byte{1}),
			root: map[string]any{systemFieldID: uuid.NewSHA1(collectionID, []byte{1}).String()},
			wire: json.RawMessage(strconv.Quote(strings.Repeat("x", 1024))),
			cursorValues: []json.RawMessage{
				longValue, json.RawMessage(strconv.Quote(uuid.NewSHA1(collectionID, []byte{1}).String())),
			},
		},
		{
			id:   uuid.NewSHA1(collectionID, []byte{2}),
			root: map[string]any{systemFieldID: uuid.NewSHA1(collectionID, []byte{2}).String()},
			wire: json.RawMessage(`{}`),
			cursorValues: []json.RawMessage{
				shortValue, json.RawMessage(strconv.Quote(uuid.NewSHA1(collectionID, []byte{2}).String())),
			},
		},
	}
	firstBytes, err := candidates[0].measureWire()
	if err != nil {
		t.Fatal(err)
	}
	secondBytes, err := candidates[1].measureWire()
	if err != nil {
		t.Fatal(err)
	}
	_, longCursorPrefix, err := assembler.measureResponse(candidates[:1], firstBytes, true)
	if err != nil {
		t.Fatal(err)
	}
	_, shortCursorPair, err := assembler.measureResponse(candidates, firstBytes+secondBytes, true)
	if err != nil {
		t.Fatal(err)
	}
	if shortCursorPair >= longCursorPrefix {
		t.Fatalf("short/long cursor page bytes = %d/%d, want short anchor smaller", shortCursorPair, longCursorPrefix)
	}
	assembler.maximumBytes = shortCursorPair
	_, _, err = assembler.finish(candidates[:1], true)
	assertValidationDetail(t, err, "response", "response_too_large")
	keep, cursor, err := assembler.finish(candidates, true)
	if err != nil {
		t.Fatalf("short-anchor pair: %v", err)
	}
	if keep != 2 || cursor == "" {
		t.Fatalf("short-anchor pair kept/cursor = %d/%q, want 2/non-empty", keep, cursor)
	}
}

func TestPageAssemblerCursorSurvivesReleasedRootRepresentation(t *testing.T) {
	t.Parallel()

	collectionID := uuid.MustParse("dddddddd-eeee-4fff-8000-bbbbbbbbbbbb")
	recordID := uuid.MustParse("11111111-2222-4333-8444-555555555555")
	expiresAt := time.Date(2026, 9, 2, 12, 0, 0, 0, time.UTC)
	codec := cursor.NewCodec([]byte("0123456789abcdef0123456789abcdef"))
	assembler := pageAssembler{
		cursors: codec, order: pageOrderForTest(collectionID, []SortKey{{Field: systemFieldID}}),
		expiresAt: expiresAt, maximumBytes: 1 << 20,
		cursorV5: pageDescriptorForTest([]SortKey{{Field: systemFieldID}}),
	}
	assembler.order.terms[0].relationPath = &plannedRelationPath{}
	candidate := pageCandidate{
		id: recordID, cursorValues: []json.RawMessage{json.RawMessage(strconv.Quote(recordID.String()))},
		wireBytes: 2, wireMeasured: true,
	}

	_, cursorToken, err := assembler.finish([]pageCandidate{candidate}, true)
	if err != nil {
		t.Fatalf("finish released candidate: %v", err)
	}
	decoded, err := codec.Decode(cursorToken, collectionID, expiresAt.Add(-time.Minute))
	if err != nil {
		t.Fatalf("decode released candidate cursor: %v", err)
	}
	if decoded.Version != cursor.Version5 || decoded.V5 == nil {
		t.Fatalf("production page assembler issued cursor version %d with v5=%#v", decoded.Version, decoded.V5)
	}
	if decoded.AnchorID != recordID {
		t.Fatalf("released candidate anchor = %s, want %s", decoded.AnchorID, recordID)
	}
}

// A page cursor only exists inside a bound v5 traversal now, so an assembler
// under test carries the descriptor the reader would have minted for its order.
func pageDescriptorForTest(sortKeys []SortKey) *cursor.V5Descriptor {
	order := make([]cursor.V5OrderTerm, len(sortKeys))
	for index, key := range sortKeys {
		term := cursor.V5OrderTerm{
			Path: []string{"$" + key.Field}, Type: "timestamptz", Desc: key.Desc, Nulls: cursor.NullsLast,
		}
		switch key.Field {
		case systemFieldID:
			term.Type = "uuid"
		case systemFieldCreatedAt, systemFieldUpdatedAt:
		default:
			// v5 addresses a user field by its stable catalog id, never by the
			// logical name a request used.
			term.Path = []string{uuid.NewSHA1(uuid.Nil, []byte(key.Field)).String()}
			term.Type = "text"
		}
		order[index] = term
	}
	return &cursor.V5Descriptor{
		CatalogVersion: 12, MembershipGeneration: 1, HighWater: 7,
		QueryHash: bytes.Repeat([]byte{0x11}, sha256.Size),
		ScopeHash: bytes.Repeat([]byte{0x22}, sha256.Size),
		Order:     order,
	}
}

func pageOrderForTest(collectionID uuid.UUID, sortKeys []SortKey) orderSpec {
	terms := make([]orderTerm, len(sortKeys))
	for index := range sortKeys {
		terms[index].key = sortKeys[index]
	}
	return orderSpec{root: schema.Collection{ID: collectionID}, terms: terms}
}

type countingJSONWire struct {
	count *int
	value string
}

func (wire countingJSONWire) MarshalJSON() ([]byte, error) {
	(*wire.count)++
	return json.Marshal(map[string]string{"id": wire.value})
}
