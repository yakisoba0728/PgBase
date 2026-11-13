package accessrule

import (
	"encoding/json"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/yakisoba0728/PgBase/internal/identity"
)

// OperandKind is the comparison type of a trusted resolver expression.
type OperandKind uint8

const (
	OperandText OperandKind = iota + 1
	OperandBool
	OperandNumber
	OperandTime
	OperandUUID
	OperandJSON
)

// Cardinality describes whether an operand is scalar or can produce several
// correlated values. Resolvers construct SQL only from trusted catalog data.
type Cardinality uint8

const (
	Scalar Cardinality = iota + 1
	Many
)

// MultiOperand describes a bounded correlated value source. From and Predicate are
// trusted SQL fragments produced by an internal resolver; ValueSQL names the
// typed value compared by the shared compiler.
type MultiOperand struct {
	From      string
	Predicate string
	ValueSQL  string
}

// Operand is a typed SQL operand returned by a catalog or auth-record
// adapter. SQL is used for scalar operands; Multi is required for Many.
type Operand struct {
	SQL         string
	Kind        OperandKind
	Cardinality Cardinality
	Multi       *MultiOperand
}

// Resolver is implemented by trusted adapters for physical collections and
// virtual auth records. User input is always a path, never SQL.
type Resolver interface {
	Resolve(path string) (Operand, error)
	ResolveCollection(collection, path string) (Operand, error)
}

// ExpressionContext supplies authority-free resolution and request values. The
// caller executes returned SQL inside its already pinned transaction.
type ExpressionContext struct {
	Resolver       Resolver
	Principal      identity.Subject
	Request        *RequestContext
	ArgOffset      int
	validationOnly bool
	outerAliases   []string
}

// CompileExpression compiles the persisted expression AST through a
// trusted operand resolver. Returned arguments start after ArgOffset.
func CompileExpression(rule Rule, context ExpressionContext) (string, []any, error) {
	if context.Resolver == nil || context.ArgOffset < 0 || context.ArgOffset > 10000 {
		return "", nil, ErrInvalid
	}
	c := expressionCompiler{context: context}
	sql, err := c.rule(rule, 0)
	return sql, c.args, err
}

type expressionCompiler struct {
	context ExpressionContext
	args    []any
	nodes   int
}

func (c *expressionCompiler) parameter(value any) string {
	c.args = append(c.args, value)
	return "$" + strconv.Itoa(c.context.ArgOffset+len(c.args))
}

func (c *expressionCompiler) typedParameter(kind OperandKind, value any) string {
	parameter := c.parameter(value)
	cast := map[OperandKind]string{OperandBool: "boolean", OperandNumber: "numeric", OperandTime: "timestamptz", OperandUUID: "uuid", OperandJSON: "jsonb"}[kind]
	if cast != "" {
		return parameter + "::" + cast
	}
	return parameter
}
func (c *expressionCompiler) rule(rule Rule, depth int) (string, error) {
	c.nodes++
	if (rule.Binding != "" && rule.Binding != BindingCatalog && rule.Binding != BindingScalar) || depth > 8 || c.nodes > 64 {
		return "", ErrInvalid
	}
	if rule.Op == "all" || rule.Op == "any" {
		if len(rule.Args) < 1 || len(rule.Args) > 16 {
			return "", ErrInvalid
		}
		parts := make([]string, len(rule.Args))
		for i, child := range rule.Args {
			value, err := c.rule(child, depth+1)
			if err != nil {
				return "", err
			}
			parts[i] = "(" + value + ")"
		}
		join := " AND "
		if rule.Op == "any" {
			join = " OR "
		}
		return "(" + strings.Join(parts, join) + ")", nil
	}
	if rule.Op == "allow" {
		return "TRUE", nil
	}
	if rule.Op == "deny" {
		return "FALSE", nil
	}
	if rule.Op == "authenticated" || rule.Op == "unauthenticated" {
		value := authenticatedSubject(c.context.Principal)
		if rule.Op == "unauthenticated" {
			value = !value
		}
		return c.parameter(value), nil
	}
	base := strings.TrimPrefix(rule.Op, "any_")
	anyMatch := base != rule.Op
	op, ok := map[string]string{"eq": "=", "ne": "<>", "lt": "<", "lte": "<=", "gt": ">", "gte": ">=", "contains": "LIKE", "not_contains": "NOT LIKE"}[base]
	if !ok || rule.Field == "" {
		return "", ErrInvalid
	}
	var left Operand
	var err error
	if rule.Function != nil {
		left, err = c.function(*rule.Function)
	} else {
		left, err = c.resolve(rule.Field)
	}
	if err != nil {
		return "", err
	}
	if left.Kind == 0 || (base == "contains" || base == "not_contains") && left.Kind != OperandText {
		return "", ErrInvalid
	}
	rightSQL := ""
	var rightMulti *MultiOperand
	if rule.OtherField != "" {
		right, err := c.resolve(rule.OtherField)
		if err != nil || right.Kind != left.Kind {
			return "", ErrInvalid
		}
		if right.Cardinality == Many {
			rightMulti = right.Multi
		} else {
			rightSQL = right.SQL
		}
	} else if rule.Auth != "" {
		var value any
		var ok bool
		if rule.Auth == "id" {
			value, ok = c.context.Principal.ID, true
		} else if c.context.Request != nil {
			value, ok = c.context.Request.Auth[rule.Auth]
		}
		if !ok && !c.context.validationOnly {
			return "", ErrInvalid
		}
		rightSQL = c.typedParameter(left.Kind, value)
	} else if rule.Macro != "" {
		macro, kind, ok := pbMacroSQL(rule.Macro)
		if !ok || kind != left.Kind {
			return "", ErrInvalid
		}
		rightSQL = macro
	} else {
		value, err := pbTypedValue(left.Kind, rule.Value)
		if err != nil {
			return "", err
		}
		if value == nil {
			if base == "eq" {
				op = "IS"
			} else if base == "ne" {
				op = "IS NOT"
			} else {
				return "", ErrInvalid
			}
			rightSQL = "NULL"
		} else {
			rightSQL = c.typedParameter(left.Kind, value)
		}
	}
	compare := func(valueSQL string) string {
		rhs := rightSQL
		if base == "contains" || base == "not_contains" {
			rhs = "('%' || " + rightSQL + " || '%')"
		}
		return "COALESCE(" + valueSQL + " " + op + " " + rhs + ",false)"
	}
	if rightMulti != nil {
		if rightMulti.From == "" || rightMulti.ValueSQL == "" {
			return "", ErrInvalid
		}
		match := func(leftSQL, rightSQL string) string {
			rhs := rightSQL
			if base == "contains" || base == "not_contains" {
				rhs = "('%' || " + rightSQL + " || '%')"
			}
			return "COALESCE(" + leftSQL + " " + op + " " + rhs + ",false)"
		}
		if left.Cardinality == Scalar {
			if left.SQL == "" {
				return "", ErrInvalid
			}
			alias := c.projectionAlias("pgbase_operand_right")
			return "EXISTS (SELECT 1 FROM LATERAL " + multiSource(rightMulti, alias) + " WHERE " + match(left.SQL, alias+".value") + ")", nil
		}
		if left.Multi == nil {
			return "", ErrInvalid
		}
		// Project each source in its own scope so resolver aliases cannot shadow
		// the other operand's value or correlation predicates.
		leftAlias := c.projectionAlias("pgbase_operand_left")
		rightAlias := c.projectionAlias("pgbase_operand_right")
		leftSource := multiSource(left.Multi, leftAlias)
		rightSource := multiSource(rightMulti, rightAlias)
		rightExists := "EXISTS (SELECT 1 FROM LATERAL " + rightSource + " WHERE " + match(leftAlias+".value", rightAlias+".value") + ")"
		if anyMatch {
			return "EXISTS (SELECT 1 FROM LATERAL " + leftSource + " WHERE " + rightExists + ")", nil
		}
		return "NOT EXISTS (SELECT 1 FROM LATERAL " + leftSource + " WHERE NOT (" + rightExists + "))", nil
	}
	if left.Cardinality == Scalar {
		if left.SQL == "" {
			return "", ErrInvalid
		}
		return compare(left.SQL), nil
	}
	if left.Multi == nil || left.Multi.From == "" || left.Multi.ValueSQL == "" {
		return "", ErrInvalid
	}
	where := left.Multi.Predicate
	if where != "" {
		where = "(" + where + ") AND "
	}
	match := compare(left.Multi.ValueSQL)
	if anyMatch {
		return "EXISTS (SELECT 1 FROM " + left.Multi.From + " WHERE " + where + match + ")", nil
	}
	return "NOT EXISTS (SELECT 1 FROM " + left.Multi.From + " WHERE " + where + "NOT (" + match + "))", nil
}

func (c *expressionCompiler) projectionAlias(alias string) string {
	for slices.Contains(c.context.outerAliases, alias) {
		alias += "_"
	}
	return alias
}

func multiSource(value *MultiOperand, alias string) string {
	where := ""
	if value.Predicate != "" {
		where = " WHERE " + value.Predicate
	}
	return "(SELECT " + value.ValueSQL + " AS value FROM " + value.From + where + ") AS " + alias
}

var pbRelativeTimeModifier = regexp.MustCompile(`^([+-]?\d+)\s+(seconds?|minutes?|hours?|days?|months?|years?)$`)

func (c *expressionCompiler) function(function Function) (Operand, error) {
	arg := func(index int, kind OperandKind) (Operand, error) {
		if index >= len(function.Args) {
			return Operand{}, ErrInvalid
		}
		value := function.Args[index]
		if value.Macro != "" {
			sql, resolvedKind, ok := pbMacroSQL(value.Macro)
			if !ok || kind != 0 && kind != resolvedKind {
				return Operand{}, ErrInvalid
			}
			return Operand{SQL: sql, Kind: resolvedKind, Cardinality: Scalar}, nil
		}
		if value.Field != "" && len(value.Value) == 0 {
			resolved, err := c.resolve(value.Field)
			if err != nil || (kind != 0 && resolved.Kind != kind) {
				return Operand{}, ErrInvalid
			}
			return resolved, nil
		}
		if value.Field != "" || len(value.Value) == 0 {
			return Operand{}, ErrInvalid
		}
		if kind == 0 {
			kind = OperandText
		}
		typed, err := pbTypedValue(kind, value.Value)
		if err != nil {
			return Operand{}, err
		}
		return Operand{SQL: c.typedParameter(kind, typed), Kind: kind, Cardinality: Scalar}, nil
	}
	combine := func(values []Operand, expression func([]string) string, kind OperandKind) (Operand, error) {
		parts := make([]string, len(values))
		var multi *MultiOperand
		for i, value := range values {
			if value.Cardinality == Many {
				if value.Multi == nil || (multi != nil && (multi.From != value.Multi.From || multi.Predicate != value.Multi.Predicate)) {
					return Operand{}, ErrInvalid
				}
				multi = value.Multi
				parts[i] = value.Multi.ValueSQL
			} else {
				parts[i] = value.SQL
			}
		}
		sql := expression(parts)
		if sql == "" {
			return Operand{}, ErrInvalid
		}
		if multi == nil {
			return Operand{SQL: sql, Kind: kind, Cardinality: Scalar}, nil
		}
		return Operand{Kind: kind, Cardinality: Many, Multi: &MultiOperand{From: multi.From, Predicate: multi.Predicate, ValueSQL: sql}}, nil
	}
	switch function.Name {
	case "geoDistance":
		if len(function.Args) != 4 {
			return Operand{}, ErrInvalid
		}
		values := make([]Operand, 4)
		for i := range values {
			var err error
			values[i], err = arg(i, OperandNumber)
			if err != nil {
				return Operand{}, err
			}
		}
		return combine(values, func(v []string) string {
			return "(6371 * acos(LEAST(1,GREATEST(-1,cos(radians(" + v[1] + "))*cos(radians(" + v[3] + "))*cos(radians(" + v[2] + ")-radians(" + v[0] + "))+sin(radians(" + v[1] + "))*sin(radians(" + v[3] + "))))))"
		}, OperandNumber)
	case "strftime":
		if len(function.Args) < 1 || len(function.Args) > 10 {
			return Operand{}, ErrInvalid
		}
		var rawFormat string
		if function.Args[0].Field != "" || json.Unmarshal(function.Args[0].Value, &rawFormat) != nil {
			return Operand{}, ErrInvalid
		}
		formatSQL := c.parameter(pbTimeFormat(rawFormat))
		value := Operand{SQL: "CURRENT_TIMESTAMP", Kind: OperandTime, Cardinality: Scalar}
		if len(function.Args) > 1 {
			var err error
			value, err = arg(1, OperandTime)
			if err != nil {
				return Operand{}, err
			}
		}
		modifierSQL := []string{}
		for i := 2; i < len(function.Args); i++ {
			var raw string
			if json.Unmarshal(function.Args[i].Value, &raw) != nil {
				return Operand{}, ErrInvalid
			}
			modifierSQL = append(modifierSQL, raw)
		}
		return combine([]Operand{value}, func(v []string) string {
			timeSQL, ok := pbApplyTimeModifiers(v[0], modifierSQL, c)
			if !ok {
				return ""
			}
			return "to_char(" + timeSQL + "," + formatSQL + ")"
		}, OperandText)
	}
	return Operand{}, ErrInvalid
}

func pbMacroSQL(name string) (string, OperandKind, bool) {
	now := "CURRENT_TIMESTAMP"
	switch name {
	case "@now":
		return now, OperandTime, true
	case "@yesterday":
		return "(" + now + "-interval '1 day')", OperandTime, true
	case "@tomorrow":
		return "(" + now + "+interval '1 day')", OperandTime, true
	case "@todayStart":
		return "date_trunc('day'," + now + ")", OperandTime, true
	case "@todayEnd":
		return "(date_trunc('day'," + now + ")+interval '1 day'-interval '1 millisecond')", OperandTime, true
	case "@monthStart":
		return "date_trunc('month'," + now + ")", OperandTime, true
	case "@monthEnd":
		return "(date_trunc('month'," + now + ")+interval '1 month'-interval '1 millisecond')", OperandTime, true
	case "@yearStart":
		return "date_trunc('year'," + now + ")", OperandTime, true
	case "@yearEnd":
		return "(date_trunc('year'," + now + ")+interval '1 year'-interval '1 millisecond')", OperandTime, true
	}
	part := map[string]string{"@second": "second", "@minute": "minute", "@hour": "hour", "@day": "day", "@month": "month", "@weekday": "dow", "@year": "year"}[name]
	if part == "" {
		return "", 0, false
	}
	return "extract(" + part + " from " + now + ")", OperandNumber, true
}

func pbTimeFormat(format string) string {
	return strings.NewReplacer("%Y", "YYYY", "%m", "MM", "%d", "DD", "%H", "HH24", "%M", "MI", "%S", "SS", "%w", "D", "%j", "DDD", "%s", "SSSS").Replace(format)
}

func pbApplyTimeModifiers(value string, modifiers []string, c *expressionCompiler) (string, bool) {
	result := value
	for _, modifier := range modifiers {
		switch modifier {
		case "start of day":
			result = "date_trunc('day'," + result + ")"
		case "start of month":
			result = "date_trunc('month'," + result + ")"
		case "start of year":
			result = "date_trunc('year'," + result + ")"
		case "utc":
			result = "(" + result + " AT TIME ZONE 'UTC')"
		case "localtime":
			result = "(" + result + " AT TIME ZONE current_setting('TIMEZONE'))"
		default:
			match := pbRelativeTimeModifier.FindStringSubmatch(modifier)
			if match == nil {
				return "", false
			}
			result = "(" + result + "+(" + c.parameter(match[1]+" "+match[2]) + ")::interval)"
		}
	}
	return result, true
}

func (c *expressionCompiler) resolve(path string) (Operand, error) {
	if strings.HasPrefix(path, "__pgbase_request_") {
		return c.resolveRequest(path)
	}
	if strings.HasPrefix(path, "@collection.") {
		parts := strings.SplitN(strings.TrimPrefix(path, "@collection."), ".", 2)
		if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
			return Operand{}, ErrInvalid
		}
		return c.context.Resolver.ResolveCollection(parts[0], parts[1])
	}
	return c.context.Resolver.Resolve(path)
}

func (c *expressionCompiler) resolveRequest(path string) (Operand, error) {
	r := c.context.Request
	if r == nil {
		return Operand{}, ErrInvalid
	}
	text := func(value any) Operand {
		return Operand{SQL: c.typedParameter(OperandText, value), Kind: OperandText, Cardinality: Scalar}
	}
	if path == "__pgbase_request_method" {
		return text(r.Method), nil
	}
	if strings.HasPrefix(path, "__pgbase_request_auth_") {
		key := strings.TrimPrefix(path, "__pgbase_request_auth_")
		value, ok := r.Auth[key]
		if key == "" || !ok && !c.context.validationOnly {
			return Operand{}, ErrInvalid
		}
		kind := OperandText
		if c.context.validationOnly {
			switch key {
			case "verified", "active", "emailVisibility":
				kind = OperandBool
			}
		}
		switch value.(type) {
		case bool:
			kind = OperandBool
		case int, int8, int16, int32, int64, uint, uint8, uint16, uint32, uint64, float32, float64, json.Number:
			kind = OperandNumber
		case time.Time:
			kind = OperandTime
		case uuid.UUID:
			kind = OperandUUID
		}
		return Operand{SQL: c.typedParameter(kind, value), Kind: kind, Cardinality: Scalar}, nil
	}
	lookup := func(prefix string, values map[string][]string) (Operand, error) {
		key := strings.TrimPrefix(path, prefix)
		modifier := ""
		if strings.Contains(key, ":") {
			key, modifier, _ = strings.Cut(key, ":")
		}
		if key == "" {
			return Operand{}, ErrInvalid
		}
		items := values[key]
		if modifier == "isset" {
			_, present := values[key]
			return Operand{SQL: c.typedParameter(OperandBool, present), Kind: OperandBool, Cardinality: Scalar}, nil
		}
		if modifier == "each" {
			parameter := c.typedParameter(OperandJSON, items)
			alias := c.projectionAlias("pb_request_values")
			return Operand{Kind: OperandText, Cardinality: Many, Multi: &MultiOperand{From: "jsonb_array_elements_text(" + parameter + ") " + alias + "(value)", ValueSQL: alias + ".value"}}, nil
		}
		first := ""
		if len(items) != 0 {
			first = items[0]
		}
		result := text(first)
		switch modifier {
		case "":
			return result, nil
		case "lower":
			result.SQL = "lower(" + result.SQL + ")"
			return result, nil
		case "length":
			result.SQL = "char_length(" + result.SQL + ")"
			result.Kind = OperandNumber
			return result, nil
		default:
			return Operand{}, ErrInvalid
		}
	}
	if strings.HasPrefix(path, "__pgbase_request_query_") {
		return lookup("__pgbase_request_query_", r.Query)
	}
	if strings.HasPrefix(path, "__pgbase_request_header_") {
		return lookup("__pgbase_request_header_", r.Headers)
	}
	if strings.HasPrefix(path, "__pgbase_request_context_") {
		key := strings.TrimPrefix(path, "__pgbase_request_context_")
		return text(r.Context[key]), nil
	}
	if strings.HasPrefix(path, "__pgbase_request_body_") {
		key := strings.TrimPrefix(path, "__pgbase_request_body_")
		modifier := ""
		if strings.Contains(key, ":") {
			key, modifier, _ = strings.Cut(key, ":")
		}
		if modifier == "isset" {
			return Operand{SQL: c.typedParameter(OperandBool, r.Present[key]), Kind: OperandBool, Cardinality: Scalar}, nil
		}
		if modifier == "changed" {
			return Operand{SQL: c.typedParameter(OperandBool, r.Changed[key]), Kind: OperandBool, Cardinality: Scalar}, nil
		}
		field, err := c.context.Resolver.Resolve(key)
		if err != nil {
			return Operand{}, err
		}
		raw, ok := r.Body[key]
		if !ok {
			if !c.context.validationOnly {
				return Operand{SQL: c.typedParameter(field.Kind, nil), Kind: field.Kind, Cardinality: Scalar}, nil
			}
			raw = json.RawMessage(`null`)
		}
		if modifier == "each" {
			var array any
			if json.Unmarshal(raw, &array) != nil {
				return Operand{}, ErrInvalid
			}
			parameter := c.typedParameter(OperandJSON, array)
			alias := c.projectionAlias("pb_request_each")
			return Operand{Kind: OperandText, Cardinality: Many, Multi: &MultiOperand{From: "jsonb_array_elements_text(CASE WHEN jsonb_typeof(" + parameter + ")='array' THEN " + parameter + " ELSE jsonb_build_array(" + parameter + ") END) " + alias + "(value)", ValueSQL: alias + ".value"}}, nil
		}
		value, err := pbTypedValue(field.Kind, raw)
		if err != nil {
			return Operand{}, err
		}
		operand := Operand{SQL: c.typedParameter(field.Kind, value), Kind: field.Kind, Cardinality: Scalar}
		switch modifier {
		case "":
			return operand, nil
		case "lower":
			if field.Kind != OperandText {
				return Operand{}, ErrInvalid
			}
			operand.SQL = "lower(" + operand.SQL + ")"
			return operand, nil
		case "length":
			if field.Kind != OperandText {
				return Operand{}, ErrInvalid
			}
			operand.SQL = "char_length(" + operand.SQL + ")"
			operand.Kind = OperandNumber
			return operand, nil
		default:
			return Operand{}, ErrInvalid
		}
	}
	return Operand{}, ErrInvalid
}

func pbTypedValue(kind OperandKind, raw json.RawMessage) (any, error) {
	if len(raw) == 0 || len(raw) > 4096 {
		return nil, ErrInvalid
	}
	if string(raw) == "null" {
		return nil, nil
	}
	var value any
	decoder := json.NewDecoder(strings.NewReader(string(raw)))
	decoder.UseNumber()
	if decoder.Decode(&value) != nil {
		return nil, ErrInvalid
	}
	switch kind {
	case OperandText, OperandTime, OperandUUID:
		if _, ok := value.(string); !ok {
			return nil, ErrInvalid
		}
		if kind == OperandUUID {
			if _, err := uuid.Parse(value.(string)); err != nil {
				return nil, ErrInvalid
			}
		}
		if kind == OperandTime {
			if _, err := time.Parse(time.RFC3339Nano, value.(string)); err != nil {
				return nil, ErrInvalid
			}
		}
	case OperandBool:
		if _, ok := value.(bool); !ok {
			return nil, ErrInvalid
		}
	case OperandNumber:
		if _, ok := value.(json.Number); !ok {
			return nil, ErrInvalid
		}
	}
	return value, nil
}
