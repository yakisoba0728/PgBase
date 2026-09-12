// Package accessrule validates and compiles bounded, typed record predicates.
// Its JSON AST never accepts SQL, identifiers outside the catalog, or scripts.
package accessrule

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/yakisoba0728/PgBase/internal/identity"
	"github.com/yakisoba0728/PgBase/internal/schema"
)

var ErrInvalid = errors.New("invalid record access rule")

type Rule struct {
	Op         string          `json:"op"`
	Args       []Rule          `json:"args,omitempty"`
	Field      string          `json:"field,omitempty"`
	OtherField string          `json:"otherField,omitempty"`
	Value      json.RawMessage `json:"value,omitempty"`
	Auth       string          `json:"auth,omitempty"`
	PocketBase *string         `json:"pocketbase,omitempty"`
	Function   *PBFunction     `json:"function,omitempty"`
	Macro      string          `json:"macro,omitempty"`
}

type PBFunction struct {
	Name string      `json:"name"`
	Args []PBFuncArg `json:"args"`
}

type PBFuncArg struct {
	Field string          `json:"field,omitempty"`
	Value json.RawMessage `json:"value,omitempty"`
	Macro string          `json:"macro,omitempty"`
}

type Rules struct {
	List   Rule `json:"list"`
	View   Rule `json:"view"`
	Create Rule `json:"create"`
	Update Rule `json:"update"`
	Delete Rule `json:"delete"`
}

func OwnerRules() Rules { owner := Rule{Op: "owner"}; return Rules{owner, owner, owner, owner, owner} }

func Parse(raw []byte) (Rules, error) {
	if len(raw) == 0 || len(raw) > 16384 {
		return Rules{}, ErrInvalid
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	var rules Rules
	if decoder.Decode(&rules) != nil {
		return Rules{}, ErrInvalid
	}
	if decoder.Decode(new(any)) != io.EOF {
		return Rules{}, ErrInvalid
	}
	return rules, nil
}

func (r Rules) Action(action string) (Rule, error) {
	switch action {
	case "list":
		return r.List, nil
	case "view":
		return r.View, nil
	case "create":
		return r.Create, nil
	case "update":
		return r.Update, nil
	case "delete":
		return r.Delete, nil
	}
	return Rule{}, ErrInvalid
}

// Compile appends parameter values after offset. prospectiveOwner is true only
// for an admitted create before the new ownership row has been inserted.
func Compile(rule Rule, collection schema.Collection, alias string, principal identity.Subject, offset int, prospectiveOwner bool) (string, []any, error) {
	if offset < 0 || offset > 10000 || alias == "" {
		return "", nil, ErrInvalid
	}
	compiler := compiler{collection: collection, alias: pgx.Identifier{alias}.Sanitize(), principal: principal, offset: offset, prospectiveOwner: prospectiveOwner}
	sql, err := compiler.rule(rule, 0)
	return sql, compiler.args, err
}

type compiler struct {
	collection       schema.Collection
	alias            string
	principal        identity.Subject
	offset           int
	prospectiveOwner bool
	eventAlias       string
	nodes            int
	args             []any
	request          *RequestContext
}

type RequestContext struct {
	Present map[string]bool
	Changed map[string]bool
	Method  string
	Body    map[string]json.RawMessage
	Query   map[string][]string
	Headers map[string][]string
	Auth    map[string]any
	Context map[string]any
}

func authenticatedSubject(subject identity.Subject) bool {
	return subject.Valid() && (subject.Kind == identity.User || subject.Kind == identity.Administrator || subject.Kind == identity.Service)
}

func CompileWithRequest(rule Rule, collection schema.Collection, alias string, principal identity.Subject, offset int, prospectiveOwner bool, request RequestContext) (string, []any, error) {
	if offset < 0 || offset > 10000 || alias == "" {
		return "", nil, ErrInvalid
	}
	compiler := compiler{collection: collection, alias: pgx.Identifier{alias}.Sanitize(), principal: principal, offset: offset, prospectiveOwner: prospectiveOwner, request: &request}
	sql, err := compiler.rule(rule, 0)
	return sql, compiler.args, err
}

func (c *compiler) parameter(value any) string {
	c.args = append(c.args, value)
	return "$" + strconv.Itoa(c.offset+len(c.args))
}

func (c *compiler) rule(rule Rule, depth int) (string, error) {
	c.nodes++
	if depth > 8 || c.nodes > 64 {
		return "", ErrInvalid
	}
	switch rule.Op {
	case "allow", "deny", "owner", "authenticated", "unauthenticated":
		if len(rule.Args) > 0 || rule.Field != "" || rule.OtherField != "" || len(rule.Value) > 0 || rule.Auth != "" || rule.Function != nil || rule.Macro != "" {
			return "", ErrInvalid
		}
		switch rule.Op {
		case "allow":
			return "TRUE", nil
		case "deny":
			return "FALSE", nil
		case "authenticated":
			return c.parameter(authenticatedSubject(c.principal)), nil
		case "unauthenticated":
			return c.parameter(!authenticatedSubject(c.principal)), nil
		case "owner":
			if c.principal.Kind != identity.User {
				return "FALSE", nil
			}
			if c.prospectiveOwner {
				return "TRUE", nil
			}
			if c.eventAlias != "" {
				return c.eventAlias + ".owner_user_id=" + c.parameter(c.principal.ID), nil
			}
			return "EXISTS (SELECT 1 FROM pgbase_meta.record_owners acl_owner WHERE acl_owner.collection_id=" + c.parameter(c.collection.ID) + " AND acl_owner.record_id=" + c.alias + `."id" AND acl_owner.user_id=` + c.parameter(c.principal.ID) + ")", nil
		}
	case "all", "any":
		if len(rule.Args) < 1 || len(rule.Args) > 16 || rule.Field != "" || rule.OtherField != "" || len(rule.Value) > 0 || rule.Auth != "" || rule.Function != nil || rule.Macro != "" {
			return "", ErrInvalid
		}
		parts := make([]string, 0, len(rule.Args))
		for _, child := range rule.Args {
			sql, err := c.rule(child, depth+1)
			if err != nil {
				return "", err
			}
			parts = append(parts, "("+sql+")")
		}
		operator := " AND "
		if rule.Op == "any" {
			operator = " OR "
		}
		return "(" + strings.Join(parts, operator) + ")", nil
	case "eq", "ne", "lt", "lte", "gt", "gte", "contains", "not_contains", "any_eq", "any_ne", "any_lt", "any_lte", "any_gt", "any_gte", "any_contains", "any_not_contains":
		if rule.Function != nil || rule.Macro != "" {
			return "", ErrInvalid
		}
		rhsCount := 0
		if len(rule.Value) > 0 {
			rhsCount++
		}
		if rule.Auth != "" {
			rhsCount++
		}
		if rule.OtherField != "" {
			rhsCount++
		}
		if len(rule.Args) != 0 || rule.Field == "" || rhsCount != 1 {
			return "", ErrInvalid
		}
		column, fieldType, err := c.fieldForValue(rule.Field, rule.Value)
		if err != nil {
			return "", err
		}
		baseOp := strings.TrimPrefix(rule.Op, "any_")
		ordered := baseOp != "eq" && baseOp != "ne" && baseOp != "contains" && baseOp != "not_contains"
		if ordered && (fieldType == schema.TypeBoolean || fieldType == schema.TypeUUID) {
			return "", ErrInvalid
		}
		var rhs string
		if rule.OtherField != "" {
			other, otherType, otherErr := c.field(rule.OtherField)
			if otherErr != nil || otherType != fieldType {
				return "", ErrInvalid
			}
			rhs = other
		} else if rule.Auth != "" {
			switch rule.Auth {
			case "id":
				if fieldType != schema.TypeUUID {
					return "", ErrInvalid
				}
				rhs = c.parameter(c.principal.ID)
			case "verified":
				if fieldType != schema.TypeBoolean {
					return "", ErrInvalid
				}
				rhs = "COALESCE((SELECT verified FROM pgbase_meta.users WHERE id=" + c.parameter(c.principal.ID) + "),false)"
			case "collectionId":
				if fieldType != schema.TypeUUID {
					return "", ErrInvalid
				}
				rhs = "(SELECT auth_collection_id FROM pgbase_meta.users WHERE id=" + c.parameter(c.principal.ID) + ")"
			default:
				return "", ErrInvalid
			}
		} else {
			value, err := typedValue(fieldType, rule.Value)
			if err != nil {
				return "", err
			}
			if value == nil {
				if ordered {
					return "", ErrInvalid
				}
				op := " IS NULL"
				if baseOp == "ne" {
					op = " IS NOT NULL"
				}
				return column + op, nil
			}
			rhs = c.parameter(value)
		}
		if baseOp == "contains" || baseOp == "not_contains" {
			if fieldType != schema.TypeText && fieldType != schema.TypeEditor {
				return "", ErrInvalid
			}
			op := "LIKE"
			if baseOp == "not_contains" {
				op = "NOT LIKE"
			}
			return "COALESCE(" + column + " " + op + " ('%' || " + rhs + " || '%') ESCAPE '\\\\',false)", nil
		}
		operator := map[string]string{"eq": "=", "ne": "<>", "lt": "<", "lte": "<=", "gt": ">", "gte": ">="}[baseOp]
		return "COALESCE(" + column + " " + operator + " " + rhs + ",false)", nil
	}
	return "", ErrInvalid
}

func (c *compiler) fieldForValue(name string, raw json.RawMessage) (string, schema.FieldType, error) {
	column, kind, err := c.field(name)
	if err == nil {
		return column, kind, nil
	}
	segments := strings.Split(name, ".")
	if len(segments) < 2 {
		return "", "", err
	}
	var root schema.Field
	for _, field := range c.collection.Fields {
		if field.Name == segments[0] {
			root = field
			break
		}
	}
	if root.Type != schema.TypeJSONB || root.Hidden {
		return "", "", ErrInvalid
	}
	for _, segment := range segments[1:] {
		if segment == "" || len(segment) > 48 {
			return "", "", ErrInvalid
		}
		for _, ch := range segment {
			if !(ch == '_' || ch >= 'a' && ch <= 'z' || ch >= 'A' && ch <= 'Z' || ch >= '0' && ch <= '9') {
				return "", "", ErrInvalid
			}
		}
	}
	path := strings.Join(segments[1:], ",")
	expr := "(" + c.alias + "." + pgx.Identifier{root.ColumnName}.Sanitize() + "#>>'{" + path + "}')"
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) > 0 && trimmed[0] != '"' && string(trimmed) != "null" && string(trimmed) != "true" && string(trimmed) != "false" {
		return "NULLIF(" + expr + ",'')::numeric", schema.TypeNumeric, nil
	}
	if string(trimmed) == "true" || string(trimmed) == "false" {
		return "NULLIF(" + expr + ",'')::boolean", schema.TypeBoolean, nil
	}
	return expr, schema.TypeText, nil
}

func (c *compiler) field(name string) (string, schema.FieldType, error) {
	if strings.HasPrefix(name, "__pgbase_request_") {
		if c.request == nil {
			return "", "", ErrInvalid
		}
		return c.requestField(name)
	}
	modifier := ""
	if strings.Contains(name, ":") {
		if strings.Count(name, ":") != 1 {
			return "", "", ErrInvalid
		}
		name, modifier, _ = strings.Cut(name, ":")
		if modifier == "isset" || modifier == "changed" {
			if c.request == nil {
				return "", "", ErrInvalid
			}
			value := c.request.Present[name]
			if modifier == "changed" {
				value = c.request.Changed[name]
			}
			return c.parameter(value) + "::boolean", schema.TypeBoolean, nil
		}
	}
	switch name {
	case "id":
		return c.ruleFieldExpression(c.alias+`."id"`, schema.TypeUUID, modifier)
	case "created_at", "updated_at":
		return c.ruleFieldExpression(c.alias+"."+pgx.Identifier{name}.Sanitize(), schema.TypeTimestampTZ, modifier)
	}
	path := strings.Split(name, ".")
	for _, field := range c.collection.Fields {
		if field.Name == name {
			switch field.Type {
			case schema.TypeText, schema.TypeEditor, schema.TypeBoolean, schema.TypeInteger, schema.TypeBigInt, schema.TypeNumeric, schema.TypeUUID, schema.TypeTimestampTZ, schema.TypeAutoDate:
				return c.ruleFieldExpression(c.alias+"."+pgx.Identifier{field.ColumnName}.Sanitize(), field.Type, modifier)
			}
		}
		if len(path) == 2 && field.Name == path[0] && field.Type == schema.TypeGeoPoint && (path[1] == "lon" || path[1] == "lat") {
			column := "((" + c.alias + "." + pgx.Identifier{field.ColumnName}.Sanitize() + "->>'" + path[1] + "')::numeric)"
			return c.ruleFieldExpression(column, schema.TypeNumeric, modifier)
		}
	}
	return "", "", ErrInvalid
}

func (c *compiler) requestField(name string) (string, schema.FieldType, error) {
	if name == "__pgbase_request_method" {
		return c.parameter(c.request.Method), schema.TypeText, nil
	}
	lookup := func(prefix string, values map[string][]string) (string, schema.FieldType, error) {
		if !strings.HasPrefix(name, prefix) {
			return "", "", ErrInvalid
		}
		key := strings.TrimPrefix(name, prefix)
		if key == "" {
			return "", "", ErrInvalid
		}
		items := values[key]
		if len(items) == 0 {
			return c.parameter(""), schema.TypeText, nil
		}
		return c.parameter(items[0]), schema.TypeText, nil
	}
	if strings.HasPrefix(name, "__pgbase_request_query_") {
		return lookup("__pgbase_request_query_", c.request.Query)
	}
	if strings.HasPrefix(name, "__pgbase_request_header_") {
		return lookup("__pgbase_request_header_", c.request.Headers)
	}
	if strings.HasPrefix(name, "__pgbase_request_context_") {
		key := strings.TrimPrefix(name, "__pgbase_request_context_")
		value, ok := c.request.Context[key]
		if !ok {
			value = ""
		}
		return c.parameter(fmt.Sprint(value)), schema.TypeText, nil
	}
	if strings.HasPrefix(name, "__pgbase_request_body_") {
		key := strings.TrimPrefix(name, "__pgbase_request_body_")
		modifier := ""
		if strings.Contains(key, ":") {
			key, modifier, _ = strings.Cut(key, ":")
		}
		if modifier == "isset" {
			return c.parameter(c.request.Present[key]), schema.TypeBoolean, nil
		}
		if modifier == "changed" {
			return c.parameter(c.request.Changed[key]), schema.TypeBoolean, nil
		}
		var field schema.Field
		for _, candidate := range c.collection.Fields {
			if candidate.Name == key {
				field = candidate
				break
			}
		}
		if field.Name == "" {
			return "", "", ErrInvalid
		}
		raw, ok := c.request.Body[key]
		if !ok {
			return c.parameter(nil), field.Type, nil
		}
		value, err := typedValue(field.Type, raw)
		if err != nil {
			return "", "", err
		}
		return c.parameter(value), field.Type, nil
	}
	return "", "", ErrInvalid
}

func (c *compiler) ruleFieldExpression(column string, kind schema.FieldType, modifier string) (string, schema.FieldType, error) {
	if modifier == "" {
		return column, kind, nil
	}
	if kind != schema.TypeText && kind != schema.TypeEditor {
		return "", "", ErrInvalid
	}
	if modifier == "lower" {
		return "lower(" + column + ")", kind, nil
	}
	if modifier == "length" {
		return "char_length(" + column + ")", schema.TypeInteger, nil
	}
	return "", "", ErrInvalid
}

func typedValue(kind schema.FieldType, raw json.RawMessage) (any, error) {
	if len(raw) > 4096 {
		return nil, ErrInvalid
	}
	if string(bytes.TrimSpace(raw)) == "null" {
		return nil, nil
	}
	switch kind {
	case schema.TypeBoolean:
		var value bool
		if json.Unmarshal(raw, &value) != nil {
			return nil, ErrInvalid
		}
		return value, nil
	case schema.TypeInteger, schema.TypeBigInt:
		var value int64
		if json.Unmarshal(raw, &value) != nil {
			return nil, ErrInvalid
		}
		if kind == schema.TypeInteger && (value < -2147483648 || value > 2147483647) {
			return nil, ErrInvalid
		}
		return value, nil
	case schema.TypeNumeric:
		var value json.Number
		decoder := json.NewDecoder(bytes.NewReader(raw))
		decoder.UseNumber()
		if decoder.Decode(&value) != nil {
			return nil, ErrInvalid
		}
		if _, err := value.Float64(); err != nil {
			return nil, ErrInvalid
		}
		return value.String(), nil
	case schema.TypeText, schema.TypeEditor, schema.TypeTimestampTZ, schema.TypeAutoDate, schema.TypeUUID:
		var value string
		if json.Unmarshal(raw, &value) != nil {
			return nil, ErrInvalid
		}
		if kind == schema.TypeUUID {
			parsed, err := uuid.Parse(value)
			if err != nil {
				return nil, ErrInvalid
			}
			return parsed, nil
		}
		if kind == schema.TypeTimestampTZ || kind == schema.TypeAutoDate {
			if _, err := time.Parse(time.RFC3339Nano, value); err != nil {
				return nil, ErrInvalid
			}
		}
		return value, nil
	}
	return nil, fmt.Errorf("%w: unsupported field type", ErrInvalid)
}

// CompileEvent uses the immutable audience captured before a deleted row lost
// its ownership metadata. The alias is quoted as an identifier, never SQL.
func CompileEvent(rule Rule, collection schema.Collection, alias string, principal identity.Subject, offset int, eventAlias string) (string, []any, error) {
	c := compiler{collection: collection, alias: pgx.Identifier{alias}.Sanitize(), principal: principal, offset: offset, eventAlias: pgx.Identifier{eventAlias}.Sanitize()}
	sql, err := c.rule(rule, 0)
	return sql, c.args, err
}
