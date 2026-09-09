package sqlconsole

import (
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/zkrebbekx/pgparse"
)

var ErrSQLRejected = errors.New("SQL console statement is not allowed")

type StatementKind string

const (
	StatementSelect StatementKind = "select"
	StatementInsert StatementKind = "insert"
	StatementUpdate StatementKind = "update"
	StatementDelete StatementKind = "delete"
)

type RelationRef struct {
	Schema string
	Name   string
}

type FunctionRef struct {
	Schema string
	Name   string
}

type AnalyzedStatement struct {
	Kind         StatementKind
	Relations    []RelationRef
	Functions    []FunctionRef
	Casts        []string
	Operators    []string
	WriteTarget  *RelationRef
	WriteColumns []string
}

type Plan struct {
	Statements []AnalyzedStatement
	Writes     bool
}

type Rejection struct {
	StatementIndex int
	Position       int
	Reason         string
}

func (rejection *Rejection) Error() string {
	if rejection.Position > 0 {
		return fmt.Sprintf("%v: statement %d at byte %d: %s", ErrSQLRejected, rejection.StatementIndex, rejection.Position, rejection.Reason)
	}
	return fmt.Sprintf("%v: statement %d: %s", ErrSQLRejected, rejection.StatementIndex, rejection.Reason)
}

func (rejection *Rejection) Unwrap() error { return ErrSQLRejected }

var blockedWriteSchemas = map[string]struct{}{
	"app":                {},
	"information_schema": {},
	"pg_catalog":         {},
	"pg_toast":           {},
	"pgbase_meta":        {},
	"pgbase_relation":    {},
}

var allowedFunctions = map[string]struct{}{
	"abs": {}, "array_agg": {}, "avg": {}, "bool_and": {}, "bool_or": {},
	"btrim": {}, "ceil": {}, "ceiling": {}, "char_length": {}, "coalesce": {},
	"count": {}, "date_trunc": {}, "floor": {}, "generate_series": {},
	"greatest": {}, "json_agg": {}, "jsonb_agg": {}, "least": {}, "length": {},
	"lower": {}, "ltrim": {}, "max": {}, "min": {}, "nullif": {},
	"octet_length": {}, "pg_sleep": {}, "round": {}, "rtrim": {}, "substring": {},
	"sum": {}, "trim": {}, "trunc": {}, "upper": {},
}

var allowedOperators = map[string]struct{}{
	"+": {}, "-": {}, "*": {}, "/": {}, "%": {}, "^": {},
	"=": {}, "!=": {}, "<>": {}, "<": {}, "<=": {}, ">": {}, ">=": {},
	"AND": {}, "OR": {}, "||": {}, "&": {}, "|": {}, "#": {}, "<<": {}, ">>": {},
	"->": {}, "->>": {}, "#>": {}, "#>>": {}, "@>": {}, "<@": {}, "?": {}, "?|": {}, "?&": {},
	"~": {}, "~*": {}, "!~": {}, "!~*": {},
}

var allowedUnaryOperators = map[string]struct{}{"+": {}, "-": {}, "NOT": {}, "~": {}}

var allowedCasts = map[string]struct{}{
	"bigint": {}, "bool": {}, "boolean": {}, "bytea": {}, "date": {}, "decimal": {},
	"float4": {}, "float8": {}, "inet": {}, "int2": {}, "int4": {}, "int8": {},
	"integer": {}, "json": {}, "jsonb": {}, "numeric": {}, "real": {}, "smallint": {},
	"text": {}, "time": {}, "timestamp": {}, "timestamptz": {}, "uuid": {}, "varchar": {},
}

func Analyze(request Request) (Plan, error) {
	normalized, err := NormalizeRequest(request, request.Mode == ModeWrite)
	if err != nil {
		return Plan{}, err
	}
	plan := Plan{Statements: make([]AnalyzedStatement, len(normalized.Statements))}
	for index, statement := range normalized.Statements {
		analyzed, err := analyzeStatement(index, normalized.Mode, statement)
		if err != nil {
			return Plan{}, err
		}
		plan.Statements[index] = analyzed
		plan.Writes = plan.Writes || analyzed.WriteTarget != nil
	}
	if normalized.Mode == ModeWrite && !plan.Writes {
		return Plan{}, reject(0, "write mode requires at least one INSERT, UPDATE, or DELETE")
	}
	return plan, nil
}

func analyzeStatement(index int, mode Mode, statement Statement) (AnalyzedStatement, error) {
	parsed, err := pgparse.ParseOne(statement.SQL)
	if err != nil {
		position := 0
		var syntax *pgparse.SyntaxError
		if errors.As(err, &syntax) {
			position = syntax.Pos + 1
		}
		return AnalyzedStatement{}, &Rejection{StatementIndex: index, Position: position, Reason: "statement does not match the supported PostgreSQL grammar"}
	}

	analyzed := AnalyzedStatement{}
	switch typed := parsed.(type) {
	case *pgparse.SelectStmt:
		analyzed.Kind = StatementSelect
		if err := validateSelect(typed); err != nil {
			return AnalyzedStatement{}, reject(index, err.Error())
		}
	case *pgparse.InsertStmt:
		if mode != ModeWrite {
			return AnalyzedStatement{}, reject(index, "INSERT requires write mode")
		}
		analyzed.Kind = StatementInsert
		target, err := validateInsert(typed)
		if err != nil {
			return AnalyzedStatement{}, reject(index, err.Error())
		}
		analyzed.WriteTarget = &target
		analyzed.WriteColumns = sortedStrings(typed.Columns)
	case *pgparse.UpdateStmt:
		if mode != ModeWrite {
			return AnalyzedStatement{}, reject(index, "UPDATE requires write mode")
		}
		analyzed.Kind = StatementUpdate
		target, err := validateUpdate(typed)
		if err != nil {
			return AnalyzedStatement{}, reject(index, err.Error())
		}
		analyzed.WriteTarget = &target
		columns := make([]string, 0, len(typed.Set))
		for _, assignment := range typed.Set {
			columns = append(columns, assignment.Column)
		}
		analyzed.WriteColumns = sortedStrings(columns)
	case *pgparse.DeleteStmt:
		if mode != ModeWrite {
			return AnalyzedStatement{}, reject(index, "DELETE requires write mode")
		}
		analyzed.Kind = StatementDelete
		target, err := validateDelete(typed)
		if err != nil {
			return AnalyzedStatement{}, reject(index, err.Error())
		}
		analyzed.WriteTarget = &target
	default:
		return AnalyzedStatement{}, reject(index, "statement type is not allowed")
	}

	collector := astCollector{ctes: collectCTENames(parsed), parameters: make(map[int]struct{})}
	pgparse.Walk(parsed, collector.visit)
	if collector.err != nil {
		return AnalyzedStatement{}, reject(index, collector.err.Error())
	}
	if err := collector.requireParameters(len(statement.Parameters)); err != nil {
		return AnalyzedStatement{}, reject(index, err.Error())
	}
	analyzed.Relations = sortedRelations(collector.relations)
	analyzed.Functions = sortedFunctions(collector.functions)
	analyzed.Casts = sortedKeys(collector.casts)
	analyzed.Operators = sortedKeys(collector.operators)
	return analyzed, nil
}

func validateSelect(statement *pgparse.SelectStmt) error {
	if statement.Into != nil {
		return errors.New("SELECT INTO is not allowed")
	}
	if len(statement.Locking) != 0 {
		return errors.New("row locking clauses are not allowed")
	}
	for _, cte := range statement.With {
		if cte.Aux != "" {
			return errors.New("unmodelled CTE SEARCH/CYCLE clauses are not allowed")
		}
		selectStatement, ok := cte.Stmt.(*pgparse.SelectStmt)
		if !ok {
			return errors.New("data-modifying CTEs are not allowed")
		}
		if err := validateSelect(selectStatement); err != nil {
			return err
		}
	}
	if statement.Left != nil {
		if err := validateSelect(statement.Left); err != nil {
			return err
		}
	}
	if statement.Right != nil {
		if err := validateSelect(statement.Right); err != nil {
			return err
		}
	}
	return nil
}

func validateInsert(statement *pgparse.InsertStmt) (RelationRef, error) {
	if len(statement.With) != 0 {
		return RelationRef{}, errors.New("write statements cannot have a WITH clause")
	}
	target, err := writeTarget(statement.Table)
	if err != nil {
		return RelationRef{}, err
	}
	if statement.DefaultValues || len(statement.Columns) == 0 {
		return RelationRef{}, errors.New("INSERT must name every target column and explicit values")
	}
	if hasDuplicateStrings(statement.Columns) {
		return RelationRef{}, errors.New("INSERT target columns must be unique")
	}
	if statement.OnConflict != nil {
		return RelationRef{}, errors.New("ON CONFLICT is not in the allowed write grammar")
	}
	if statement.Select != nil {
		if err := validateSelect(statement.Select); err != nil {
			return RelationRef{}, err
		}
	}
	return target, nil
}

func validateUpdate(statement *pgparse.UpdateStmt) (RelationRef, error) {
	if len(statement.With) != 0 {
		return RelationRef{}, errors.New("write statements cannot have a WITH clause")
	}
	target, err := writeTarget(statement.Table)
	if err != nil {
		return RelationRef{}, err
	}
	if len(statement.Set) == 0 {
		return RelationRef{}, errors.New("UPDATE must assign at least one column")
	}
	columns := make([]string, 0, len(statement.Set))
	for _, assignment := range statement.Set {
		if assignment.Column == "" || assignment.Target != nil || len(assignment.Columns) != 0 || len(assignment.Values) != 0 {
			return RelationRef{}, errors.New("UPDATE permits direct single-column assignments only")
		}
		columns = append(columns, assignment.Column)
	}
	if hasDuplicateStrings(columns) {
		return RelationRef{}, errors.New("UPDATE target columns must be unique")
	}
	return target, nil
}

func validateDelete(statement *pgparse.DeleteStmt) (RelationRef, error) {
	if len(statement.With) != 0 {
		return RelationRef{}, errors.New("write statements cannot have a WITH clause")
	}
	return writeTarget(statement.Table)
}

func writeTarget(table *pgparse.TableName) (RelationRef, error) {
	if table == nil || table.Schema == "" || table.Name == "" {
		return RelationRef{}, errors.New("write target must be schema-qualified")
	}
	schema := strings.ToLower(table.Schema)
	if _, blocked := blockedWriteSchemas[schema]; blocked || strings.HasPrefix(schema, "pg_temp_") {
		return RelationRef{}, errors.New("PgBase-managed and system schemas cannot be write targets")
	}
	return RelationRef{Schema: table.Schema, Name: table.Name}, nil
}

func collectCTENames(statement pgparse.Stmt) map[string]struct{} {
	result := make(map[string]struct{})
	pgparse.Walk(statement, func(node pgparse.Node) bool {
		if selectStatement, ok := node.(*pgparse.SelectStmt); ok {
			for _, cte := range selectStatement.With {
				result[strings.ToLower(cte.Name)] = struct{}{}
			}
		}
		return true
	})
	return result
}

type astCollector struct {
	ctes       map[string]struct{}
	relations  map[RelationRef]struct{}
	functions  map[FunctionRef]struct{}
	casts      map[string]struct{}
	operators  map[string]struct{}
	parameters map[int]struct{}
	err        error
}

func (collector *astCollector) visit(node pgparse.Node) bool {
	if collector.err != nil {
		return false
	}
	ensureMaps(collector)
	switch typed := node.(type) {
	case *pgparse.SelectStmt:
		collector.err = validateSelect(typed)
	case *pgparse.InsertStmt, *pgparse.UpdateStmt, *pgparse.DeleteStmt:
	case *pgparse.TableName:
		if typed.Schema == "" {
			if _, cte := collector.ctes[strings.ToLower(typed.Name)]; cte {
				return true
			}
			collector.err = errors.New("persistent relation references must be schema-qualified")
			return false
		}
		collector.relations[RelationRef{Schema: typed.Schema, Name: typed.Name}] = struct{}{}
	case *pgparse.SubqueryTable, *pgparse.JoinExpr:
	case *pgparse.FuncTable:
		if typed.ColumnsText != "" {
			collector.err = errors.New("set-returning function column definitions are not allowed")
		}
	case *pgparse.GroupingExpr, *pgparse.WindowDef:
	case *pgparse.ColumnRef, *pgparse.Star, *pgparse.Literal:
	case *pgparse.Param:
		if typed.Num <= 0 {
			collector.err = errors.New("parameter numbers start at one")
			return false
		}
		collector.parameters[typed.Num] = struct{}{}
	case *pgparse.BinaryExpr:
		operator := strings.ToUpper(typed.Op)
		if _, allowed := allowedOperators[operator]; !allowed {
			collector.err = fmt.Errorf("operator %q is not allowed", typed.Op)
			return false
		}
		collector.operators[operator] = struct{}{}
	case *pgparse.UnaryExpr:
		operator := strings.ToUpper(typed.Op)
		if _, allowed := allowedUnaryOperators[operator]; !allowed {
			collector.err = fmt.Errorf("unary operator %q is not allowed", typed.Op)
			return false
		}
		collector.operators[operator] = struct{}{}
	case *pgparse.FuncCall:
		name := strings.ToLower(typed.Name)
		schema := strings.ToLower(typed.Schema)
		if schema != "" && schema != "pg_catalog" {
			collector.err = errors.New("functions outside pg_catalog are not allowed")
			return false
		}
		if _, allowed := allowedFunctions[name]; !allowed {
			collector.err = fmt.Errorf("function %q is not allowed", typed.Name)
			return false
		}
		collector.functions[FunctionRef{Schema: schema, Name: name}] = struct{}{}
	case *pgparse.CastExpr:
		cast := normalizeCast(typed.Type)
		if _, allowed := allowedCasts[cast]; !allowed {
			collector.err = fmt.Errorf("cast to %q is not allowed", typed.Type)
			return false
		}
		collector.casts[cast] = struct{}{}
	case *pgparse.CaseExpr, *pgparse.InExpr, *pgparse.BetweenExpr, *pgparse.IsExpr,
		*pgparse.LikeExpr, *pgparse.SubqueryExpr, *pgparse.ExistsExpr, *pgparse.ParenExpr,
		*pgparse.SubscriptExpr, *pgparse.AnyAllExpr, *pgparse.ArrayExpr,
		*pgparse.IsDistinctExpr, *pgparse.RowExpr:
	case *pgparse.CollateExpr:
		collector.err = errors.New("explicit collations are not allowed")
	case *pgparse.FieldExpr:
		collector.err = errors.New("composite field selection is not allowed")
	default:
		collector.err = fmt.Errorf("AST node %T is not allowed", node)
	}
	return collector.err == nil
}

func ensureMaps(collector *astCollector) {
	if collector.relations == nil {
		collector.relations = make(map[RelationRef]struct{})
		collector.functions = make(map[FunctionRef]struct{})
		collector.casts = make(map[string]struct{})
		collector.operators = make(map[string]struct{})
	}
}

func (collector *astCollector) requireParameters(count int) error {
	if len(collector.parameters) != count {
		return errors.New("bound parameters must exactly match the placeholders")
	}
	for number := 1; number <= count; number++ {
		if _, present := collector.parameters[number]; !present {
			return errors.New("bound parameters must be contiguous from $1")
		}
	}
	return nil
}

func normalizeCast(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	if dot := strings.LastIndexByte(value, '.'); dot >= 0 {
		if value[:dot] != "pg_catalog" {
			return value
		}
		value = value[dot+1:]
	}
	if paren := strings.IndexByte(value, '('); paren >= 0 {
		value = value[:paren]
	}
	return value
}

func sortedRelations(values map[RelationRef]struct{}) []RelationRef {
	result := make([]RelationRef, 0, len(values))
	for value := range values {
		result = append(result, value)
	}
	sort.Slice(result, func(left, right int) bool {
		if result[left].Schema == result[right].Schema {
			return result[left].Name < result[right].Name
		}
		return result[left].Schema < result[right].Schema
	})
	return result
}

func sortedFunctions(values map[FunctionRef]struct{}) []FunctionRef {
	result := make([]FunctionRef, 0, len(values))
	for value := range values {
		result = append(result, value)
	}
	sort.Slice(result, func(left, right int) bool {
		if result[left].Schema == result[right].Schema {
			return result[left].Name < result[right].Name
		}
		return result[left].Schema < result[right].Schema
	})
	return result
}

func sortedKeys(values map[string]struct{}) []string {
	result := make([]string, 0, len(values))
	for value := range values {
		result = append(result, value)
	}
	sort.Strings(result)
	return result
}

func sortedStrings(values []string) []string {
	result := append([]string(nil), values...)
	sort.Strings(result)
	return result
}

func hasDuplicateStrings(values []string) bool {
	seen := make(map[string]struct{}, len(values))
	for _, value := range values {
		key := strings.ToLower(value)
		if _, exists := seen[key]; exists {
			return true
		}
		seen[key] = struct{}{}
	}
	return false
}

func reject(statement int, reason string) error {
	return &Rejection{StatementIndex: statement, Reason: reason}
}
