package recordops

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/yakisoba0728/PgBase/internal/database"
	"github.com/yakisoba0728/PgBase/internal/observability"
	"github.com/yakisoba0728/PgBase/internal/query"
	"github.com/yakisoba0728/PgBase/internal/schema"
)

func (s *Service) finishRelationRecordOperation(
	ctx context.Context,
	operation observability.Operation,
	started time.Time,
	operationErr *error,
	enabled *bool,
) {
	if recovered := recover(); recovered != nil {
		if enabled != nil && *enabled {
			s.record(ctx, observability.OperationEvent(operation, observability.OutcomeFailure, time.Since(started)))
		}
		panic(recovered)
	}
	if enabled == nil || !*enabled {
		return
	}
	var err error
	if operationErr != nil {
		err = *operationErr
	}
	s.record(ctx, observability.OperationEvent(operation, relationOperationOutcome(err), time.Since(started)))
}

// relationReadOperation separates expansion from relation-aware filter and
// sort work so a sort or filter budget rejection is not counted against
// expansion, which has its own budgets and its own alerting shape.
func relationReadOperation(expand string) observability.Operation {
	if expand != "" {
		return observability.OperationRelationExpand
	}
	return observability.OperationRelationRead
}

// readTouchesRelationField reports the relation work a read request is known to
// reach from its text alone, the way the mutation paths compute the same flag
// from their own state. Expansion is explicit and dotted paths can only be
// relation paths, so a relation sort budget rejection is recorded whether or
// not the request also expands. A bare relation field name is indistinguishable
// from a scalar column without the catalog, so List additionally folds in what
// the reader reports once the catalog is pinned.
func readTouchesRelationField(expand, filter, sort string) bool {
	if expand != "" || strings.Contains(sort, ".") {
		return true
	}
	return filterTouchesRelationPath(filter)
}

// filterTouchesRelationPath scans filter operands without re-implementing the
// grammar: quoted literals are skipped whole, and only a word that starts a
// field name can carry the dot of a relation path.
func filterTouchesRelationPath(filter string) bool {
	for index := 0; index < len(filter); {
		switch character := filter[index]; {
		case character == '\'':
			index++
			for index < len(filter) && filter[index] != '\'' {
				index++
			}
			index++
		case character == '_' || (character >= 'a' && character <= 'z') || (character >= 'A' && character <= 'Z'):
			start := index
			for index < len(filter) && (isFilterWordByte(filter[index]) || filter[index] == '.') {
				index++
			}
			if strings.Contains(filter[start:index], ".") {
				return true
			}
		default:
			index++
		}
	}
	return false
}

func isFilterWordByte(current byte) bool {
	return current == '_' || (current >= 'a' && current <= 'z') ||
		(current >= 'A' && current <= 'Z') || (current >= '0' && current <= '9')
}

func inputTouchesRelationField(collection schema.Collection, input map[string]json.RawMessage) bool {
	for _, field := range collection.Fields {
		if field.Type != schema.TypeRelation {
			continue
		}
		if _, present := input[field.Name]; present {
			return true
		}
	}
	return false
}

func collectionParticipatesInRelations(catalog schema.Catalog, collection schema.Collection) bool {
	if len(catalog.Incoming[collection.ID]) != 0 {
		return true
	}
	for _, field := range collection.Fields {
		if field.Type == schema.TypeRelation || field.Relation != nil {
			return true
		}
	}
	return false
}

// relationBudgetCode reports whether a validation code is a bounded-guard
// rejection. The response-size guard is the one budget that does not carry the
// shared suffix, and PgBaseRelationBudgetExceeded alerts on it like the rest.
func relationBudgetCode(code string) bool {
	return strings.HasSuffix(code, "_budget_exceeded") || code == "response_too_large"
}

func relationOperationOutcome(err error) observability.Outcome {
	if err == nil {
		return observability.OutcomeSuccess
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return observability.OutcomeCanceled
	}
	var validationError *query.ValidationError
	if errors.As(err, &validationError) {
		for _, detail := range validationError.Details {
			if relationBudgetCode(detail.Code) {
				return observability.OutcomeBudget
			}
		}
		return observability.OutcomeValidation
	}
	var operationError *Error
	if errors.As(err, &operationError) {
		switch operationError.Code {
		case CodeIdempotencyConflict, CodeIdempotencyInProgress, CodePreconditionFailed:
			return observability.OutcomeConflict
		case CodeUnavailable:
			if operationError.canceled {
				return observability.OutcomeCanceled
			}
			return observability.OutcomeUnavailable
		default:
			return observability.OutcomeValidation
		}
	}
	if errors.Is(err, schema.ErrConflict) {
		return observability.OutcomeConflict
	}
	if errors.Is(err, query.ErrCollectionNotFound) || errors.Is(err, query.ErrRecordNotFound) || query.IsInvalidCursor(err) {
		return observability.OutcomeValidation
	}
	switch database.Classify(err) {
	case database.KindValidation:
		return observability.OutcomeValidation
	case database.KindConflict:
		return observability.OutcomeConflict
	case database.KindUnavailable:
		return observability.OutcomeUnavailable
	case database.KindCanceled:
		return observability.OutcomeCanceled
	default:
		return observability.OutcomeFailure
	}
}
