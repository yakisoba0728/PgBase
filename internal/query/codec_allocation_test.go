package query

import (
	"reflect"
	"sort"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/yakisoba0728/PgBase/internal/schema"
)

func TestRecordCodecKeepsColumnOrderWithoutMutatingCatalog(t *testing.T) {
	for _, fields := range [][]schema.Field{
		{
			{Name: "alpha", ColumnName: "alpha", Type: schema.TypeText, Ordinal: 0},
			{Name: "beta", ColumnName: "beta", Type: schema.TypeText, Ordinal: 0},
			{Name: "gamma", ColumnName: "gamma", Type: schema.TypeText, Ordinal: 4},
		},
		{
			{Name: "gamma", ColumnName: "gamma", Type: schema.TypeText, Ordinal: 4},
			{Name: "beta", ColumnName: "beta", Type: schema.TypeText, Ordinal: 0},
			{Name: "alpha", ColumnName: "alpha", Type: schema.TypeText, Ordinal: 0},
		},
	} {
		name := fields[0].Name
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			collection := schema.Collection{Fields: fields}
			before := append([]schema.Field(nil), fields...)
			id := uuid.MustParse("aaaaaaaa-bbbb-4ccc-8ddd-eeeeeeeeeeee")
			now := time.Date(2026, 11, 16, 0, 0, 0, 0, time.UTC)
			row := &fixtureRow{values: []any{
				pgtype.UUID{Bytes: id, Valid: true}, now, now,
				pgtype.Text{String: "first", Valid: true},
				pgtype.Text{String: "second", Valid: true},
				pgtype.Text{String: "third", Valid: true},
			}}
			for range 3 {
				record, err := EncodeRecord(collection, row)
				if err != nil {
					t.Fatal(err)
				}
				if record["alpha"] != "first" || record["beta"] != "second" || record["gamma"] != "third" {
					t.Fatalf("record columns were reordered: %#v", record)
				}
				if got := selectedColumns(collection); got != `"id", "created_at", "updated_at", "alpha", "beta", "gamma"` {
					t.Fatalf("selected columns = %q", got)
				}
			}
			if !reflect.DeepEqual(collection.Fields, before) {
				t.Fatalf("caller-owned catalog fields changed: %#v", collection.Fields)
			}
		})
	}
}

// The copy/sort reference is the original implementation. Keeping it in the
// benchmark permits paired measurement in one process without a database.
func BenchmarkFieldsByOrdinal20(b *testing.B) {
	collection, _ := benchmarkCollectionAndInput(20)
	for _, test := range []struct {
		name string
		read func(schema.Collection) []schema.Field
	}{
		{"copy_sort_reference", func(collection schema.Collection) []schema.Field {
			fields := append([]schema.Field(nil), collection.Fields...)
			sort.Slice(fields, func(left, right int) bool {
				if fields[left].Ordinal == fields[right].Ordinal {
					return fields[left].Name < fields[right].Name
				}
				return fields[left].Ordinal < fields[right].Ordinal
			})
			return fields
		}},
		{"current", fieldsByOrdinal},
	} {
		b.Run(test.name, func(b *testing.B) {
			b.ReportAllocs()
			for range b.N {
				if got := test.read(collection); len(got) != 20 || got[19].Name != "notes" {
					b.Fatal("invalid ordered fields")
				}
			}
		})
	}
}

// This benchmark isolates row conversion. Its fixture scanner excludes pgx
// network/decode work; use the integration benchmark for complete query costs.
func BenchmarkEncodeRecord20Fields(b *testing.B) {
	collection, _ := benchmarkCollectionAndInput(20)
	id := uuid.MustParse("aaaaaaaa-bbbb-4ccc-8ddd-eeeeeeeeeeee")
	now := time.Date(2026, 11, 16, 0, 0, 0, 0, time.UTC)
	row := &fixtureRow{values: []any{pgtype.UUID{Bytes: id, Valid: true}, now, now}}
	for _, field := range collection.Fields {
		var value any
		switch field.Type {
		case schema.TypeBoolean:
			value = pgtype.Bool{Bool: true, Valid: true}
		case schema.TypeInteger:
			value = pgtype.Int4{Int32: 42, Valid: true}
		case schema.TypeBigInt:
			value = pgtype.Int8{Int64: 9223372036854775000, Valid: true}
		case schema.TypeNumeric:
			value = pgtype.Text{String: "1234567890.123456789", Valid: true}
		case schema.TypeText:
			value = pgtype.Text{String: "representative benchmark record", Valid: true}
		case schema.TypeTimestampTZ:
			value = pgtype.Timestamptz{Time: now, Valid: true}
		case schema.TypeUUID:
			value = pgtype.UUID{Bytes: id, Valid: true}
		case schema.TypeJSONB:
			value = []byte(`{"source":"benchmark","seed":42}`)
		default:
			b.Fatalf("unsupported fixture field type %q", field.Type)
		}
		row.values = append(row.values, value)
	}
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		if record, err := EncodeRecord(collection, row); err != nil || len(record) != 23 {
			b.Fatalf("encode record: fields=%d err=%v", len(record), err)
		}
	}
}
