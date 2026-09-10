package query

import (
	"github.com/yakisoba0728/PgBase/internal/identity"
	"github.com/yakisoba0728/PgBase/internal/schema"
	"strings"
)

func rejectHiddenQueryFields(collection schema.Collection, principal Principal, filter filterNode, sort []SortKey) error {
	if principal.Kind == identity.Administrator {
		return nil
	}
	hidden := map[string]bool{}
	for _, field := range collection.Fields {
		hidden[field.Name] = field.Hidden
	}
	var visit func(filterNode) bool
	visit = func(node filterNode) bool {
		switch value := node.(type) {
		case filterComparison:
			return hidden[strings.Split(value.field, ".")[0]]
		case filterAnd:
			for _, child := range value.children {
				if visit(child) {
					return true
				}
			}
		case filterOr:
			for _, child := range value.children {
				if visit(child) {
					return true
				}
			}
		}
		return false
	}
	if visit(filter) {
		return validation("filter", "hidden_field")
	}
	for _, key := range sort {
		if hidden[strings.Split(key.Field, ".")[0]] {
			return validation("sort", "hidden_field")
		}
	}
	return nil
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
			if path := node.comparison.path; path != nil {
				for _, hop := range path.hops {
					if hop.field.Hidden {
						return true
					}
				}
				if path.terminal.Hidden {
					return true
				}
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
		if term.relationPath != nil {
			for _, hop := range term.relationPath.hops {
				if hop.field.Hidden {
					return validation("sort", "hidden_field")
				}
			}
			if term.relationPath.terminal.Hidden {
				return validation("sort", "hidden_field")
			}
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
