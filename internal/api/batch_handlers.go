package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"

	"github.com/google/uuid"
	"github.com/webisnoteasy/PgBase/internal/recordops"
)

const maximumBatchRequestBytes = 1 << 20

type batchRecordService interface {
	Batch(context.Context, recordops.BatchRequest) ([]recordops.MutationResult, error)
}

type batchInput struct {
	Operations []batchOperationInput `json:"operations"`
}

type batchOperationInput struct {
	Operation      string                     `json:"operation"`
	Collection     string                     `json:"collection"`
	ID             string                     `json:"id"`
	Input          map[string]json.RawMessage `json:"input"`
	IdempotencyKey string                     `json:"idempotencyKey"`
	IfMatch        string                     `json:"ifMatch"`
}

// batch is shared by administrator and public-user routes. The router applies
// each route's normal principal middleware before entering this handler.
func (e recordEndpoints) batch(w http.ResponseWriter, r *http.Request) {
	service, ok := e.service.(batchRecordService)
	if !ok || service == nil {
		writeRecordError(w, r, errors.New("batch record service is unavailable"))
		return
	}
	if r.ContentLength > maximumBatchRequestBytes {
		writeInvalidRecordRequest(w, r)
		return
	}
	var input batchInput
	if err := decodeStrictJSON(r, &input); err != nil || len(input.Operations) == 0 || len(input.Operations) > 50 {
		writeInvalidRecordRequest(w, r)
		return
	}
	principal, ok := recordPrincipal(w, r)
	if !ok {
		return
	}
	operations := make([]recordops.BatchOperation, 0, len(input.Operations))
	for _, item := range input.Operations {
		operation, ok := batchOperation(item)
		if !ok {
			writeInvalidRecordRequest(w, r)
			return
		}
		operations = append(operations, operation)
	}
	results, err := service.Batch(r.Context(), recordops.BatchRequest{
		Principal: principal, RequestID: requestID(r.Context()), Operations: operations,
	})
	if err != nil {
		writeRecordError(w, r, err)
		return
	}
	response := make([]batchResult, 0, len(results))
	for _, result := range results {
		item := batchResult{EventID: result.EventID.String()}
		if result.Record != nil {
			item.Record = result.Record.Data
			item.ETag = result.Record.ETag
			item.Version = result.Record.Version
		}
		response = append(response, item)
	}
	writeJSON(w, http.StatusOK, struct {
		Results []batchResult `json:"results"`
	}{response})
}

type batchResult struct {
	EventID string         `json:"eventId"`
	Record  map[string]any `json:"record,omitempty"`
	ETag    string         `json:"etag,omitempty"`
	Version int64          `json:"version,omitempty"`
}

func batchOperation(input batchOperationInput) (recordops.BatchOperation, bool) {
	switch input.Operation {
	case "create":
		if input.ID != "" || input.IfMatch != "" {
			return recordops.BatchOperation{}, false
		}
		return recordops.BatchOperation{Create: &recordops.CreateRequest{Collection: input.Collection, Input: input.Input, IdempotencyKey: input.IdempotencyKey}}, input.Collection != "" && input.Input != nil
	case "update", "delete":
		id, err := uuid.Parse(input.ID)
		if err != nil || id == uuid.Nil || input.Collection == "" {
			return recordops.BatchOperation{}, false
		}
		ifMatch, ok := parseIfMatch(http.Header{"If-Match": []string{input.IfMatch}})
		if input.IfMatch == "" {
			ifMatch = recordops.EntityTagSet{}
			ok = true
		}
		if !ok {
			return recordops.BatchOperation{}, false
		}
		if input.Operation == "delete" {
			if input.Input != nil || input.IdempotencyKey != "" {
				return recordops.BatchOperation{}, false
			}
			return recordops.BatchOperation{Delete: &recordops.DeleteRequest{Collection: input.Collection, ID: id, IfMatch: ifMatch}}, true
		}
		if input.IdempotencyKey != "" {
			return recordops.BatchOperation{}, false
		}
		return recordops.BatchOperation{Update: &recordops.UpdateRequest{Collection: input.Collection, ID: id, Input: input.Input, IfMatch: ifMatch}}, input.Input != nil
	default:
		return recordops.BatchOperation{}, false
	}
}
