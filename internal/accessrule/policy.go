package accessrule

import (
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/yakisoba0728/PgBase/internal/identity"
	"github.com/yakisoba0728/PgBase/internal/schema"
)

// PolicyContext contains trusted execution bindings, never client SQL.
type PolicyContext struct {
	Catalog          schema.Catalog
	Collection       schema.Collection
	Alias            string
	Principal        identity.Subject
	Request          *RequestContext
	ArgOffset        int
	ProspectiveOwner bool
	EventAlias       string
}

// CompilePolicy binds the typed AST independently of its source metadata.
// Composite policies may combine ownership and catalog-aware operands.
func CompilePolicy(rule Rule, context PolicyContext) (string, []any, error) {
	return compilePolicy(rule, context, false)
}

// ValidatePolicy checks bindings without requiring execution-time request values.
func ValidatePolicy(rule Rule, context PolicyContext) error {
	context.Request = &RequestContext{}
	_, _, err := compilePolicy(rule, context, true)
	return err
}

func compilePolicy(rule Rule, context PolicyContext, validationOnly bool) (string, []any, error) {
	if context.Alias == "" || context.ArgOffset < 0 || context.ArgOffset > 10000 {
		return "", nil, ErrInvalid
	}
	c := policyCompiler{context: context, validationOnly: validationOnly}
	sql, err := c.rule(rule, "", 0)
	return sql, c.args, err
}

type policyCompiler struct {
	context        PolicyContext
	resolver       Resolver
	args           []any
	validationOnly bool
	nodes          int
}

func (c *policyCompiler) rule(rule Rule, inherited string, depth int) (string, error) {
	c.nodes++
	if depth > 8 || c.nodes > 64 || (rule.Binding != "" && rule.Binding != BindingCatalog && rule.Binding != BindingScalar) {
		return "", ErrInvalid
	}
	binding := rule.Binding
	if binding == "" {
		binding = inherited
	}
	if rule.Op == "all" || rule.Op == "any" {
		if len(rule.Args) < 1 || len(rule.Args) > 16 || rule.Field != "" || rule.OtherField != "" || len(rule.Value) != 0 || rule.Auth != "" || rule.Function != nil || rule.Macro != "" {
			return "", ErrInvalid
		}
		parts := make([]string, len(rule.Args))
		for i, child := range rule.Args {
			sql, err := c.rule(child, binding, depth+1)
			if err != nil {
				return "", err
			}
			parts[i] = "(" + sql + ")"
		}
		join := " AND "
		if rule.Op == "any" {
			join = " OR "
		}
		return "(" + strings.Join(parts, join) + ")", nil
	}
	// Authority-only predicates always retain the ownership/event contract.
	authority := rule.Op == "owner" || rule.Op == "allow" || rule.Op == "deny" || rule.Op == "authenticated" || rule.Op == "unauthenticated"
	if len(rule.Args) != 0 {
		return "", ErrInvalid
	}
	if binding == BindingCatalog && !authority {
		if c.resolver == nil {
			var err error
			c.resolver, err = NewCatalogResolver(c.context.Catalog, c.context.Collection, c.context.Alias)
			if err != nil {
				return "", err
			}
		}
		sql, args, err := CompileExpression(rule, ExpressionContext{Resolver: c.resolver, Principal: c.context.Principal, Request: c.context.Request, ArgOffset: c.context.ArgOffset + len(c.args), validationOnly: c.validationOnly, outerAliases: []string{c.context.Alias, c.context.EventAlias}})
		c.args = append(c.args, args...)
		return sql, err
	}
	scalar := compiler{collection: c.context.Collection, alias: pgx.Identifier{c.context.Alias}.Sanitize(), principal: c.context.Principal,
		offset: c.context.ArgOffset + len(c.args), prospectiveOwner: c.context.ProspectiveOwner, request: c.context.Request}
	if c.context.EventAlias != "" {
		scalar.eventAlias = pgx.Identifier{c.context.EventAlias}.Sanitize()
	}
	sql, err := scalar.rule(rule, 0)
	c.args = append(c.args, scalar.args...)
	return sql, err
}
