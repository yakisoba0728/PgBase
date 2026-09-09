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
	"github.com/webisnoteasy/PgBase/internal/identity"
	"github.com/webisnoteasy/PgBase/internal/schema"
)

var ErrInvalid = errors.New("invalid record access rule")

type Rule struct {
	Op    string          `json:"op"`
	Args  []Rule          `json:"args,omitempty"`
	Field string          `json:"field,omitempty"`
	Value json.RawMessage `json:"value,omitempty"`
	Auth  string          `json:"auth,omitempty"`
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
	case "allow", "deny", "owner", "authenticated":
		if len(rule.Args) > 0 || rule.Field != "" || len(rule.Value) > 0 || rule.Auth != "" {
			return "", ErrInvalid
		}
		switch rule.Op {
		case "allow":
			return "TRUE", nil
		case "deny":
			return "FALSE", nil
		case "authenticated":
			return c.parameter(c.principal.Kind == identity.User || c.principal.Kind == identity.Administrator || string(c.principal.Kind) == "service"), nil
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
		if len(rule.Args) < 1 || len(rule.Args) > 16 || rule.Field != "" || len(rule.Value) > 0 || rule.Auth != "" {
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
	case "eq", "ne", "lt", "lte", "gt", "gte":
		if len(rule.Args) != 0 || rule.Field == "" || (len(rule.Value) > 0) == (rule.Auth != "") {
			return "", ErrInvalid
		}
		column, fieldType, err := c.field(rule.Field)
		if err != nil {
			return "", err
		}
		ordered := rule.Op != "eq" && rule.Op != "ne"
		if ordered && (fieldType == schema.TypeBoolean || fieldType == schema.TypeUUID) {
			return "", ErrInvalid
		}
		var rhs string
		if rule.Auth != "" {
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
				if rule.Op == "ne" {
					op = " IS NOT NULL"
				}
				return column + op, nil
			}
			rhs = c.parameter(value)
		}
		operator := map[string]string{"eq": "=", "ne": "<>", "lt": "<", "lte": "<=", "gt": ">", "gte": ">="}[rule.Op]
		return "COALESCE(" + column + " " + operator + " " + rhs + ",false)", nil
	}
	return "", ErrInvalid
}

func (c *compiler) field(name string) (string, schema.FieldType, error) {
	switch name {
	case "id":
		return c.alias + `."id"`, schema.TypeUUID, nil
	case "created_at", "updated_at":
		return c.alias + "." + pgx.Identifier{name}.Sanitize(), schema.TypeTimestampTZ, nil
	}
	for _, field := range c.collection.Fields {
		if field.Name == name {
			switch field.Type {
			case schema.TypeText, schema.TypeEditor, schema.TypeBoolean, schema.TypeInteger, schema.TypeBigInt, schema.TypeNumeric, schema.TypeUUID, schema.TypeTimestampTZ, schema.TypeAutoDate:
				return c.alias + "." + pgx.Identifier{field.ColumnName}.Sanitize(), field.Type, nil
			}
		}
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
	case schema.TypeText, schema.TypeTimestampTZ, schema.TypeUUID:
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
