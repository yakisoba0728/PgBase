package schema

import (
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/zkrebbekx/pgparse"
)

func planCompoundIndexes(collection string, inputs []IndexInput, fields []PlannedField) ([]PlannedIndex, error) {
	available := make(map[string]PlannedField, len(fields))
	for _, field := range fields {
		available[field.Name] = field
	}
	seen := make(map[string]struct{}, len(inputs))
	seenDefinitions := make(map[string]struct{}, len(inputs))
	result := make([]PlannedIndex, 0, len(inputs))
	for i, input := range inputs {
		path := fmt.Sprintf("indexes.%d", i)
		if err := validateInputName(path+".name", input.Name); err != nil {
			return nil, err
		}
		if _, ok := seen[input.Name]; ok {
			return nil, validation(path+".name", "duplicate_name")
		}
		seen[input.Name] = struct{}{}
		planned, signature, err := normalizeIndexInput(collection, input, available, path)
		if err != nil {
			return nil, err
		}
		if _, ok := seenDefinitions[signature]; ok {
			return nil, validation(path, "duplicate_index")
		}
		seenDefinitions[signature] = struct{}{}
		result = append(result, planned)
	}
	return result, nil
}

func normalizeIndexInput(collection string, input IndexInput, fields map[string]PlannedField, path string) (PlannedIndex, string, error) {
	shapes := 0
	if len(input.Fields) != 0 {
		shapes++
	}
	if len(input.Keys) != 0 {
		shapes++
	}
	if strings.TrimSpace(input.RawSQL) != "" {
		shapes++
	}
	if shapes != 1 {
		return PlannedIndex{}, "", validation(path, "invalid_shape")
	}
	if len(input.Fields) != 0 {
		if input.Where != "" || len(input.Fields) < 2 || len(input.Fields) > 8 {
			return PlannedIndex{}, "", validation(path+".fields", "invalid_count")
		}
		deps, err := validateIndexFields(input.Fields, fields, path+".fields")
		if err != nil {
			return PlannedIndex{}, "", err
		}
		planned := PlannedIndex{Name: input.Name, Fields: deps, Unique: input.Unique}
		return planned, indexDefinitionSignature(planned), nil
	}
	if len(input.Keys) != 0 {
		if len(input.Keys) > 8 {
			return PlannedIndex{}, "", validation(path+".keys", "invalid_count")
		}
		keys := append([]IndexKeyInput(nil), input.Keys...)
		dependencies := []string{}
		for i := range keys {
			keyPath := fmt.Sprintf("%s.keys.%d", path, i)
			if (keys[i].Field == "") == (strings.TrimSpace(keys[i].Expression) == "") {
				return PlannedIndex{}, "", validation(keyPath, "invalid_shape")
			}
			if keys[i].Order == "" {
				keys[i].Order = "asc"
			}
			if keys[i].Order != "asc" && keys[i].Order != "desc" {
				return PlannedIndex{}, "", validation(keyPath+".order", "invalid")
			}
			if !validIndexCollation(keys[i].Collation) {
				return PlannedIndex{}, "", validation(keyPath+".collation", "invalid")
			}
			if keys[i].Field != "" {
				deps, err := validateIndexFields([]string{keys[i].Field}, fields, keyPath+".field")
				if err != nil {
					return PlannedIndex{}, "", err
				}
				dependencies = appendUniqueStrings(dependencies, deps...)
				continue
			}
			expr, deps, err := parseIndexExpression(keys[i].Expression, fields, false)
			if err != nil {
				return PlannedIndex{}, "", validation(keyPath+".expression", "invalid_expression")
			}
			keys[i].Expression = expr
			dependencies = appendUniqueStrings(dependencies, deps...)
		}
		where := strings.TrimSpace(input.Where)
		if where != "" {
			canonical, deps, err := parseIndexPredicate(where, fields)
			if err != nil {
				return PlannedIndex{}, "", validation(path+".where", "invalid_predicate")
			}
			where = canonical
			dependencies = appendUniqueStrings(dependencies, deps...)
		}
		planned := PlannedIndex{Name: input.Name, Fields: dependencies, Unique: input.Unique, Keys: keys, Where: where}
		return planned, indexDefinitionSignature(planned), nil
	}
	if input.Where != "" {
		return PlannedIndex{}, "", validation(path+".where", "invalid_shape")
	}
	statement, ok := parseRawIndex(input.RawSQL)
	if !ok || statement.Name != input.Name || statement.Table == nil || statement.Table.Name != collection ||
		(statement.Table.Schema != "" && statement.Table.Schema != "app") || statement.IfNotExists ||
		(statement.Using != "" && !strings.EqualFold(statement.Using, "btree")) || statement.Unique != input.Unique ||
		len(statement.Columns) < 1 || len(statement.Columns) > 8 {
		return PlannedIndex{}, "", validation(path+".rawSql", "invalid_index_sql")
	}
	dependencies, err := validateParsedIndex(statement, fields)
	if err != nil {
		return PlannedIndex{}, "", validation(path+".rawSql", "unsafe_index_sql")
	}
	statement.Table.Schema = "app"
	statement.Using = ""
	canonical := deparseRawIndex(statement)
	planned := PlannedIndex{Name: input.Name, Fields: dependencies, Unique: input.Unique, RawSQL: canonical}
	return planned, indexDefinitionSignature(planned), nil
}

func validateIndexFields(names []string, fields map[string]PlannedField, path string) ([]string, error) {
	seen := map[string]bool{}
	result := make([]string, 0, len(names))
	for i, name := range names {
		field, ok := fields[name]
		if !ok {
			return nil, validation(fmt.Sprintf("%s.%d", path, i), "unknown_field")
		}
		if !indexableField(field) {
			return nil, validation(fmt.Sprintf("%s.%d", path, i), "unsupported_field")
		}
		if seen[name] {
			return nil, validation(fmt.Sprintf("%s.%d", path, i), "duplicate_name")
		}
		seen[name] = true
		result = append(result, name)
	}
	return result, nil
}

func indexableField(field PlannedField) bool {
	switch field.Type {
	case TypeFile, TypeRelation, TypeJSONB, TypeGeoPoint, TypePassword:
		return false
	default:
		return true
	}
}

func plannedFieldsByName(fields []Field) map[string]PlannedField {
	result := make(map[string]PlannedField, len(fields))
	for _, field := range fields {
		result[field.Name] = PlannedField{Name: field.Name, ColumnName: field.ColumnName, Type: field.Type}
	}
	return result
}

func validIndexCollation(value string) bool {
	return value == "" || value == "C" || value == "POSIX"
}

func parseRawIndex(raw string) (*pgparse.CreateIndexStmt, bool) {
	if len(raw) == 0 || len(raw) > 64<<10 {
		return nil, false
	}
	trimmed := strings.TrimSpace(raw)
	if !supportedRawIndexPrefix(trimmed) {
		return nil, false
	}
	parsed, err := pgparse.Parse(trimmed)
	if err != nil || len(parsed.Stmts) != 1 {
		return nil, false
	}
	statement, ok := parsed.Stmts[0].(*pgparse.CreateIndexStmt)
	return statement, ok
}

// supportedRawIndexPrefix closes the gap between pgparse's best-effort CREATE
// parser and the index AST we persist. pgparse deliberately consumes some
// CREATE modifiers without representing them, so accepting the AST alone can
// silently change the requested DDL when it is deparsed. Token positions keep
// comments and quoted occurrences from being mistaken for syntax.
func supportedRawIndexPrefix(raw string) bool {
	tokens, err := pgparse.Tokenize(raw)
	if err != nil || len(tokens) < 4 || !rawIndexWord(tokens[0], "create") {
		return false
	}
	position := 1
	if rawIndexWord(tokens[position], "unique") {
		position++
	}
	if position >= len(tokens) || !rawIndexWord(tokens[position], "index") {
		return false
	}
	position++
	return position < len(tokens) && !rawIndexWord(tokens[position], "concurrently")
}

func rawIndexWord(token pgparse.Token, value string) bool {
	return (token.Type == pgparse.TokenIdent || token.Type == pgparse.TokenKeyword) &&
		strings.EqualFold(token.Val, value)
}

// deparseRawIndex quotes the logical index name explicitly. pgparse correctly
// remembers the decoded identifier but its generic identifier renderer does
// not know CREATE INDEX's contextual CONCURRENTLY and IF words; leaving either
// unquoted would make the durable canonical SQL impossible to parse again.
func deparseRawIndex(statement *pgparse.CreateIndexStmt) string {
	if statement.Name == "" {
		return pgparse.Deparse(statement)
	}
	const placeholder = "pgbase_raw_index_name_placeholder"
	cloned := *statement
	cloned.Name = placeholder
	rendered := pgparse.Deparse(&cloned)
	return strings.Replace(rendered, placeholder, pgx.Identifier{statement.Name}.Sanitize(), 1)
}

func parseIndexExpression(raw string, fields map[string]PlannedField, predicate bool) (string, []string, error) {
	statement, ok := parseRawIndex("CREATE INDEX expression_probe ON expression_table (" + strings.TrimSpace(raw) + ")")
	if !ok || len(statement.Columns) != 1 || statement.Where != nil {
		return "", nil, ErrConflict
	}
	dependencies := []string{}
	if err := validateSafeIndexExpr(statement.Columns[0].Expr, fields, predicate, &dependencies); err != nil {
		return "", nil, err
	}
	return pgparse.Deparse(statement.Columns[0].Expr), dependencies, nil
}

func parseIndexPredicate(raw string, fields map[string]PlannedField) (string, []string, error) {
	statement, ok := parseRawIndex("CREATE INDEX predicate_probe ON predicate_table (predicate_key) WHERE " + strings.TrimSpace(raw))
	if !ok || statement.Where == nil {
		return "", nil, ErrConflict
	}
	dependencies := []string{}
	if err := validateSafeIndexExpr(statement.Where, fields, true, &dependencies); err != nil {
		return "", nil, err
	}
	return pgparse.Deparse(statement.Where), dependencies, nil
}

func validateParsedIndex(statement *pgparse.CreateIndexStmt, fields map[string]PlannedField) ([]string, error) {
	dependencies := []string{}
	for _, key := range statement.Columns {
		if err := validateSafeIndexExpr(key.Expr, fields, false, &dependencies); err != nil {
			return nil, err
		}
	}
	if statement.Where != nil {
		if err := validateSafeIndexExpr(statement.Where, fields, true, &dependencies); err != nil {
			return nil, err
		}
	}
	return dependencies, nil
}

func validateSafeIndexExpr(expr pgparse.Expr, fields map[string]PlannedField, predicate bool, dependencies *[]string) error {
	switch node := expr.(type) {
	case *pgparse.ColumnRef:
		if len(node.Parts) != 1 {
			return ErrConflict
		}
		field, ok := fields[node.Parts[0]]
		if !ok || !indexableField(field) {
			return ErrConflict
		}
		*dependencies = appendUniqueStrings(*dependencies, node.Parts[0])
		return nil
	case *pgparse.Literal:
		return nil
	case *pgparse.ParenExpr:
		return validateSafeIndexExpr(node.Expr, fields, predicate, dependencies)
	case *pgparse.CollateExpr:
		collation := strings.Trim(node.Collation, `"`)
		if !validIndexCollation(collation) || collation == "" {
			return ErrConflict
		}
		return validateSafeIndexExpr(node.Expr, fields, predicate, dependencies)
	case *pgparse.FuncCall:
		if node.Schema != "" || node.Distinct || node.Star || len(node.OrderBy) != 0 || node.Filter != nil || len(node.WithinGroup) != 0 || node.Over != nil || len(node.Args) != 1 {
			return ErrConflict
		}
		switch strings.ToLower(node.Name) {
		case "lower", "upper", "length", "btrim", "abs":
		default:
			return ErrConflict
		}
		return validateSafeIndexExpr(node.Args[0], fields, false, dependencies)
	case *pgparse.BinaryExpr:
		allowed := map[string]bool{"AND": true, "OR": true, "=": true, "<>": true, "<": true, "<=": true, ">": true, ">=": true}
		if !predicate {
			allowed = map[string]bool{"+": true, "-": true, "*": true, "/": true, "||": true}
		}
		if !allowed[strings.ToUpper(node.Op)] {
			return ErrConflict
		}
		if err := validateSafeIndexExpr(node.Left, fields, predicate, dependencies); err != nil {
			return err
		}
		return validateSafeIndexExpr(node.Right, fields, predicate, dependencies)
	case *pgparse.UnaryExpr:
		if (predicate && strings.ToUpper(node.Op) != "NOT") || (!predicate && node.Op != "+" && node.Op != "-") {
			return ErrConflict
		}
		return validateSafeIndexExpr(node.Operand, fields, predicate, dependencies)
	case *pgparse.IsExpr:
		if !predicate {
			return ErrConflict
		}
		return validateSafeIndexExpr(node.Expr, fields, true, dependencies)
	default:
		return ErrConflict
	}
}

func appendUniqueStrings(values []string, additions ...string) []string {
	for _, addition := range additions {
		found := false
		for _, value := range values {
			if value == addition {
				found = true
				break
			}
		}
		if !found {
			values = append(values, addition)
		}
	}
	return values
}

func indexDefinitionSignature(index PlannedIndex) string {
	return fmt.Sprintf("%t\x00%s\x00%v\x00%s\x00%s", index.Unique, strings.Join(index.Fields, "\x00"), index.Keys, index.Where, index.RawSQL)
}

func compoundIndexName(collection, name string) string {
	return indexName(collection, "compound:"+name)
}

func createCompoundIndexStatement(collection, table string, index PlannedIndex, fields []PlannedField) string {
	physical := make(map[string]string, len(fields))
	for _, field := range fields {
		physical[field.Name] = field.ColumnName
	}
	if index.RawSQL != "" {
		statement, ok := parseRawIndex(index.RawSQL)
		if !ok {
			panic("validated raw index became invalid")
		}
		rewriteIndexStatement(statement, physical)
		statement.Name = compoundIndexName(collection, index.Name)
		statement.Table = &pgparse.TableName{Schema: "app", Name: table}
		return deparseRawIndex(statement)
	}
	prefix := "CREATE INDEX "
	if index.Unique {
		prefix = "CREATE UNIQUE INDEX "
	}
	keys := make([]string, 0, len(index.Fields)+len(index.Keys))
	if len(index.Keys) == 0 {
		for _, field := range index.Fields {
			keys = append(keys, pgx.Identifier{physical[field]}.Sanitize())
		}
	} else {
		for _, key := range index.Keys {
			value := pgx.Identifier{physical[key.Field]}.Sanitize()
			if key.Expression != "" {
				statement, _ := parseRawIndex("CREATE INDEX expression_probe ON expression_table (" + key.Expression + ")")
				rewriteIndexStatement(statement, physical)
				value = pgparse.Deparse(statement.Columns[0].Expr)
			}
			if key.Collation != "" {
				value += " COLLATE " + pgx.Identifier{key.Collation}.Sanitize()
			}
			if key.Order == "desc" {
				value += " DESC"
			}
			keys = append(keys, value)
		}
	}
	statement := prefix + pgx.Identifier{compoundIndexName(collection, index.Name)}.Sanitize() + " ON " + pgx.Identifier{"app", table}.Sanitize() + " (" + strings.Join(keys, ", ") + ")"
	if index.Where != "" {
		predicate, _ := parseRawIndex("CREATE INDEX predicate_probe ON predicate_table (predicate_key) WHERE " + index.Where)
		rewriteIndexStatement(predicate, physical)
		statement += " WHERE " + pgparse.Deparse(predicate.Where)
	}
	return statement
}

func collectionIndexPhysicalSpec(collection Collection, index Index, physical map[string]string) (IndexSpec, error) {
	planned := PlannedIndex{Name: index.Name, Fields: index.Fields, Unique: index.Unique, Keys: index.Keys, Where: index.Where, RawSQL: index.RawSQL}
	fields := make([]PlannedField, 0, len(collection.Fields))
	for _, field := range collection.Fields {
		fields = append(fields, PlannedField{Name: field.Name, ColumnName: field.ColumnName, Type: field.Type})
	}
	statement, ok := parseRawIndex(createCompoundIndexStatement(collection.Name, collection.TableName, planned, fields))
	if !ok {
		return IndexSpec{}, ErrConflict
	}
	spec := IndexSpec{Namespace: "app", Table: collection.TableName, Name: index.IndexName, Method: "btree", Unique: index.Unique, Valid: true, Ready: true, Live: true}
	expressions := []string{}
	for position, column := range statement.Columns {
		key := IndexKeySpec{}
		if len(index.Keys) != 0 && index.Keys[position].Field != "" {
			key.Column = physical[index.Keys[position].Field]
		} else if len(index.Keys) == 0 && index.RawSQL == "" {
			key.Column = physical[index.Fields[position]]
		} else if name, direct := directIndexColumn(column.Expr); direct {
			key.Column = name
		} else {
			expressions = append(expressions, pgparse.Deparse(column.Expr))
		}
		key.Descending = column.Desc
		key.NullsFirst = key.Descending
		spec.Keys = append(spec.Keys, key)
	}
	if statement.Where != nil {
		spec.PredicateSQL = pgparse.Deparse(statement.Where)
	}
	spec.ExpressionSQL = strings.Join(expressions, ", ")
	return spec, nil
}

func directIndexColumn(expr pgparse.Expr) (string, bool) {
	switch value := expr.(type) {
	case *pgparse.ColumnRef:
		if len(value.Parts) == 1 {
			return value.Parts[0], true
		}
	case *pgparse.CollateExpr:
		return directIndexColumn(value.Expr)
	}
	return "", false
}

func rewriteIndexStatement(statement *pgparse.CreateIndexStmt, physical map[string]string) {
	for _, key := range statement.Columns {
		rewriteIndexExpr(key.Expr, physical)
	}
	if statement.Where != nil {
		rewriteIndexExpr(statement.Where, physical)
	}
}

func rewriteIndexExpr(expr pgparse.Expr, physical map[string]string) {
	switch node := expr.(type) {
	case *pgparse.ColumnRef:
		if len(node.Parts) == 1 && physical[node.Parts[0]] != "" {
			node.Parts[0] = physical[node.Parts[0]]
		}
	case *pgparse.ParenExpr:
		rewriteIndexExpr(node.Expr, physical)
	case *pgparse.CollateExpr:
		rewriteIndexExpr(node.Expr, physical)
	case *pgparse.FuncCall:
		for _, arg := range node.Args {
			rewriteIndexExpr(arg, physical)
		}
	case *pgparse.BinaryExpr:
		rewriteIndexExpr(node.Left, physical)
		rewriteIndexExpr(node.Right, physical)
	case *pgparse.UnaryExpr:
		rewriteIndexExpr(node.Operand, physical)
	case *pgparse.IsExpr:
		rewriteIndexExpr(node.Expr, physical)
	}
}

func compoundIndexChangeStatements(collection, table string, current []Index, desired []PlannedIndex, fields []PlannedField) []string {
	old := map[string]Index{}
	next := map[string]PlannedIndex{}
	for _, index := range current {
		old[index.Name] = index
	}
	for _, index := range desired {
		next[index.Name] = index
	}
	statements := []string{}
	for _, index := range current {
		desiredIndex, ok := next[index.Name]
		if !ok || !sameIndexDefinition(index, desiredIndex) {
			statements = append(statements, "DROP INDEX IF EXISTS "+pgx.Identifier{"app", index.IndexName}.Sanitize())
		}
	}
	for _, index := range desired {
		currentIndex, ok := old[index.Name]
		if !ok || !sameIndexDefinition(currentIndex, index) {
			statements = append(statements, createCompoundIndexStatement(collection, table, index, fields))
		}
	}
	return statements
}

func sameIndexDefinition(current Index, desired PlannedIndex) bool {
	return current.Unique == desired.Unique && stringSlicesEqual(current.Fields, desired.Fields) &&
		fmt.Sprint(current.Keys) == fmt.Sprint(desired.Keys) && current.Where == desired.Where && current.RawSQL == desired.RawSQL
}

func indexInputFromCatalog(index Index) IndexInput {
	input := IndexInput{
		Name: index.Name, Unique: index.Unique,
		Keys: append([]IndexKeyInput(nil), index.Keys...), Where: index.Where, RawSQL: index.RawSQL,
	}
	if len(input.Keys) == 0 && input.RawSQL == "" {
		input.Fields = append([]string(nil), index.Fields...)
	}
	return input
}

func indexInputForCollection(index Index, collection string) IndexInput {
	input := indexInputFromCatalog(index)
	if input.RawSQL == "" {
		return input
	}
	statement, ok := parseRawIndex(input.RawSQL)
	if !ok {
		return input
	}
	statement.Name = input.Name
	statement.Table = &pgparse.TableName{Schema: "app", Name: collection}
	input.RawSQL = deparseRawIndex(statement)
	return input
}

func catalogIndexesForCollection(indexes []Index, collection string) []Index {
	result := append([]Index(nil), indexes...)
	for i := range result {
		if result[i].RawSQL != "" {
			result[i].RawSQL = indexInputForCollection(result[i], collection).RawSQL
		}
	}
	return result
}

func stringSlicesEqual(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// The two plan formats a live fleet can hold. v2 differs from v1 in exactly one
// way — a create plan builds its companion indexes in the same transaction as
// the table — and the label is part of the digest, so the two never collide.
// Which one this binary issues is not a constant: migration 8 puts that in the
// capability row, where only 'ready' means v2.
const (
	schemaPlanCompilerV1 = "schema-plan-v1"
	schemaPlanCompilerV2 = "schema-plan-v2"
)
