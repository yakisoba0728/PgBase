package api

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/google/uuid"
	"github.com/webisnoteasy/PgBase/internal/lifecycle"
	"github.com/webisnoteasy/PgBase/internal/recordops"
	"github.com/webisnoteasy/PgBase/internal/schema"
)

type RecordRequestEvent struct {
	Actor          recordops.Principal
	RequestID      string
	Collection     string
	RecordID       uuid.UUID
	IdempotencyKey string
	Limit          int
	Cursor         string
	Filter         string
	Sort           string
	Expand         string
	Input          map[string]json.RawMessage
	Result         *recordops.MutationResult
}

type BatchRequestEvent struct {
	Actor      recordops.Principal
	RequestID  string
	Operations []recordops.BatchOperation
	Results    []recordops.MutationResult
}

func cloneMutationResult(result recordops.MutationResult) *recordops.MutationResult {
	cloned := result
	if result.Record == nil {
		return &cloned
	}
	record := *result.Record
	raw, err := json.Marshal(result.Record.Data)
	if err == nil {
		var data map[string]any
		if json.Unmarshal(raw, &data) == nil {
			record.Data = data
		}
	}
	cloned.Record = &record
	return &cloned
}

func cloneMutationResults(results []recordops.MutationResult) []recordops.MutationResult {
	cloned := make([]recordops.MutationResult, len(results))
	for index, result := range results {
		cloned[index] = *cloneMutationResult(result)
	}
	return cloned
}

type SchemaImportRequestEvent struct {
	Mode           string
	ActorID        uuid.UUID
	RequestID      string
	OperationID    uuid.UUID
	IdempotencyKey string
	BaseVersion    int64
	Checksum       string
	Document       schema.SchemaDocument
	Result         *schema.SchemaImportResult
}

func cloneSchemaDocument(document schema.SchemaDocument) schema.SchemaDocument {
	raw, err := json.Marshal(document)
	if err != nil {
		return schema.SchemaDocument{}
	}
	var cloned schema.SchemaDocument
	if json.Unmarshal(raw, &cloned) != nil {
		return schema.SchemaDocument{}
	}
	return cloned
}

func cloneSchemaImportResult(result schema.SchemaImportResult) *schema.SchemaImportResult {
	cloned := result
	return &cloned
}

func beginLifecycle(ctx context.Context, hooks *lifecycle.Registry, kind string, data any) (func(error), error) {
	if hooks == nil {
		return func(error) {}, nil
	}
	return hooks.Begin(ctx, &lifecycle.Event{Kind: kind, Data: data})
}

func finishLifecycle(finish func(error), operation func() error) (err error) {
	defer func() {
		if recovered := recover(); recovered != nil {
			finish(errors.New("request operation panicked"))
			panic(recovered)
		}
		finish(err)
	}()
	return operation()
}
