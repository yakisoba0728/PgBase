package accessrule

import (
	"encoding/json"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/yakisoba0728/PgBase/internal/identity"
)

// PBKind is the comparison type of a trusted resolver expression.
type PBKind uint8

const (
	PBText PBKind = iota + 1
	PBBool
	PBNumber
	PBTime
	PBUUID
	PBJSON
)

// PBCardinality describes whether an operand is scalar or can produce several
// correlated values. Resolvers construct SQL only from trusted catalog data.
type PBCardinality uint8

const (
	PBScalar PBCardinality = iota + 1
	PBMany
)

// PBMulti describes a bounded correlated value source. From and Predicate are
// trusted SQL fragments produced by an internal resolver; ValueSQL names the
// typed value compared by the shared compiler.
type PBMulti struct {
	From      string
	Predicate string
	ValueSQL  string
}

// PBOperand is a typed SQL operand returned by a catalog or auth-record
// adapter. SQL is used for scalar operands; Multi is required for PBMany.
type PBOperand struct {
	SQL         string
	Kind        PBKind
	Cardinality PBCardinality
	Multi       *PBMulti
}

// PBResolver is implemented by trusted adapters for physical collections and
// virtual auth records. User input is always a path, never SQL.
type PBResolver interface {
	Resolve(path string) (PBOperand, error)
	ResolveCollection(collection, path string) (PBOperand, error)
}

// PBCompileContext supplies authority-free resolution and request values. The
// caller executes returned SQL inside its already pinned transaction.
type PBCompileContext struct {
	Resolver  PBResolver
	Principal identity.Subject
	Request   *RequestContext
	ArgOffset int
}

// CompilePocketBase compiles the persisted PocketBase rule AST through a
// trusted operand resolver. Returned arguments start after ArgOffset.
func CompilePocketBase(rule Rule, context PBCompileContext) (string, []any, error) {
	if context.Resolver == nil || context.ArgOffset < 0 || context.ArgOffset > 10000 {
		return "", nil, ErrInvalid
	}
	c := pbCompiler{context: context}
	sql, err := c.rule(rule, 0)
	return sql, c.args, err
}

type pbCompiler struct {
	context PBCompileContext
	args    []any
	nodes   int
}

func (c *pbCompiler) parameter(value any) string {
	c.args = append(c.args, value)
	return "$" + strconv.Itoa(c.context.ArgOffset+len(c.args))
}

func (c *pbCompiler) typedParameter(kind PBKind, value any) string {
	parameter := c.parameter(value)
	cast := map[PBKind]string{PBBool: "boolean", PBNumber: "numeric", PBTime: "timestamptz", PBUUID: "uuid", PBJSON: "jsonb"}[kind]
	if cast != "" {
		return parameter + "::" + cast
	}
	return parameter
}
func (c *pbCompiler) rule(rule Rule, depth int) (string, error) {
	c.nodes++
	if depth > 8 || c.nodes > 64 {
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
		value := c.context.Principal.Kind != identity.Anonymous
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
	var left PBOperand
	var err error
	if rule.Function != nil {
		left, err = c.function(*rule.Function)
	} else {
		left, err = c.resolve(rule.Field)
	}
	if err != nil {
		return "", err
	}
	if left.Kind == 0 {
		return "", ErrInvalid
	}
	rightSQL := ""
	var rightMulti *PBMulti
	if rule.OtherField != "" {
		right, err := c.resolve(rule.OtherField)
		if err != nil || right.Kind != left.Kind {
			return "", ErrInvalid
		}
		if right.Cardinality == PBMany {
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
		if !ok {
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
		if left.Cardinality != PBMany || left.Multi == nil || rightMulti.From == "" || rightMulti.ValueSQL == "" {
			return "", ErrInvalid
		}
		match := func(leftSQL, rightSQL string) string {
			rhs := rightSQL
			if base == "contains" || base == "not_contains" {
				rhs = "('%' || " + rightSQL + " || '%')"
			}
			return "COALESCE(" + leftSQL + " " + op + " " + rhs + ",false)"
		}
		leftWhere, rightWhere := left.Multi.Predicate, rightMulti.Predicate
		if leftWhere != "" {
			leftWhere = "(" + leftWhere + ") AND "
		}
		if rightWhere != "" {
			rightWhere = "(" + rightWhere + ") AND "
		}
		rightExists := "EXISTS (SELECT 1 FROM " + rightMulti.From + " WHERE " + rightWhere + match(left.Multi.ValueSQL, rightMulti.ValueSQL) + ")"
		if anyMatch {
			return "EXISTS (SELECT 1 FROM " + left.Multi.From + " WHERE " + leftWhere + rightExists + ")", nil
		}
		return "NOT EXISTS (SELECT 1 FROM " + left.Multi.From + " WHERE " + leftWhere + "NOT (" + rightExists + "))", nil
	}
	if left.Cardinality == PBScalar {
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

var pbRelativeTimeModifier = regexp.MustCompile(`^([+-]?\d+)\s+(seconds?|minutes?|hours?|days?|months?|years?)$`)

func (c *pbCompiler) function(function PBFunction) (PBOperand, error) {
	arg := func(index int, kind PBKind) (PBOperand, error) {
		if index >= len(function.Args) {
			return PBOperand{}, ErrInvalid
		}
		value := function.Args[index]
		if value.Macro != "" {
			sql, resolvedKind, ok := pbMacroSQL(value.Macro)
			if !ok || kind != 0 && kind != resolvedKind {
				return PBOperand{}, ErrInvalid
			}
			return PBOperand{SQL: sql, Kind: resolvedKind, Cardinality: PBScalar}, nil
		}
		if value.Field != "" && len(value.Value) == 0 {
			resolved, err := c.resolve(value.Field)
			if err != nil || (kind != 0 && resolved.Kind != kind) {
				return PBOperand{}, ErrInvalid
			}
			return resolved, nil
		}
		if value.Field != "" || len(value.Value) == 0 {
			return PBOperand{}, ErrInvalid
		}
		if kind == 0 {
			kind = PBText
		}
		typed, err := pbTypedValue(kind, value.Value)
		if err != nil {
			return PBOperand{}, err
		}
		return PBOperand{SQL: c.typedParameter(kind, typed), Kind: kind, Cardinality: PBScalar}, nil
	}
	combine := func(values []PBOperand, expression func([]string) string, kind PBKind) (PBOperand, error) {
		parts := make([]string, len(values))
		var multi *PBMulti
		for i, value := range values {
			if value.Cardinality == PBMany {
				if value.Multi == nil || (multi != nil && (multi.From != value.Multi.From || multi.Predicate != value.Multi.Predicate)) {
					return PBOperand{}, ErrInvalid
				}
				multi = value.Multi
				parts[i] = value.Multi.ValueSQL
			} else {
				parts[i] = value.SQL
			}
		}
		sql := expression(parts)
		if sql == "" {
			return PBOperand{}, ErrInvalid
		}
		if multi == nil {
			return PBOperand{SQL: sql, Kind: kind, Cardinality: PBScalar}, nil
		}
		return PBOperand{Kind: kind, Cardinality: PBMany, Multi: &PBMulti{From: multi.From, Predicate: multi.Predicate, ValueSQL: sql}}, nil
	}
	switch function.Name {
	case "geoDistance":
		if len(function.Args) != 4 {
			return PBOperand{}, ErrInvalid
		}
		values := make([]PBOperand, 4)
		for i := range values {
			var err error
			values[i], err = arg(i, PBNumber)
			if err != nil {
				return PBOperand{}, err
			}
		}
		return combine(values, func(v []string) string {
			return "(6371 * acos(LEAST(1,GREATEST(-1,cos(radians(" + v[1] + "))*cos(radians(" + v[3] + "))*cos(radians(" + v[2] + ")-radians(" + v[0] + "))+sin(radians(" + v[1] + "))*sin(radians(" + v[3] + "))))))"
		}, PBNumber)
	case "strftime":
		if len(function.Args) < 1 || len(function.Args) > 10 {
			return PBOperand{}, ErrInvalid
		}
		var rawFormat string
		if function.Args[0].Field != "" || json.Unmarshal(function.Args[0].Value, &rawFormat) != nil {
			return PBOperand{}, ErrInvalid
		}
		formatSQL := c.parameter(pbTimeFormat(rawFormat))
		value := PBOperand{SQL: "CURRENT_TIMESTAMP", Kind: PBTime, Cardinality: PBScalar}
		if len(function.Args) > 1 {
			var err error
			value, err = arg(1, PBTime)
			if err != nil {
				return PBOperand{}, err
			}
		}
		modifierSQL := []string{}
		for i := 2; i < len(function.Args); i++ {
			var raw string
			if json.Unmarshal(function.Args[i].Value, &raw) != nil {
				return PBOperand{}, ErrInvalid
			}
			modifierSQL = append(modifierSQL, raw)
		}
		return combine([]PBOperand{value}, func(v []string) string {
			timeSQL, ok := pbApplyTimeModifiers(v[0], modifierSQL, c)
			if !ok {
				return ""
			}
			return "to_char(" + timeSQL + "," + formatSQL + ")"
		}, PBText)
	}
	return PBOperand{}, ErrInvalid
}

func pbMacroSQL(name string) (string, PBKind, bool) {
	now := "CURRENT_TIMESTAMP"
	switch name {
	case "@now":
		return now, PBTime, true
	case "@yesterday":
		return "(" + now + "-interval '1 day')", PBTime, true
	case "@tomorrow":
		return "(" + now + "+interval '1 day')", PBTime, true
	case "@todayStart":
		return "date_trunc('day'," + now + ")", PBTime, true
	case "@todayEnd":
		return "(date_trunc('day'," + now + ")+interval '1 day'-interval '1 millisecond')", PBTime, true
	case "@monthStart":
		return "date_trunc('month'," + now + ")", PBTime, true
	case "@monthEnd":
		return "(date_trunc('month'," + now + ")+interval '1 month'-interval '1 millisecond')", PBTime, true
	case "@yearStart":
		return "date_trunc('year'," + now + ")", PBTime, true
	case "@yearEnd":
		return "(date_trunc('year'," + now + ")+interval '1 year'-interval '1 millisecond')", PBTime, true
	}
	part := map[string]string{"@second": "second", "@minute": "minute", "@hour": "hour", "@day": "day", "@month": "month", "@weekday": "dow", "@year": "year"}[name]
	if part == "" {
		return "", 0, false
	}
	return "extract(" + part + " from " + now + ")", PBNumber, true
}

func pbTimeFormat(format string) string {
	return strings.NewReplacer("%Y", "YYYY", "%m", "MM", "%d", "DD", "%H", "HH24", "%M", "MI", "%S", "SS", "%w", "D", "%j", "DDD", "%s", "SSSS").Replace(format)
}

func pbApplyTimeModifiers(value string, modifiers []string, c *pbCompiler) (string, bool) {
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

func (c *pbCompiler) resolve(path string) (PBOperand, error) {
	if strings.HasPrefix(path, "__pgbase_request_") {
		return c.resolveRequest(path)
	}
	if strings.HasPrefix(path, "@collection.") {
		parts := strings.SplitN(strings.TrimPrefix(path, "@collection."), ".", 2)
		if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
			return PBOperand{}, ErrInvalid
		}
		return c.context.Resolver.ResolveCollection(parts[0], parts[1])
	}
	return c.context.Resolver.Resolve(path)
}

func (c *pbCompiler) resolveRequest(path string) (PBOperand, error) {
	r := c.context.Request
	if r == nil {
		return PBOperand{}, ErrInvalid
	}
	text := func(value any) PBOperand {
		return PBOperand{SQL: c.typedParameter(PBText, value), Kind: PBText, Cardinality: PBScalar}
	}
	if path == "__pgbase_request_method" {
		return text(r.Method), nil
	}
	if strings.HasPrefix(path, "__pgbase_request_auth_") {
		key := strings.TrimPrefix(path, "__pgbase_request_auth_")
		value, ok := r.Auth[key]
		if !ok || key == "" {
			return PBOperand{}, ErrInvalid
		}
		kind := PBText
		switch value.(type) {
		case bool:
			kind = PBBool
		case int, int8, int16, int32, int64, uint, uint8, uint16, uint32, uint64, float32, float64, json.Number:
			kind = PBNumber
		case time.Time:
			kind = PBTime
		case uuid.UUID:
			kind = PBUUID
		}
		return PBOperand{SQL: c.typedParameter(kind, value), Kind: kind, Cardinality: PBScalar}, nil
	}
	lookup := func(prefix string, values map[string][]string) (PBOperand, error) {
		key := strings.TrimPrefix(path, prefix)
		modifier := ""
		if strings.Contains(key, ":") {
			key, modifier, _ = strings.Cut(key, ":")
		}
		if key == "" {
			return PBOperand{}, ErrInvalid
		}
		items := values[key]
		if modifier == "isset" {
			_, present := values[key]
			return PBOperand{SQL: c.typedParameter(PBBool, present), Kind: PBBool, Cardinality: PBScalar}, nil
		}
		if modifier == "each" {
			parameter := c.typedParameter(PBJSON, items)
			return PBOperand{Kind: PBText, Cardinality: PBMany, Multi: &PBMulti{From: "jsonb_array_elements_text(" + parameter + ") pb_request_values(value)", ValueSQL: "pb_request_values.value"}}, nil
		}
		if len(items) == 0 {
			return text(""), nil
		}
		result := text(items[0])
		switch modifier {
		case "":
			return result, nil
		case "lower":
			result.SQL = "lower(" + result.SQL + ")"
			return result, nil
		case "length":
			result.SQL = "char_length(" + result.SQL + ")"
			result.Kind = PBNumber
			return result, nil
		default:
			return PBOperand{}, ErrInvalid
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
			return PBOperand{SQL: c.typedParameter(PBBool, r.Present[key]), Kind: PBBool, Cardinality: PBScalar}, nil
		}
		if modifier == "changed" {
			return PBOperand{SQL: c.typedParameter(PBBool, r.Changed[key]), Kind: PBBool, Cardinality: PBScalar}, nil
		}
		field, err := c.context.Resolver.Resolve(key)
		if err != nil {
			return PBOperand{}, err
		}
		raw, ok := r.Body[key]
		if !ok {
			return PBOperand{SQL: c.typedParameter(field.Kind, nil), Kind: field.Kind, Cardinality: PBScalar}, nil
		}
		if modifier == "each" {
			var array any
			if json.Unmarshal(raw, &array) != nil {
				return PBOperand{}, ErrInvalid
			}
			parameter := c.typedParameter(PBJSON, array)
			return PBOperand{Kind: PBText, Cardinality: PBMany, Multi: &PBMulti{From: "jsonb_array_elements_text(CASE WHEN jsonb_typeof(" + parameter + ")='array' THEN " + parameter + " ELSE jsonb_build_array(" + parameter + ") END) pb_request_each(value)", ValueSQL: "pb_request_each.value"}}, nil
		}
		value, err := pbTypedValue(field.Kind, raw)
		if err != nil {
			return PBOperand{}, err
		}
		operand := PBOperand{SQL: c.typedParameter(field.Kind, value), Kind: field.Kind, Cardinality: PBScalar}
		switch modifier {
		case "":
			return operand, nil
		case "lower":
			if field.Kind != PBText {
				return PBOperand{}, ErrInvalid
			}
			operand.SQL = "lower(" + operand.SQL + ")"
			return operand, nil
		case "length":
			if field.Kind != PBText {
				return PBOperand{}, ErrInvalid
			}
			operand.SQL = "char_length(" + operand.SQL + ")"
			operand.Kind = PBNumber
			return operand, nil
		default:
			return PBOperand{}, ErrInvalid
		}
	}
	return PBOperand{}, ErrInvalid
}

func pbTypedValue(kind PBKind, raw json.RawMessage) (any, error) {
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
	case PBText, PBTime, PBUUID:
		if _, ok := value.(string); !ok {
			return nil, ErrInvalid
		}
	case PBBool:
		if _, ok := value.(bool); !ok {
			return nil, ErrInvalid
		}
	case PBNumber:
		if _, ok := value.(json.Number); !ok {
			return nil, ErrInvalid
		}
	}
	return value, nil
}
