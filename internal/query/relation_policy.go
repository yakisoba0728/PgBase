package query

import (
	"context"
	"errors"
	"sort"
	"strconv"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/yakisoba0728/PgBase/internal/accessrule"
	"github.com/yakisoba0728/PgBase/internal/identity"
	"github.com/yakisoba0728/PgBase/internal/recordaccess"
	"github.com/yakisoba0728/PgBase/internal/schema"
)

type readPoliciesKey struct{}

type readGrant struct {
	collection uuid.UUID
	principal  Principal
	action     string
}

type readRules struct {
	rules    accessrule.Rules
	revision int64
}

type readPolicies struct {
	grants map[readGrant]struct{}
	rules  map[uuid.UUID]readRules
}

// Scope the cache to a single read attempt. The first check still locks the
// rows through the transaction, and a borrowed caller starts a new cache for
// each query so its updates are visible to later queries.
func withReadPolicies(ctx context.Context, principal Principal) context.Context {
	if principal.Kind == identity.Administrator {
		return ctx
	}
	return context.WithValue(ctx, readPoliciesKey{}, &readPolicies{
		grants: make(map[readGrant]struct{}),
		rules:  make(map[uuid.UUID]readRules),
	})
}

func authorizeReadSubject(ctx context.Context, tx pgx.Tx, collection uuid.UUID, principal Principal, action string) error {
	policies, _ := ctx.Value(readPoliciesKey{}).(*readPolicies)
	if policies == nil {
		return recordaccess.AuthorizeSubject(ctx, tx, collection, principal, action)
	}
	key := readGrant{collection, principal, action}
	if _, ok := policies.grants[key]; ok {
		return nil
	}
	if err := recordaccess.AuthorizeSubject(ctx, tx, collection, principal, action); err != nil {
		return err
	}
	policies.grants[key] = struct{}{}
	return nil
}

func loadReadRules(ctx context.Context, tx pgx.Tx, collection uuid.UUID) (accessrule.Rules, int64, error) {
	policies, _ := ctx.Value(readPoliciesKey{}).(*readPolicies)
	if policies == nil {
		return recordaccess.LoadRules(ctx, tx, collection)
	}
	if cached, ok := policies.rules[collection]; ok {
		return cached.rules, cached.revision, nil
	}
	rules, revision, err := recordaccess.LoadRules(ctx, tx, collection)
	if err == nil {
		policies.rules[collection] = readRules{rules, revision}
	}
	return rules, revision, err
}

// policyCollection retains the physical read relation while resolving owner
// and rule sidecars through a declarative view's source collection.
func policyCollection(collection schema.Collection) schema.Collection {
	collection.ID = collection.AccessCollectionID()
	return collection
}

func sourcePolicyCollection(catalog schema.Catalog, collection schema.Collection) (schema.Collection, error) {
	if !collection.IsView() || collection.IsSQLView() {
		return policyCollection(collection), nil
	}
	source, ok := catalogCollectionByID(catalog, collection.AccessCollectionID())
	if !ok {
		return schema.Collection{}, schema.ErrConflict
	}
	return source, nil
}

func bindRelationPolicies(ctx context.Context, tx pgx.Tx, plan *relationListPlan, principal Principal) error {
	bind := func(path *plannedRelationPath) error {
		if path == nil {
			return nil
		}
		for i := range path.hops {
			hop := &path.hops[i]
			policyTarget, err := sourcePolicyCollection(plan.catalog, hop.target)
			if err != nil {
				return err
			}
			hop.policyTarget = policyTarget
			if err := authorizeReadSubject(ctx, tx, hop.target.AccessCollectionID(), principal, "view"); err != nil {
				return err
			}
			if principal.Kind == identity.User || principal.Kind == identity.Anonymous {
				rules, revision, err := loadReadRules(ctx, tx, hop.target.AccessCollectionID())
				if err != nil {
					return err
				}
				hop.rule = &rules.View
				hop.ruleRevision = revision
				hop.principal = principal
			}
		}
		return nil
	}
	var visit func(*plannedRelationFilter) error
	visit = func(node *plannedRelationFilter) error {
		if node == nil {
			return nil
		}
		if node.comparison != nil {
			return bind(node.comparison.path)
		}
		for _, child := range node.children {
			if err := visit(child); err != nil {
				return err
			}
		}
		return nil
	}
	if err := visit(plan.filter); err != nil {
		return err
	}
	for i := range plan.order.terms {
		if err := bind(plan.order.terms[i].relationPath); err != nil {
			return err
		}
	}
	return nil
}

// bindRelationKeyVisibility masks every bare single-relation sort key for the
// principals whose relation values hydration filters by the target view rule.
// Without it the stored target id would order rows even when the response
// redacts it, disclosing which rows share a hidden target and how hidden
// targets order relative to each other. A target collection the principal may
// not view at all masks every key, as hydration redacts every value. A
// public view rule on a table target needs no mask: the deferred foreign key
// already guarantees every stored target exists, so the index order stays.
func bindRelationKeyVisibility(ctx context.Context, tx pgx.Tx, plan *relationListPlan, principal Principal) error {
	if principal.Kind != identity.User && principal.Kind != identity.Anonymous {
		return nil
	}
	for i := range plan.order.terms {
		term := &plan.order.terms[i]
		if term.relationPath != nil || term.identityCollectionID != uuid.Nil || term.random {
			continue
		}
		field, ok := filterField(plan.order.root, term.key.Field)
		if !ok || field.Type != schema.TypeRelation || field.Relation == nil || field.Relation.Cardinality != schema.RelationSingle {
			continue
		}
		target, ok := catalogCollectionByID(plan.catalog, field.Relation.TargetCollectionID)
		if !ok {
			return schema.ErrConflict
		}
		policyTarget, err := sourcePolicyCollection(plan.catalog, target)
		if err != nil {
			return err
		}
		hop := plannedRelationHop{field: field, target: target, policyTarget: policyTarget, principal: principal}
		switch err := authorizeReadSubject(ctx, tx, target.AccessCollectionID(), principal, "view"); {
		case errors.Is(err, recordaccess.ErrDenied):
			hop.rule = &accessrule.Rule{Op: "deny"}
		case err != nil:
			return err
		default:
			rules, _, err := loadReadRules(ctx, tx, target.AccessCollectionID())
			if err != nil {
				return err
			}
			if rules.View.Op == "allow" && !target.IsView() {
				continue
			}
			hop.rule = &rules.View
		}
		term.visibility = &hop
	}
	return nil
}

func relationPolicyRevisionString(plan relationListPlan) string {
	values := map[string]int64{}
	add := func(path *plannedRelationPath) {
		if path == nil {
			return
		}
		for _, hop := range path.hops {
			if hop.ruleRevision > 0 {
				values[hop.target.AccessCollectionID().String()] = hop.ruleRevision
			}
		}
	}
	var visit func(*plannedRelationFilter)
	visit = func(n *plannedRelationFilter) {
		if n == nil {
			return
		}
		if n.comparison != nil {
			add(n.comparison.path)
			return
		}
		for _, c := range n.children {
			visit(c)
		}
	}
	visit(plan.filter)
	for i := range plan.order.terms {
		add(plan.order.terms[i].relationPath)
	}
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	var b strings.Builder
	for _, key := range keys {
		b.WriteString("|hop:")
		b.WriteString(key)
		b.WriteByte(':')
		b.WriteString(strconv.FormatInt(values[key], 10))
	}
	return b.String()
}

// bindOutputRuleScope pins every rule that can redact a relation value in the
// response. The cursor then becomes invalid if any of those policies changes
// between pages, including rules on expanded records' raw relation fields.
func bindOutputRuleScope(ctx context.Context, tx pgx.Tx, catalog schema.Catalog, root schema.Collection, expansion expandPlan) (string, error) {
	collections := map[uuid.UUID]schema.Collection{}
	var addRelations func(schema.Collection) error
	addRelations = func(collection schema.Collection) error {
		for _, field := range collection.Fields {
			if field.Type != schema.TypeRelation || field.Relation == nil {
				continue
			}
			target, ok := catalogCollectionByID(catalog, field.Relation.TargetCollectionID)
			if !ok {
				return schema.ErrConflict
			}
			collections[target.AccessCollectionID()] = target
		}
		return nil
	}
	if err := addRelations(root); err != nil {
		return "", err
	}
	var visit func([]expandNode) error
	visit = func(nodes []expandNode) error {
		for i := range nodes {
			collections[nodes[i].target.AccessCollectionID()] = nodes[i].target
			if err := addRelations(nodes[i].target); err != nil {
				return err
			}
			if err := visit(nodes[i].children); err != nil {
				return err
			}
		}
		return nil
	}
	if err := visit(expansion.nodes); err != nil {
		return "", err
	}
	ids := make([]uuid.UUID, 0, len(collections))
	for id := range collections {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i].String() < ids[j].String() })
	var result strings.Builder
	for _, id := range ids {
		_, revision, err := loadReadRules(ctx, tx, id)
		if err != nil {
			return "", err
		}
		result.WriteString("|output:")
		result.WriteString(id.String())
		result.WriteByte(':')
		result.WriteString(strconv.FormatInt(revision, 10))
	}
	return result.String(), nil
}
