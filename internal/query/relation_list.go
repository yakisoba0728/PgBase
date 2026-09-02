package query

import (
	"context"
	"encoding/json"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/webisnoteasy/PgBase/internal/schema"
)

// requestAddressesRelationField mirrors the operand classification that
// planRelationFilter and planRelationSort perform, so an operation is counted
// as relation work whether the plan then succeeds or is rejected.
//
// A filter resolves against the catalog as a relation path for a dotted path
// and for the bare name of a root relation field alike. A sort does so only
// for a dotted path: a bare single relation orders on the root's own column
// and a bare multi relation is refused from its cardinality, so neither reads
// a target. An unknown bare name is a plain validation error and stays scalar.
func requestAddressesRelationField(root schema.Collection, filter filterNode, sortKeys []SortKey) bool {
	for _, key := range sortKeys {
		if strings.Contains(key.Field, ".") {
			return true
		}
	}
	return filterAddressesRelationField(root, filter)
}

func filterAddressesRelationField(root schema.Collection, node filterNode) bool {
	switch value := node.(type) {
	case filterAnd:
		return filterGroupAddressesRelationField(root, value.children)
	case filterOr:
		return filterGroupAddressesRelationField(root, value.children)
	case filterComparison:
		return operandAddressesRelationField(root, value.field)
	default:
		return false
	}
}

func filterGroupAddressesRelationField(root schema.Collection, children []filterNode) bool {
	for _, child := range children {
		if filterAddressesRelationField(root, child) {
			return true
		}
	}
	return false
}

func operandAddressesRelationField(root schema.Collection, name string) bool {
	if strings.Contains(name, ".") {
		return true
	}
	field, ok := filterField(root, name)
	return ok && field.Type == schema.TypeRelation
}

// enforceRelationSortRootBudget caps the work of the LEFT-JOIN sort plan.
// The probe reads at most limit+1 primary-key entries in the same repeatable-
// read snapshot; oversized collections fail before target joins or sorting.
func enforceRelationSortRootBudget(ctx context.Context, tx pgx.Tx, collection schema.Collection) error {
	statement := "SELECT EXISTS (SELECT 1 FROM " + quotedTable(collection) +
		" ORDER BY " + quoted(systemFieldID) + " OFFSET $1 LIMIT 1)"
	var exceeded bool
	if err := tx.QueryRow(ctx, statement, maximumRelationSortRootRows).Scan(&exceeded); err != nil {
		return err
	}
	if exceeded {
		return relationSortBudgetExceeded()
	}
	return nil
}

func relationListPlanUsesRelation(plan relationListPlan) bool {
	if plan.order.private() {
		return true
	}
	var visit func(*plannedRelationFilter) bool
	visit = func(node *plannedRelationFilter) bool {
		if node == nil {
			return false
		}
		if node.comparison != nil && node.comparison.path != nil {
			return true
		}
		for _, child := range node.children {
			if visit(child) {
				return true
			}
		}
		return false
	}
	return visit(plan.filter)
}

type relationRecordScanner struct {
	row          RowScanner
	order        orderSpec
	hiddenFields []schema.Field
	hidden       []any
}

func newRelationRecordScanner(row RowScanner, order orderSpec) (*relationRecordScanner, error) {
	scanner := &relationRecordScanner{row: row, order: order}
	for _, term := range order.terms {
		if term.relationPath == nil {
			continue
		}
		destination, err := scanDestination(term.cursorField)
		if err != nil {
			return nil, err
		}
		scanner.hiddenFields = append(scanner.hiddenFields, term.cursorField)
		scanner.hidden = append(scanner.hidden, destination)
	}
	return scanner, nil
}

func (s *relationRecordScanner) Scan(destinations ...any) error {
	return s.row.Scan(append(destinations, s.hidden...)...)
}

func (s *relationRecordScanner) cursorValues(record map[string]any) ([]json.RawMessage, error) {
	valuesByName := cloneRecord(record)
	hidden := 0
	for _, term := range s.order.terms {
		if term.relationPath == nil {
			continue
		}
		value, err := encodeScannedValue(s.hiddenFields[hidden], s.hidden[hidden])
		if err != nil {
			return nil, err
		}
		valuesByName[term.key.Field] = value
		hidden++
	}
	return s.order.recordValues(valuesByName)
}

func (s orderSpec) scanValues(row RowScanner) ([]json.RawMessage, error) {
	destinations := make([]any, len(s.terms))
	for index := range s.terms {
		destination, err := scanDestination(s.terms[index].cursorField)
		if err != nil {
			return nil, err
		}
		destinations[index] = destination
	}
	if err := row.Scan(destinations...); err != nil {
		return nil, err
	}
	values := make([]json.RawMessage, len(s.terms))
	for index := range s.terms {
		value, err := encodeScannedValue(s.terms[index].cursorField, destinations[index])
		if err != nil {
			return nil, err
		}
		values[index], err = json.Marshal(value)
		if err != nil {
			return nil, err
		}
	}
	return values, nil
}

func cloneCursorValues(values []json.RawMessage) []json.RawMessage {
	cloned := make([]json.RawMessage, len(values))
	for index := range values {
		cloned[index] = append(json.RawMessage(nil), values[index]...)
	}
	return cloned
}
