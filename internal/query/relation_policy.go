package query

import (
	"context"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/webisnoteasy/PgBase/internal/identity"
	"github.com/webisnoteasy/PgBase/internal/recordaccess"
	"github.com/webisnoteasy/PgBase/internal/schema"
	"sort"
	"strconv"
	"strings"
)

// policyCollection retains the physical read relation while resolving owner
// and rule sidecars through a declarative view's source collection.
func policyCollection(collection schema.Collection) schema.Collection {
	collection.ID = collection.AccessCollectionID()
	return collection
}

func sourcePolicyCollection(catalog schema.Catalog, collection schema.Collection) (schema.Collection, error) {
	if !collection.IsView() {
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
			if err := recordaccess.AuthorizeSubject(ctx, tx, hop.target.AccessCollectionID(), principal, "view"); err != nil {
				return err
			}
			if principal.Kind == identity.User || principal.Kind == identity.Anonymous {
				rules, revision, err := recordaccess.LoadRules(ctx, tx, hop.target.AccessCollectionID())
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
		_, revision, err := recordaccess.LoadRules(ctx, tx, id)
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
