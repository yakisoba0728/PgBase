package recordops

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/webisnoteasy/PgBase/internal/database"
)

const maximumBatchOperations = 50

// Reserve framing for event IDs, ETags, versions and the outer JSON envelope.
const maximumBatchResponseBytes = (4 << 20) - (32 << 10)

// Batch executes ordered mutations in one PostgreSQL transaction. Existing
// mutation kernels detect the transaction context and never open a nested tx.
func (s *Service) Batch(ctx context.Context, request BatchRequest) (results []MutationResult, err error) {
	if err := s.validateRequest(request.Principal, request.RequestID); err != nil {
		return nil, err
	}
	if len(request.Operations) == 0 || len(request.Operations) > maximumBatchOperations {
		return nil, &Error{Code: CodeInvalidRequest}
	}
	ctx, hooks := s.hookContext(ctx)
	if hooks != nil {
		hooks.deferAfter = true
		defer func() { hooks.deferAfter = false; hooks.afterCommitted(ctx, &err) }()
	}
	err = database.Retry(ctx, func() error {
		if hooks != nil {
			hooks.events = nil
		}
		results = nil
		responseBytes := 0
		// Delete graph discovery must see a relation writer that committed while
		// this batch waited on its ordered relation locks. Read Committed keeps
		// that first reverse-discovery query fresh; individual record locks and
		// the retry wrapper still protect write conflicts.
		return s.pool.InTx(ctx, pgx.TxOptions{IsoLevel: pgx.ReadCommitted}, func(tx pgx.Tx) error {
			txContext := context.WithValue(ctx, batchTransactionKey{}, tx)
			for index, operation := range request.Operations {
				if operation.kind() == "" || (operation.Create != nil && (operation.Update != nil || operation.Delete != nil)) ||
					(operation.Update != nil && operation.Delete != nil) {
					return &Error{Code: CodeInvalidRequest}
				}
				result, err := s.batchOperation(txContext, request, operation)
				if err != nil {
					return fmt.Errorf("batch operation %d: %w", index, err)
				}
				results = append(results, result)
				encoded, err := json.Marshal(result.Record)
				if err != nil {
					return err
				}
				responseBytes += len(encoded)
				if responseBytes > maximumBatchResponseBytes {
					return &Error{Code: CodeInvalidRequest}
				}
			}
			return nil
		})
	}, nil)
	if err != nil {
		return nil, membershipBoundaryError(err)
	}
	return results, nil
}

func (s *Service) batchOperation(ctx context.Context, batch BatchRequest, operation BatchOperation) (MutationResult, error) {
	if operation.Create != nil {
		request := *operation.Create
		request.Principal, request.RequestID = batch.Principal, batch.RequestID
		return s.Create(ctx, request)
	}
	if operation.Update != nil {
		request := *operation.Update
		request.Principal, request.RequestID = batch.Principal, batch.RequestID
		return s.Update(ctx, request)
	}
	request := *operation.Delete
	request.Principal, request.RequestID = batch.Principal, batch.RequestID
	return s.Delete(ctx, request)
}
