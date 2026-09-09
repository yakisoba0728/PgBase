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
				members := 0
				if operation.Create != nil {
					members++
				}
				if operation.Update != nil {
					members++
				}
				if operation.Delete != nil {
					members++
				}
				if operation.Upsert != nil {
					members++
				}
				if operation.kind() == "" || members != 1 {
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
			for _, participant := range request.Participants {
				if participant == nil {
					return &Error{Code: CodeInvalidRequest}
				}
				if err := participant.CommitBatch(ctx, tx, results); err != nil {
					return err
				}
			}
			if err := s.validateTransaction(ctx, tx, request, results); err != nil {
				return err
			}
			catalog, err := s.schemas.EnsureCurrent(ctx, tx)
			if err != nil {
				return err
			}
			for index, operation := range request.Operations {
				name := operationCollection(operation)
				if collection, ok := catalog.Collections[name]; ok {
					projectMutationResult(collection, request.Principal, &results[index])
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

func operationCollection(operation BatchOperation) string {
	switch {
	case operation.Create != nil:
		return operation.Create.Collection
	case operation.Update != nil:
		return operation.Update.Collection
	case operation.Upsert != nil:
		return operation.Upsert.Collection
	case operation.Delete != nil:
		return operation.Delete.Collection
	}
	return ""
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
	if operation.Upsert != nil {
		request := *operation.Upsert
		request.Principal, request.RequestID = batch.Principal, batch.RequestID
		tx, _ := ctx.Value(batchTransactionKey{}).(pgx.Tx)
		var exists bool
		if err := tx.QueryRow(ctx, `select exists(select 1 from pgbase_meta.record_revisions r join pgbase_meta.collections c on c.id=r.collection_id where c.name=$1 and r.record_id=$2)`, request.Collection, request.ID).Scan(&exists); err != nil {
			return MutationResult{}, err
		}
		if exists {
			return s.Update(ctx, request)
		}
		return s.Create(ctx, CreateRequest{Principal: batch.Principal, RequestID: batch.RequestID, Collection: request.Collection, ID: request.ID, Input: request.Input})
	}
	request := *operation.Delete
	request.Principal, request.RequestID = batch.Principal, batch.RequestID
	return s.Delete(ctx, request)
}
