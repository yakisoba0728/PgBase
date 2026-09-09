package api

import (
	"context"
	"encoding/json"

	"github.com/google/uuid"
	"github.com/webisnoteasy/PgBase/internal/recordops"
	"github.com/webisnoteasy/PgBase/internal/schema"
)

// LifecycleHooks is the process-local request lifecycle seam. Implementations
// must pair the returned finish function with the final service outcome.
// It deliberately contains no HTTP response writer, session, or credential.
type LifecycleHooks interface {
	Begin(context.Context, *LifecycleEvent) (func(error), error)
}

type LifecycleEvent struct {
	Kind string
	Data any
}

// RecordRequestEvent is emitted after route admission and before its typed
// record service call. The service revalidates the mutable input and performs
// collection/rule authorization after the hook. Actor, RequestID, record
// identity, and idempotency identity are copied for observation; changes to
// them are ignored.
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

// BatchRequestEvent is emitted once after the entire batch payload has been
// decoded and route-admitted, before the atomic batch service call. Operations
// are mutable and are used for the call. The service revalidates and authorizes
// every resulting operation. Actor and RequestID are observational copies.
// Results are populated only after the service has resolved commit or
// rollback/replay.
type BatchRequestEvent struct {
	Actor      recordops.Principal
	RequestID  string
	Operations []recordops.BatchOperation
	Results    []recordops.MutationResult
}

// SchemaImportRequestEvent is emitted once for a schema-document preview or
// apply after administrator admission. Preview uses a mutable Document; guarded
// apply exposes its document for observation and veto only, preserving the
// checksum and replay identity supplied by the caller. The actor, operation ID,
// idempotency key, base version and checksum are immutable observations. Result
// is populated after the schema service resolves its final outcome.
type SchemaImportRequestEvent struct {
	Mode           string
	ActorID        uuid.UUID
	OperationID    uuid.UUID
	IdempotencyKey string
	BaseVersion    int64
	Checksum       string
	Document       schema.SchemaDocument
	Result         *schema.SchemaImportResult
}

func beginLifecycle(ctx context.Context, hooks LifecycleHooks, kind string, data any) (func(error), error) {
	if hooks == nil {
		return func(error) {}, nil
	}
	return hooks.Begin(ctx, &LifecycleEvent{Kind: kind, Data: data})
}
