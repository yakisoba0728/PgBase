package sqlconsole

import (
	"bytes"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestNormalizeRequestAppliesReadDefaultsAndHardLimits(t *testing.T) {
	t.Parallel()

	request := Request{
		ExecutionID: uuid.MustParse("5b698558-32fd-4556-9c65-4c28288fcecc"),
		Statements:  []Statement{{SQL: "SELECT $1", Parameters: []Parameter{{Kind: ParameterInteger, Text: "9007199254740993"}}}},
	}
	normalized, err := NormalizeRequest(request, false)
	if err != nil {
		t.Fatalf("NormalizeRequest: %v", err)
	}
	if normalized.Mode != ModeRead || normalized.Timeout != DefaultTimeout {
		t.Fatalf("defaults = mode %q timeout %s", normalized.Mode, normalized.Timeout)
	}
	if normalized.Statements[0].Parameters[0].Text != "9007199254740993" {
		t.Fatal("exact integer parameter changed")
	}

	tests := []struct {
		name    string
		request Request
		write   bool
		want    error
	}{
		{name: "missing execution id", request: Request{Statements: []Statement{{SQL: "SELECT 1"}}}, want: ErrInvalidRequest},
		{name: "no statements", request: Request{ExecutionID: uuid.New()}, want: ErrInvalidRequest},
		{name: "too many statements", request: Request{ExecutionID: uuid.New(), Statements: make([]Statement, MaximumStatements+1)}, want: ErrRequestTooLarge},
		{name: "invalid utf8", request: Request{ExecutionID: uuid.New(), Statements: []Statement{{SQL: string([]byte{0xff})}}}, want: ErrInvalidRequest},
		{name: "sql bytes", request: Request{ExecutionID: uuid.New(), Statements: []Statement{{SQL: "SELECT '" + strings.Repeat("x", MaximumSQLBytes) + "'"}}}, want: ErrRequestTooLarge},
		{name: "timeout", request: Request{ExecutionID: uuid.New(), Timeout: MaximumTimeout + time.Nanosecond, Statements: []Statement{{SQL: "SELECT 1"}}}, want: ErrInvalidRequest},
		{name: "read dry run", request: Request{ExecutionID: uuid.New(), DryRun: true, Statements: []Statement{{SQL: "SELECT 1"}}}, want: ErrInvalidRequest},
		{name: "write disabled", request: Request{ExecutionID: uuid.New(), Mode: ModeWrite, IdempotencyKey: strings.Repeat("k", 32), Statements: []Statement{{SQL: "DELETE FROM ext.items"}}}, want: ErrWriteDisabled},
		{name: "write key missing", request: Request{ExecutionID: uuid.New(), Mode: ModeWrite, Statements: []Statement{{SQL: "DELETE FROM ext.items"}}}, write: true, want: ErrInvalidRequest},
		{name: "bad parameter", request: Request{ExecutionID: uuid.New(), Statements: []Statement{{SQL: "SELECT $1", Parameters: []Parameter{{Kind: ParameterInteger, Text: "1.0"}}}}}, want: ErrInvalidRequest},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := NormalizeRequest(test.request, test.write)
			if !errors.Is(err, test.want) {
				t.Fatalf("error = %v, want %v", err, test.want)
			}
		})
	}
}

func TestRequestDigestIsStableAndDomainSeparated(t *testing.T) {
	t.Parallel()

	secret := bytes.Repeat([]byte{0x2a}, 32)
	base := Request{
		ExecutionID:    uuid.MustParse("f9821e88-77dd-4754-8daf-1c20c15b89f7"),
		Mode:           ModeWrite,
		DryRun:         true,
		Timeout:        7 * time.Second,
		IdempotencyKey: "write-attempt-000000000000000001",
		Statements: []Statement{{
			SQL:        "UPDATE ext.items SET amount=$1 WHERE id=$2 RETURNING amount",
			Parameters: []Parameter{{Kind: ParameterDecimal, Text: "9007199254740993.0001"}, {Kind: ParameterUUID, Text: "4c2f3398-7200-4ee8-9dbe-8f3bf7a860f3"}},
		}},
	}
	first := RequestDigest(secret, base)
	second := RequestDigest(secret, base)
	if first != second {
		t.Fatal("same request produced different digest")
	}
	changed := base
	changed.Statements = append([]Statement(nil), base.Statements...)
	changed.Statements[0].Parameters = append([]Parameter(nil), base.Statements[0].Parameters...)
	changed.Statements[0].Parameters[0].Text = "9007199254740993.0002"
	if first == RequestDigest(secret, changed) {
		t.Fatal("parameter change did not change digest")
	}
	if first == QueryFingerprint(secret, base.Statements) {
		t.Fatal("request digest and query fingerprint share a domain")
	}
}

func TestCellJSONKeepsNullAndExactValuesDistinct(t *testing.T) {
	t.Parallel()

	cells := []Cell{
		{Kind: CellNull},
		{Kind: CellString, Text: ""},
		{Kind: CellInteger, Text: "9007199254740993"},
		{Kind: CellDecimal, Text: "0.1000000000000000001"},
		{Kind: CellJSON, Text: `{"n":9007199254740993}`},
	}
	encoded, err := EncodeCells(cells)
	if err != nil {
		t.Fatalf("EncodeCells: %v", err)
	}
	want := `[{"kind":"null"},{"kind":"string","text":""},{"kind":"integer","text":"9007199254740993"},{"kind":"decimal","text":"0.1000000000000000001"},{"kind":"json","text":"{\"n\":9007199254740993}"}]`
	if string(encoded) != want {
		t.Fatalf("encoded cells = %s", encoded)
	}
}
