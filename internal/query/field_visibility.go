package query

import (
	"strings"

	"github.com/yakisoba0728/PgBase/internal/identity"
	"github.com/yakisoba0728/PgBase/internal/schema"
)

func rejectHiddenQueryFields(catalog schema.Catalog, collection schema.Collection, principal Principal, filter filterNode, sort []SortKey) error {
	if principal.Kind == identity.Administrator {
		return nil
	}
	hidden := func(name string) bool {
		if field, ok := filterField(collection, strings.SplitN(name, ".", 2)[0]); ok && field.Hidden {
			return true
		}
		if !strings.Contains(name, ".") {
			return false
		}
		path, err := resolveRelationPath(catalog, collection, name, "filter")
		return err == nil && hiddenRelationPath(&path)
	}
	// Inspect only the caller's filter. Stored authorization rules deliberately
	// retain access to hidden fields and are compiled separately by the kernel.
	if filterReferencesField(filter, hidden) {
		return validation("filter", "hidden_field")
	}
	for _, key := range sort {
		if hidden(key.Field) {
			return validation("sort", "hidden_field")
		}
	}
	return nil
}

func filterReferencesField(node filterNode, matches func(string) bool) bool {
	switch value := node.(type) {
	case filterComparison:
		return matches(value.field) || matches(value.otherField) || filterFunctionReferencesField(value.function, matches)
	case filterAnd:
		for _, child := range value.children {
			if filterReferencesField(child, matches) {
				return true
			}
		}
	case filterOr:
		for _, child := range value.children {
			if filterReferencesField(child, matches) {
				return true
			}
		}
	}
	return false
}

func filterFunctionReferencesField(function *filterFunction, matches func(string) bool) bool {
	if function != nil {
		for _, argument := range function.args {
			if matches(argument.field) || filterFunctionReferencesField(argument.function, matches) {
				return true
			}
		}
	}
	return false
}

func hiddenRelationPath(path *plannedRelationPath) bool {
	if path == nil {
		return false
	}
	for _, hop := range path.hops {
		if hop.field.Hidden {
			return true
		}
	}
	return path.terminal.Hidden
}

func rejectHiddenRelationPlan(principal Principal, plan relationListPlan) error {
	if principal.Kind == identity.Administrator {
		return nil
	}
	var visit func(*plannedRelationFilter) bool
	visit = func(node *plannedRelationFilter) bool {
		if node == nil {
			return false
		}
		if node.comparison != nil {
			if node.comparison.rootField != nil && node.comparison.rootField.Hidden {
				return true
			}
			if field := node.comparison.otherRootField; field != nil && field.Hidden {
				return true
			}
			if hiddenRelationPath(node.comparison.path) {
				return true
			}
		}
		for _, child := range node.children {
			if visit(child) {
				return true
			}
		}
		return false
	}
	if visit(plan.filter) {
		return validation("filter", "hidden_field")
	}
	for _, term := range plan.order.terms {
		if term.cursorField.Hidden {
			return validation("sort", "hidden_field")
		}
		if hiddenRelationPath(term.relationPath) {
			return validation("sort", "hidden_field")
		}
	}
	return nil
}

func rejectHiddenExpand(principal Principal, nodes []expandNode) error {
	if principal.Kind == identity.Administrator {
		return nil
	}
	for _, node := range nodes {
		if node.field.Hidden {
			return validation("expand", "hidden_field")
		}
		if err := rejectHiddenExpand(principal, node.children); err != nil {
			return err
		}
	}
	return nil
}

func hideRecordFields(collection schema.Collection, principal Principal, record map[string]any) {
	if principal.Kind == identity.Administrator {
		return
	}
	for _, field := range collection.Fields {
		if field.Hidden {
			delete(record, field.Name)
		}
	}
}

func hideMaterializedFields(collection schema.Collection, nodes []expandNode, principal Principal, records []*materializedRecord) {
	if principal.Kind == identity.Administrator {
		return
	}
	for _, record := range records {
		hideRecordFields(collection, principal, record.root)
		for _, node := range nodes {
			value, ok := record.expand[node.name]
			if !ok {
				continue
			}
			if value.single != nil {
				hideMaterializedFields(node.target, node.children, principal, []*materializedRecord{value.single})
			}
			hideMaterializedFields(node.target, node.children, principal, value.many)
		}
	}
}
