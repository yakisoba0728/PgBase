package recordops

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/yakisoba0728/PgBase/internal/database"
)

// HTTP admission applies the published operator limit (50 by default).
// The transaction kernel must accommodate the full supported setting range.
const maximumBatchOperations = 100

// Match the escaped record-page budget. The HTTP layer reserves a further
// 32 KiB for event IDs, ETags, versions and the outer JSON envelope.
const maximumBatchResponseBytes = 48<<20 + 4<<10

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
		request.RuleContext = batch.RuleContext
		return s.Create(ctx, request)
	}
	if operation.Update != nil {
		request := *operation.Update
		request.Principal, request.RequestID = batch.Principal, batch.RequestID
		request.RuleContext = batch.RuleContext
		return s.Update(ctx, request)
	}
	if operation.Upsert != nil {
		request := *operation.Upsert
		request.Principal, request.RequestID = batch.Principal, batch.RequestID
		request.RuleContext = batch.RuleContext
		tx, _ := ctx.Value(batchTransactionKey{}).(pgx.Tx)
		var tableName string
		if err := tx.QueryRow(ctx, `select table_name from pgbase_meta.collections where name=$1`, request.Collection).Scan(&tableName); err != nil {
			if err == pgx.ErrNoRows {
				return s.Update(ctx, request)
			}
			return MutationResult{}, err
		}
		var exists bool
		if tableName == "" {
			return s.Update(ctx, request)
		}
		if err := tx.QueryRow(ctx, `select exists(select 1 from `+pgx.Identifier{"app", tableName}.Sanitize()+` where id=$1)`, request.ID).Scan(&exists); err != nil {
			return MutationResult{}, err
		}
		// Preserve conditional PUT semantics for an absent row. Creating here
		// would discard If-Match and could resurrect a concurrently deleted row.
		if exists || request.IfMatch.Present {
			return s.Update(ctx, request)
		}
		return s.Create(ctx, CreateRequest{Principal: batch.Principal, RequestID: batch.RequestID, Collection: request.Collection, ID: request.ID, Input: request.Input, RuleContext: batch.RuleContext})
	}
	request := *operation.Delete
	request.Principal, request.RequestID = batch.Principal, batch.RequestID
	request.RuleContext = batch.RuleContext
	return s.Delete(ctx, request)
}
