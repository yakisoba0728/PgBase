package recordops

import (
	"context"
	"encoding/json"
	"sort"

	"github.com/jackc/pgx/v5"
)

func (s *Service) SetTransactionValidators(validators []TransactionValidator) {
	s.validators = append([]TransactionValidator(nil), validators...)
}

func (s *Service) validateTransaction(ctx context.Context, tx pgx.Tx, request BatchRequest, results []MutationResult) error {
	for _, validator := range s.validators {
		if validator == nil {
			return &Error{Code: CodeInvalidRequest}
		}
		if err := validator.ValidateRecords(ctx, tx, request, results); err != nil {
			return err
		}
	}
	return nil
}

func mutationChangedFields(input map[string]json.RawMessage, sidecar []string) []string {
	fields := append(sortedInputFields(input), sidecar...)
	sort.Strings(fields)
	result := fields[:0]
	for _, field := range fields {
		if len(result) == 0 || result[len(result)-1] != field {
			result = append(result, field)
		}
	}
	return result
}
