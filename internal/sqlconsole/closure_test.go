package sqlconsole

import (
	"bytes"
	"github.com/jackc/pgx/v5/pgtype"
	"testing"
)

func TestPostgresSpecialDecimalResultsRemainTaggedText(t *testing.T) {
	for _, oid := range []uint32{pgtype.NumericOID, pgtype.Float4OID, pgtype.Float8OID} {
		for _, value := range []string{"NaN", "Infinity", "-Infinity"} {
			cell, err := resultCell(pgtype.NewMap(), oid, []byte(value))
			if err != nil {
				t.Fatal(err)
			}
			encoded, err := EncodeCells([]Cell{cell})
			if err != nil || cell.Kind != CellDecimal || !bytes.Contains(encoded, []byte(`"text":"`+value+`"`)) {
				t.Fatalf("%d/%s encoded=%s err=%v", oid, value, encoded, err)
			}
			if err := validateParameter(Parameter{Kind: ParameterDecimal, Text: value}); err == nil {
				t.Fatalf("accepted nonfinite input %q", value)
			}
		}
	}
}
