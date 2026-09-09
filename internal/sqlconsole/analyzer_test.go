package sqlconsole

import (
	"errors"
	"testing"

	"github.com/google/uuid"
)

func TestAnalyzeReadSupportsBoundedQueryGrammar(t *testing.T) {
	t.Parallel()

	request := Request{
		ExecutionID: uuid.New(),
		Mode:        ModeRead,
		Statements: []Statement{{SQL: `
			WITH recent AS (
				SELECT account_id, amount FROM app.orders WHERE amount > $1
			)
			SELECT a.id, count(r.account_id), sum(r.amount)
			FROM app.accounts AS a
			LEFT JOIN recent AS r ON r.account_id = a.id
			WHERE EXISTS (SELECT 1 FROM app.flags AS f WHERE f.account_id = a.id)
			GROUP BY a.id ORDER BY a.id LIMIT 100
		`, Parameters: []Parameter{{Kind: ParameterDecimal, Text: "10.00"}}}},
	}
	plan, err := Analyze(request)
	if err != nil {
		t.Fatalf("Analyze: %v", err)
	}
	if plan.Writes {
		t.Fatal("read plan marked as write")
	}
	wantRelations := []RelationRef{{Schema: "app", Name: "accounts"}, {Schema: "app", Name: "flags"}, {Schema: "app", Name: "orders"}}
	if !sameRelations(plan.Statements[0].Relations, wantRelations) {
		t.Fatalf("relations = %#v", plan.Statements[0].Relations)
	}
	wantFunctions := []FunctionRef{{Name: "count"}, {Name: "sum"}}
	if !sameFunctions(plan.Statements[0].Functions, wantFunctions) {
		t.Fatalf("functions = %#v", plan.Statements[0].Functions)
	}
}

func TestAnalyzeWriteKeepsReadAndThreeDMLFormsAtomic(t *testing.T) {
	t.Parallel()

	request := Request{
		ExecutionID:    uuid.New(),
		Mode:           ModeWrite,
		IdempotencyKey: "write-attempt-000000000000000001",
		Statements: []Statement{
			{SQL: "SELECT count(*) FROM ext.items"},
			{SQL: "INSERT INTO ext.items (id, amount) VALUES ($1, $2) RETURNING amount", Parameters: []Parameter{{Kind: ParameterUUID, Text: "f36ff36c-490b-413f-b1c3-52e17cf19e34"}, {Kind: ParameterDecimal, Text: "1.25"}}},
			{SQL: "UPDATE ext.items SET amount = amount + $1 WHERE id = $2 RETURNING amount", Parameters: []Parameter{{Kind: ParameterDecimal, Text: "2.50"}, {Kind: ParameterUUID, Text: "f36ff36c-490b-413f-b1c3-52e17cf19e34"}}},
			{SQL: "DELETE FROM ext.items WHERE id = $1 RETURNING id", Parameters: []Parameter{{Kind: ParameterUUID, Text: "f36ff36c-490b-413f-b1c3-52e17cf19e34"}}},
		},
	}
	plan, err := Analyze(request)
	if err != nil {
		t.Fatalf("Analyze: %v", err)
	}
	if !plan.Writes {
		t.Fatal("write plan was not marked as write")
	}
	for index, kind := range []StatementKind{StatementSelect, StatementInsert, StatementUpdate, StatementDelete} {
		if plan.Statements[index].Kind != kind {
			t.Fatalf("statement %d kind = %q, want %q", index, plan.Statements[index].Kind, kind)
		}
	}
	for _, index := range []int{1, 2, 3} {
		if target := plan.Statements[index].WriteTarget; target == nil || *target != (RelationRef{Schema: "ext", Name: "items"}) {
			t.Fatalf("statement %d target = %#v", index, target)
		}
	}
	if got := plan.Statements[1].WriteColumns; !sameStrings(got, []string{"amount", "id"}) {
		t.Fatalf("INSERT write columns = %#v", got)
	}
	if got := plan.Statements[2].WriteColumns; !sameStrings(got, []string{"amount"}) {
		t.Fatalf("UPDATE write columns = %#v", got)
	}
	if got := plan.Statements[3].WriteColumns; len(got) != 0 {
		t.Fatalf("DELETE write columns = %#v", got)
	}
}

func TestAnalyzeRejectsEveryUnmodelledOrEffectfulShape(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		mode Mode
		sql  string
	}{
		{name: "multiple statements", mode: ModeRead, sql: "SELECT 1; SELECT 2"},
		{name: "DDL", mode: ModeWrite, sql: "CREATE TABLE ext.x(id int)"},
		{name: "COPY program", mode: ModeRead, sql: "COPY (SELECT 1) TO PROGRAM 'cat'"},
		{name: "DO", mode: ModeRead, sql: "DO $$BEGIN NULL; END$$"},
		{name: "select into", mode: ModeRead, sql: "SELECT 1 INTO ext.created"},
		{name: "row lock", mode: ModeRead, sql: "SELECT * FROM app.items FOR UPDATE"},
		{name: "DML CTE", mode: ModeWrite, sql: "WITH changed AS (UPDATE ext.items SET amount=1 RETURNING id) SELECT * FROM changed"},
		{name: "write CTE", mode: ModeWrite, sql: "WITH source AS (SELECT 1) UPDATE ext.items SET amount=1"},
		{name: "unqualified write", mode: ModeWrite, sql: "DELETE FROM items"},
		{name: "managed write", mode: ModeWrite, sql: "UPDATE app.items SET amount=1"},
		{name: "metadata write", mode: ModeWrite, sql: "DELETE FROM pgbase_meta.collections"},
		{name: "default values", mode: ModeWrite, sql: "INSERT INTO ext.items DEFAULT VALUES"},
		{name: "implicit insert columns", mode: ModeWrite, sql: "INSERT INTO ext.items VALUES (1)"},
		{name: "duplicate insert columns", mode: ModeWrite, sql: "INSERT INTO ext.items(id,id) VALUES (1,2)"},
		{name: "on conflict", mode: ModeWrite, sql: "INSERT INTO ext.items(id) VALUES (1) ON CONFLICT (id) DO NOTHING"},
		{name: "indirected update", mode: ModeWrite, sql: "UPDATE ext.items SET tags[1]='x'"},
		{name: "duplicate update columns", mode: ModeWrite, sql: "UPDATE ext.items SET amount=1,amount=2"},
		{name: "user function", mode: ModeRead, sql: "SELECT ext.steal()"},
		{name: "unknown function", mode: ModeRead, sql: "SELECT pg_catalog.lo_import('/tmp/x')"},
		{name: "unsafe cast", mode: ModeRead, sql: "SELECT 'x'::ext.secret_type"},
		{name: "function column definition", mode: ModeRead, sql: "SELECT * FROM pg_catalog.generate_series(1,2) AS x(v int)"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			request := Request{ExecutionID: uuid.New(), Mode: test.mode, Statements: []Statement{{SQL: test.sql}}}
			if test.mode == ModeWrite {
				request.IdempotencyKey = "write-attempt-000000000000000001"
			}
			_, err := Analyze(request)
			if !errors.Is(err, ErrSQLRejected) {
				t.Fatalf("error = %v, want ErrSQLRejected", err)
			}
		})
	}
}

func sameStrings(left, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index] != right[index] {
			return false
		}
	}
	return true
}

func TestAnalyzeRequiresExactBoundParameters(t *testing.T) {
	t.Parallel()

	tests := []Statement{
		{SQL: "SELECT $1"},
		{SQL: "SELECT 1", Parameters: []Parameter{{Kind: ParameterInteger, Text: "1"}}},
		{SQL: "SELECT $2", Parameters: []Parameter{{Kind: ParameterInteger, Text: "1"}, {Kind: ParameterInteger, Text: "2"}}},
	}
	for _, statement := range tests {
		_, err := Analyze(Request{ExecutionID: uuid.New(), Mode: ModeRead, Statements: []Statement{statement}})
		if !errors.Is(err, ErrSQLRejected) {
			t.Fatalf("statement %q error = %v", statement.SQL, err)
		}
	}

	_, err := Analyze(Request{ExecutionID: uuid.New(), Mode: ModeRead, Statements: []Statement{{
		SQL: "SELECT $1 + $1", Parameters: []Parameter{{Kind: ParameterInteger, Text: "1"}},
	}}})
	if err != nil {
		t.Fatalf("reused bound parameter: %v", err)
	}
}

func sameRelations(left, right []RelationRef) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index] != right[index] {
			return false
		}
	}
	return true
}

func sameFunctions(left, right []FunctionRef) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index] != right[index] {
			return false
		}
	}
	return true
}
