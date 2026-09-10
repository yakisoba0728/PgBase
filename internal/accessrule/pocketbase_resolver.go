package accessrule

import (
	"encoding/json"
	"strconv"
	"strings"

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
	left, err := c.resolve(rule.Field)
	if err != nil {
		return "", err
	}
	if left.Kind == 0 {
		return "", ErrInvalid
	}
	rightSQL := ""
	if rule.OtherField != "" {
		right, err := c.resolve(rule.OtherField)
		if err != nil || right.Kind != left.Kind || right.Cardinality != PBScalar {
			return "", ErrInvalid
		}
		rightSQL = right.SQL
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
		}
		return PBOperand{SQL: c.typedParameter(kind, value), Kind: kind, Cardinality: PBScalar}, nil
	}
	lookup := func(prefix string, values map[string][]string) (PBOperand, error) {
		key := strings.TrimPrefix(path, prefix)
		if key == "" {
			return PBOperand{}, ErrInvalid
		}
		items := values[key]
		if len(items) == 0 {
			return text(""), nil
		}
		return text(items[0]), nil
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
		value, err := pbTypedValue(field.Kind, raw)
		if err != nil {
			return PBOperand{}, err
		}
		return PBOperand{SQL: c.typedParameter(field.Kind, value), Kind: field.Kind, Cardinality: PBScalar}, nil
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
