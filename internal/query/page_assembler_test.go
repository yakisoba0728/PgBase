package query

import (
	"encoding/json"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/webisnoteasy/PgBase/internal/schema"
)

func TestPageAssemblerMeasuresEachWireOnceAcrossGrowingPrefixes(t *testing.T) {
	t.Parallel()

	collectionID := uuid.MustParse("bbbbbbbb-cccc-4ddd-8eee-ffffffffffff")
	expiresAt := time.Date(2026, 9, 2, 12, 0, 0, 0, time.UTC)
	codec := NewCursorCodec([]byte("0123456789abcdef0123456789abcdef"))
	assembler := pageAssembler{
		cursors:      codec,
		collectionID: collectionID,
		sortKeys:     []SortKey{{Field: systemFieldID}},
		expiresAt:    expiresAt,
		maximumBytes: 1 << 20,
	}

	const measuredCandidates = 4
	marshalCounts := make([]int, measuredCandidates+1)
	candidates := make([]pageCandidate, len(marshalCounts))
	for index := range candidates {
		id := uuid.NewSHA1(collectionID, []byte{byte(index)})
		candidates[index] = pageCandidate{
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
		cursors:      NewCursorCodec([]byte("0123456789abcdef0123456789abcdef")),
		collectionID: collectionID, sortKeys: []SortKey{{Field: systemFieldID}},
		expiresAt: expiresAt, maximumBytes: 1 << 20,
	}
	candidates := make([]pageCandidate, 2)
	for index := range candidates {
		id := uuid.NewSHA1(collectionID, []byte{byte(index)})
		wire := json.RawMessage(`{}`)
		if index == 0 {
			wire = json.RawMessage(strconv.Quote(strings.Repeat("x", 1024)))
		}
		candidates[index] = pageCandidate{
			root: map[string]any{systemFieldID: id.String()}, wire: wire,
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
	assembler := pageAssembler{
		cursors:      NewCursorCodec([]byte("0123456789abcdef0123456789abcdef")),
		collectionID: collectionID, sortKeys: []SortKey{{Field: "rank"}},
		expiresAt: time.Date(2026, 9, 2, 12, 0, 0, 0, time.UTC), maximumBytes: 1 << 20,
	}
	longValue := json.RawMessage(strconv.Quote(strings.Repeat("a", schema.MaximumTextBytes)))
	shortValue := json.RawMessage(`"b"`)
	candidates := []pageCandidate{
		{
			root: map[string]any{systemFieldID: uuid.NewSHA1(collectionID, []byte{1}).String()},
			wire: json.RawMessage(strconv.Quote(strings.Repeat("x", 1024))), cursorValues: []json.RawMessage{longValue},
		},
		{
			root: map[string]any{systemFieldID: uuid.NewSHA1(collectionID, []byte{2}).String()},
			wire: json.RawMessage(`{}`), cursorValues: []json.RawMessage{shortValue},
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

type countingJSONWire struct {
	count *int
	value string
}

func (wire countingJSONWire) MarshalJSON() ([]byte, error) {
	(*wire.count)++
	return json.Marshal(map[string]string{"id": wire.value})
}
