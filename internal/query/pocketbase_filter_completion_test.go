package query

import (
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/yakisoba0728/PgBase/internal/schema"
)

func TestPocketBaseAnyMatchOperatorsAreBoundedAndParameterized(t *testing.T) {
	collection := compilerCollection()
	for _, op := range []string{"?=", "?!=", "?~", "?!~", "?<", "?<=", "?>", "?>="} {
		field, value := "title", "'needle'"
		if strings.Contains(op, "<") || strings.Contains(op, ">") {
			field, value = "large_count", "2"
		}
		node, err := parseFilterRequest(field + " " + op + " " + value)
		if err != nil {
			t.Fatalf("%s parse: %v", op, err)
		}
		builder := &statementBuilder{}
		sql, err := compileFilter(collection, node, builder)
		if err != nil {
			t.Fatalf("%s compile: %v", op, err)
		}
		if strings.Contains(sql, "needle") {
			t.Fatalf("%s literal entered SQL: %s", op, sql)
		}
	}
}

func TestPocketBaseLiteralLeftAnyMatchOperatorsPreserveOperandSemantics(t *testing.T) {
	collection := compilerCollection()
	tests := []struct {
		filter      string
		wantOp      string
		literalLeft bool
		wantSQL     string
	}{
		{filter: `2 ?< large_count`, wantOp: `?>`, wantSQL: `("large_count" > $1)`},
		{filter: `2 ?<= large_count`, wantOp: `?>=`, wantSQL: `("large_count" >= $1)`},
		{filter: `2 ?> large_count`, wantOp: `?<`, wantSQL: `("large_count" < $1)`},
		{filter: `2 ?>= large_count`, wantOp: `?<=`, wantSQL: `("large_count" <= $1)`},
		{filter: `'needle' ?~ title`, wantOp: `?~`, literalLeft: true, wantSQL: `(strpos($1, "title") > 0)`},
		{filter: `'needle' ?!~ title`, wantOp: `?!~`, literalLeft: true, wantSQL: `(NOT (strpos($1, "title") > 0))`},
		{filter: `'needle' ?= title`, wantOp: `?=`, wantSQL: `("title" = $1)`},
		{filter: `'needle' ?!= title`, wantOp: `?!=`, wantSQL: `("title" != $1)`},
	}
	for _, test := range tests {
		t.Run(test.filter, func(t *testing.T) {
			node, err := parseFilterRequest(test.filter)
			if err != nil {
				t.Fatal(err)
			}
			comparison, ok := node.(filterComparison)
			if !ok {
				t.Fatalf("node = %T, want filterComparison", node)
			}
			if comparison.operator != test.wantOp || comparison.literalLeft != test.literalLeft {
				t.Fatalf("comparison = operator %q literalLeft %t, want %q %t", comparison.operator, comparison.literalLeft, test.wantOp, test.literalLeft)
			}
			builder := &statementBuilder{}
			sql, err := compileFilter(collection, node, builder)
			if err != nil {
				t.Fatal(err)
			}
			if sql != test.wantSQL {
				t.Fatalf("sql = %q, want %q", sql, test.wantSQL)
			}
			if len(builder.args) != 1 {
				t.Fatalf("args = %#v, want one bound literal", builder.args)
			}
		})
	}
}

func TestPocketBaseJSONPathAndTimeMacroCompile(t *testing.T) {
	collection := compilerCollection()
	collection.Fields = append(collection.Fields, schema.Field{ID: uuid.New(), Name: "payload", ColumnName: "payload", Type: schema.TypeJSONB})
	node, err := parseFilterRequest("payload.profile.score ?>= 2 && created_at < @tomorrow")
	if err != nil {
		t.Fatal(err)
	}
	plan, err := compileRelationListPlan(schema.Catalog{Collections: map[string]schema.Collection{collection.Name: collection}}, collection, node, nil)
	if err != nil {
		t.Fatal(err)
	}
	statement, err := plan.pageStatementWithPolicy(nil, 2, nil, ruleBinding{})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(statement.SQL, `"payload"#>>'{profile,score}'`) || strings.Contains(statement.SQL, "@tomorrow") {
		t.Fatalf("sql=%s", statement.SQL)
	}
}

func TestPocketBaseGeoDistanceAndStrftimeCompile(t *testing.T) {
	collection := compilerCollection()
	collection.Fields = append(collection.Fields,
		schema.Field{ID: uuid.New(), Name: "place", ColumnName: "place", Type: schema.TypeGeoPoint},
		schema.Field{ID: uuid.New(), Name: "published", ColumnName: "published", Type: schema.TypeTimestampTZ},
	)
	for _, source := range []string{"geoDistance(place.lon, place.lat, 127.0, 37.5) < 10", "strftime('%Y-%m', published) = '2026-09'"} {
		node, err := parseFilterRequest(source)
		if err != nil {
			t.Fatalf("%s: %v", source, err)
		}
		builder := &statementBuilder{}
		sql, err := compileFilter(collection, node, builder)
		if err != nil {
			t.Fatalf("%s: %v", source, err)
		}
		if strings.Contains(sql, "127.0") || strings.Contains(sql, "2026-09") {
			t.Fatalf("unbound literal: %s", sql)
		}
	}
}
