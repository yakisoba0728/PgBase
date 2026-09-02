package query

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/webisnoteasy/PgBase/internal/database"
	"github.com/webisnoteasy/PgBase/internal/membership"
	"github.com/webisnoteasy/PgBase/internal/observability"
	"github.com/webisnoteasy/PgBase/internal/schema"
)

func readTelemetryOperation(expand string) observability.Operation {
	if expand != "" {
		return observability.OperationRelationExpand
	}
	return observability.OperationRelationRead
}

func requestTextAddressesRelation(expand, filter, sort string) bool {
	if expand != "" || strings.Contains(sort, ".") {
		return true
	}
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
			for index < len(filter) && (filterWordByte(filter[index]) || filter[index] == '.') {
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

func filterWordByte(current byte) bool {
	return current == '_' || (current >= 'a' && current <= 'z') ||
		(current >= 'A' && current <= 'Z') || (current >= '0' && current <= '9')
}

func (s *Service) finishReadOperation(
	ctx context.Context,
	operation observability.Operation,
	started time.Time,
	operationErr *error,
	enabled *bool,
) {
	if recovered := recover(); recovered != nil {
		if enabled != nil && *enabled {
			s.record(ctx, observability.OperationEvent(
				operation, observability.OutcomeFailure, time.Since(started),
			))
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
	s.record(ctx, observability.OperationEvent(
		operation, readOperationOutcome(err), time.Since(started),
	))
}

func readOperationOutcome(err error) observability.Outcome {
	if err == nil {
		return observability.OutcomeSuccess
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return observability.OutcomeCanceled
	}
	var validationError *ValidationError
	if errors.As(err, &validationError) {
		for _, detail := range validationError.Details {
			if strings.HasSuffix(detail.Code, "_budget_exceeded") || detail.Code == "response_too_large" {
				return observability.OutcomeBudget
			}
		}
		return observability.OutcomeValidation
	}
	if errors.Is(err, schema.ErrConflict) {
		return observability.OutcomeConflict
	}
	if errors.Is(err, membership.ErrUnavailable) {
		return observability.OutcomeUnavailable
	}
	if errors.Is(err, ErrCollectionNotFound) || errors.Is(err, ErrRecordNotFound) || IsInvalidCursor(err) {
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
