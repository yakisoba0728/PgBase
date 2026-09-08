//go:build integration

package recordops

import (
	"bytes"
	"context"
	"encoding/json"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/webisnoteasy/PgBase/internal/database"
	"github.com/webisnoteasy/PgBase/internal/schema"
)

var schemaBoundaryTimestamp = time.Date(2026, 9, 7, 16, 2, 3, 123456000, time.UTC)

// Schema changes use the same encoded-record bound as record writes. These
// cases exercise both growth shapes against records containing every scalar
// codec and a populated edge-table relation: exactly 2 MiB commits, while the
// first byte beyond it rejects and rolls the physical and catalog changes back.
func TestSchemaChangesHonorTheEncodedRecordSizeBoundary(t *testing.T) {
	for _, test := range []struct {
		name       string
		collection string
		change     string
	}{
		{name: "add nullable field", collection: "schema_add_null_boundary", change: "add_null"},
		{name: "integer to bigint", collection: "schema_integer_bigint_boundary", change: "integer_bigint"},
	} {
		t.Run(test.name, func(t *testing.T) {
			ctx := context.Background()
			pool, schemas, records, principal := newRecordOperationsTest(t)
			targets := []uuid.UUID{
				schemaBoundaryCreateTarget(t, ctx, records, principal, test.change+"-target-a"),
				schemaBoundaryCreateTarget(t, ctx, records, principal, test.change+"-target-b"),
			}
			beforeFields := schemaBoundaryFields()
			schemaBoundaryCreateCollection(t, ctx, schemas, principal.ID, test.collection, beforeFields)

			exactID := schemaBoundaryCreateRecord(t, ctx, records, principal, test.collection, targets, "exact")
			overID := schemaBoundaryCreateRecord(t, ctx, records, principal, test.collection, targets, "over")
			schemaBoundarySeedProjectedSize(t, ctx, pool, records, principal, test.collection, exactID,
				test.change, schema.MaximumRecordBytes)
			schemaBoundarySeedProjectedSize(t, ctx, pool, records, principal, test.collection, overID,
				test.change, schema.MaximumRecordBytes+1)

			afterFields := schemaBoundaryChangedFields(beforeFields, test.change)
			change := schema.NewUpdateChange(schema.UpdateCollectionInput{Name: test.collection, Fields: afterFields})
			plan, err := schemas.Preview(ctx, change)
			if err != nil {
				t.Fatal(err)
			}
			_, err = schemas.Apply(ctx, schema.LegacyApply(change, plan, principal.ID))
			assertQueryValidation(t, err, "record", "record_too_large")
			schemaBoundaryAssertRolledBack(t, ctx, pool, schemas, records, principal, test.collection,
				test.change, plan.BaseVersion, exactID, overID, targets)

			// Repair only the over-limit fixture to the exact boundary, then retry
			// the same public plan/apply flow. Both rows must now land on 2 MiB.
			schemaBoundarySeedProjectedSize(t, ctx, pool, records, principal, test.collection, overID,
				test.change, schema.MaximumRecordBytes)
			plan, err = schemas.Preview(ctx, change)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := schemas.Apply(ctx, schema.LegacyApply(change, plan, principal.ID)); err != nil {
				t.Fatalf("apply exact-boundary schema change: %v", err)
			}
			for _, id := range []uuid.UUID{exactID, overID} {
				record := schemaBoundaryGet(t, ctx, records, principal, test.collection, id)
				if size := schemaBoundaryRecordBytes(t, record.Data); size != schema.MaximumRecordBytes {
					t.Fatalf("accepted record size = %d, want %d", size, schema.MaximumRecordBytes)
				}
				schemaBoundaryAssertWireValues(t, record.Data, test.change, targets)
			}
			schemaBoundaryAssertPhysicalChange(t, ctx, pool, test.collection, test.change)
		})
	}
}

func schemaBoundaryFields() []schema.FieldInput {
	return []schema.FieldInput{
		{Name: "flag", Type: schema.TypeBoolean},
		{Name: "integer_to_bigint", Type: schema.TypeInteger},
		{Name: "big_value", Type: schema.TypeBigInt},
		{Name: "numeric_value", Type: schema.TypeNumeric},
		{Name: "text_value", Type: schema.TypeText},
		{Name: "happened_at", Type: schema.TypeTimestampTZ},
		{Name: "uuid_value", Type: schema.TypeUUID},
		{Name: "metadata", Type: schema.TypeJSONB},
		{Name: "links", Type: schema.TypeRelation, Relation: &schema.RelationInput{
			TargetCollection: "records", Cardinality: schema.RelationMultiple,
			MinSelect: relationInt(0), MaxSelect: relationInt(2), OnDelete: schema.RelationRestrict,
		}},
	}
}

func schemaBoundaryChangedFields(fields []schema.FieldInput, change string) []schema.FieldInput {
	changed := append([]schema.FieldInput(nil), fields...)
	switch change {
	case "add_null":
		return append(changed, schema.FieldInput{Name: "nullable_value", Type: schema.TypeText})
	case "integer_bigint":
		for index := range changed {
			if changed[index].Name == "integer_to_bigint" {
				changed[index].Type = schema.TypeBigInt
				return changed
			}
		}
	}
	panic("unknown schema boundary change")
}

func schemaBoundaryCreateCollection(
	t *testing.T,
	ctx context.Context,
	schemas *schema.Service,
	actor uuid.UUID,
	name string,
	fields []schema.FieldInput,
) {
	t.Helper()
	change := schema.NewCreateChange(schema.CreateCollectionInput{Name: name, Fields: fields})
	plan, err := schemas.Preview(ctx, change)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := schemas.Apply(ctx, schema.LegacyApply(change, plan, actor)); err != nil {
		t.Fatal(err)
	}
}

func schemaBoundaryCreateTarget(
	t *testing.T, ctx context.Context, records *Service, principal Principal, label string,
) uuid.UUID {
	t.Helper()
	encoded, _ := json.Marshal(label)
	result, err := records.Create(ctx, CreateRequest{
		Principal: principal, RequestID: "schema-boundary-" + label, Collection: "records",
		Input: map[string]json.RawMessage{"title": encoded},
	})
	if err != nil {
		t.Fatal(err)
	}
	return uuid.MustParse(result.Record.Data["id"].(string))
}

func schemaBoundaryCreateRecord(
	t *testing.T,
	ctx context.Context,
	records *Service,
	principal Principal,
	collection string,
	targets []uuid.UUID,
	label string,
) uuid.UUID {
	t.Helper()
	timestamp, _ := json.Marshal("2026-09-08T01:02:03.123456+09:00")
	identifier := uuid.MustParse("01999100-0000-7000-8000-000000000001")
	identifierJSON, _ := json.Marshal(identifier.String())
	result, err := records.Create(ctx, CreateRequest{
		Principal: principal, RequestID: "schema-boundary-" + label, Collection: collection,
		Input: map[string]json.RawMessage{
			"flag":              json.RawMessage("true"),
			"integer_to_bigint": json.RawMessage("2147483647"),
			"big_value":         json.RawMessage(`"9223372036854775807"`),
			"numeric_value":     json.RawMessage(`"12345678901234567890.12345"`),
			"text_value":        json.RawMessage(`"all scalar codecs"`),
			"happened_at":       timestamp,
			"uuid_value":        identifierJSON,
			"metadata":          schemaBoundaryMetadata(0),
			"links":             relationUUIDArrayJSON(targets...),
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	return uuid.MustParse(result.Record.Data["id"].(string))
}

func schemaBoundaryMetadata(fillerBytes int) json.RawMessage {
	encoded, _ := json.Marshal(struct {
		Filler string          `json:"filler"`
		HTML   string          `json:"html"`
		Number json.RawMessage `json:"number"`
	}{
		Filler: strings.Repeat("x", fillerBytes),
		HTML:   "<&>",
		Number: json.RawMessage("9007199254740993"),
	})
	return encoded
}

func schemaBoundarySeedProjectedSize(
	t *testing.T,
	ctx context.Context,
	pool *database.Pool,
	records *Service,
	principal Principal,
	collection string,
	id uuid.UUID,
	change string,
	projectedBytes int,
) {
	t.Helper()
	table := pgx.Identifier{"app", collection}.Sanitize()
	if _, err := pool.Exec(ctx, "update "+table+
		" set metadata = $2::jsonb, created_at = $3, updated_at = $3 where id = $1",
		id, string(schemaBoundaryMetadata(0)), schemaBoundaryTimestamp); err != nil {
		t.Fatal(err)
	}
	base := schemaBoundaryGet(t, ctx, records, principal, collection, id)
	baseBytes := schemaBoundaryRecordBytes(t, base.Data)
	projectionBytes := schemaBoundaryRecordBytes(t, schemaBoundaryProject(base.Data, change))
	growth := projectionBytes - baseBytes
	fillerBytes := projectedBytes - growth - baseBytes
	if fillerBytes < 0 {
		t.Fatalf("negative boundary filler: projected=%d growth=%d base=%d", projectedBytes, growth, baseBytes)
	}
	if _, err := pool.Exec(ctx, "update "+table+
		" set metadata = $2::jsonb, created_at = $3, updated_at = $3 where id = $1",
		id, string(schemaBoundaryMetadata(fillerBytes)), schemaBoundaryTimestamp); err != nil {
		t.Fatal(err)
	}
	seeded := schemaBoundaryGet(t, ctx, records, principal, collection, id)
	if size := schemaBoundaryRecordBytes(t, schemaBoundaryProject(seeded.Data, change)); size != projectedBytes {
		t.Fatalf("projected record size = %d, want %d", size, projectedBytes)
	}
}

func schemaBoundaryProject(record map[string]any, change string) map[string]any {
	projected := make(map[string]any, len(record)+1)
	for name, value := range record {
		projected[name] = value
	}
	switch change {
	case "add_null":
		projected["nullable_value"] = nil
	case "integer_bigint":
		projected["integer_to_bigint"] = strconv.FormatInt(int64(record["integer_to_bigint"].(int32)), 10)
	default:
		panic("unknown schema boundary change")
	}
	return projected
}

func schemaBoundaryGet(
	t *testing.T, ctx context.Context, records *Service, principal Principal, collection string, id uuid.UUID,
) Record {
	t.Helper()
	record, err := records.Get(ctx, GetRequest{
		Principal: principal, RequestID: "schema-boundary-get", Collection: collection, ID: id,
	})
	if err != nil {
		t.Fatal(err)
	}
	return record
}

func schemaBoundaryRecordBytes(t *testing.T, record map[string]any) int {
	t.Helper()
	encoded, err := json.Marshal(record)
	if err != nil {
		t.Fatal(err)
	}
	return len(encoded)
}

func schemaBoundaryAssertRolledBack(
	t *testing.T,
	ctx context.Context,
	pool *database.Pool,
	schemas *schema.Service,
	records *Service,
	principal Principal,
	collection string,
	change string,
	baseVersion int64,
	exactID, overID uuid.UUID,
	targets []uuid.UUID,
) {
	t.Helper()
	catalog, err := schemas.Catalog(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if catalog.Version != baseVersion {
		t.Fatalf("refused apply catalog version = %d, want %d", catalog.Version, baseVersion)
	}
	collectionState, exists := catalog.Collections[collection]
	if !exists {
		t.Fatalf("refused apply removed collection %q", collection)
	}
	for _, field := range collectionState.Fields {
		if field.Name == "nullable_value" {
			t.Fatal("refused nullable field survived in the catalog")
		}
		if field.Name == "integer_to_bigint" && field.Type != schema.TypeInteger {
			t.Fatalf("refused type change survived in the catalog as %q", field.Type)
		}
	}
	physicalType := schemaBoundaryPhysicalType(t, ctx, pool, collection, change)
	if change == "add_null" && physicalType != "" {
		t.Fatalf("refused nullable column survived rollback with type %q", physicalType)
	}
	if change == "integer_bigint" && physicalType != "integer" {
		t.Fatalf("refused type change survived rollback as %q", physicalType)
	}
	for _, id := range []uuid.UUID{exactID, overID} {
		record := schemaBoundaryGet(t, ctx, records, principal, collection, id)
		if _, exists := record.Data["nullable_value"]; exists {
			t.Fatal("refused nullable field survived rollback")
		}
		if _, ok := record.Data["integer_to_bigint"].(int32); !ok {
			t.Fatalf("refused type change altered record value: %#v", record.Data["integer_to_bigint"])
		}
		schemaBoundaryAssertRelation(t, record.Data, targets)
	}
}

func schemaBoundaryAssertPhysicalChange(
	t *testing.T, ctx context.Context, pool *database.Pool, collection, change string,
) {
	t.Helper()
	physicalType := schemaBoundaryPhysicalType(t, ctx, pool, collection, change)
	if change == "add_null" && physicalType != "text" {
		t.Fatalf("accepted nullable column type = %q, want text", physicalType)
	}
	if change == "integer_bigint" && physicalType != "bigint" {
		t.Fatalf("accepted changed column type = %q, want bigint", physicalType)
	}
}

func schemaBoundaryPhysicalType(
	t *testing.T, ctx context.Context, pool *database.Pool, collection, change string,
) string {
	t.Helper()
	column := "nullable_value"
	if change == "integer_bigint" {
		column = "integer_to_bigint"
	}
	var dataType string
	err := pool.QueryRow(ctx, `select coalesce((select data_type from information_schema.columns
		where table_schema = 'app' and table_name = $1 and column_name = $2), '')`,
		collection, column).Scan(&dataType)
	if err != nil {
		t.Fatal(err)
	}
	return dataType
}

func schemaBoundaryAssertWireValues(t *testing.T, record map[string]any, change string, targets []uuid.UUID) {
	t.Helper()
	identifier := "01999100-0000-7000-8000-000000000001"
	if record["flag"] != true || record["text_value"] != "all scalar codecs" ||
		record["uuid_value"] != identifier {
		t.Fatalf("boolean/text/UUID wire values = %#v/%#v/%#v",
			record["flag"], record["text_value"], record["uuid_value"])
	}
	if record["big_value"] != "9223372036854775807" ||
		record["numeric_value"] != "12345678901234567890.12345" {
		t.Fatalf("bigint/numeric wire values = %#v/%#v", record["big_value"], record["numeric_value"])
	}
	if record["happened_at"] != schemaBoundaryTimestamp.Format(time.RFC3339Nano) {
		t.Fatalf("timestamp wire value = %#v", record["happened_at"])
	}
	if change == "add_null" {
		if value, exists := record["nullable_value"]; !exists || value != nil {
			t.Fatalf("nullable field = %#v, exists=%t", value, exists)
		}
		if record["integer_to_bigint"] != int32(2147483647) {
			t.Fatalf("integer wire value = %#v", record["integer_to_bigint"])
		}
	} else if record["integer_to_bigint"] != "2147483647" {
		t.Fatalf("changed bigint wire value = %#v", record["integer_to_bigint"])
	}
	encoded, err := json.Marshal(record)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(encoded, []byte("9007199254740993")) ||
		!bytes.Contains(encoded, []byte(`\u003c\u0026\u003e`)) {
		t.Fatalf("JSONB precision/HTML escaping missing from encoded record")
	}
	schemaBoundaryAssertRelation(t, record, targets)
}

func schemaBoundaryAssertRelation(t *testing.T, record map[string]any, targets []uuid.UUID) {
	t.Helper()
	want := []string{targets[0].String(), targets[1].String()}
	if !reflect.DeepEqual(record["links"], want) {
		t.Fatalf("multiple relation wire value = %#v, want %#v", record["links"], want)
	}
}
