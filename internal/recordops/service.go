package recordops

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/yakisoba0728/PgBase/internal/accessrule"
	"github.com/yakisoba0728/PgBase/internal/database"
	"github.com/yakisoba0728/PgBase/internal/identity"
	"github.com/yakisoba0728/PgBase/internal/membership"
	"github.com/yakisoba0728/PgBase/internal/observability"
	"github.com/yakisoba0728/PgBase/internal/query"
	"github.com/yakisoba0728/PgBase/internal/recordaccess"
	"github.com/yakisoba0728/PgBase/internal/recordartifact"
	"github.com/yakisoba0728/PgBase/internal/relation"
	"github.com/yakisoba0728/PgBase/internal/schema"
	"io"
	"log/slog"
	"strings"
	"time"
)

const (
	maximumIfMatchBytes   = 1024
	maximumEntityTags     = 8
	outboxEnvelopeVersion = 1
)

// Service is the single supported writer for dynamic records. Query compilation
// remains in package query, while this type owns every transaction-level
// invariant around identity, revisions, audit, outbox, and replay.
type Service struct {
	pool       *database.Pool
	schemas    *schema.Service
	reader     *query.Service
	logger     *slog.Logger
	recorder   observability.Recorder
	random     io.Reader
	members    *membershipWriterProvider
	hooks      []MutationHook
	validators []TransactionValidator
}
type batchTransactionKey struct{}

// A nil runtime is not a disabled membership: the writer provider registers
// one on the first mutation, so a service built without the application's
// heartbeating runtime still claims a durable writer lease of the same release
// generation before it writes.
func NewService(
	pool *database.Pool,
	schemas *schema.Service,
	cursorSecret []byte,
	logger *slog.Logger,
	recorder observability.Recorder,
	runtime *membership.Runtime,
) *Service {
	return &Service{
		pool: pool, schemas: schemas,
		reader: query.NewService(pool, schemas, cursorSecret, logger, recorder),
		logger: logger, recorder: recorder, random: rand.Reader,
		members: newMembershipWriterProvider(pool, runtime),
	}
}

func (s *Service) Create(ctx context.Context, request CreateRequest) (result MutationResult, err error) {
	ctx, hooks := s.hookContext(ctx)
	defer hooks.after(ctx, &err)
	operationStarted := time.Now()
	relationOperation := false
	defer s.finishRelationRecordOperation(ctx, observability.OperationRelationWrite, operationStarted, &err, &relationOperation)
	if err := s.validateRequest(request.Principal, request.RequestID); err != nil {
		return MutationResult{}, err
	}
	canonicalRequest, err := canonicalInput(request.Input)
	if err != nil {
		return MutationResult{}, err
	}
	requestHash := recordartifact.Digest("pgbase-create-request-v1", []byte(request.Collection), canonicalRequest)
	if len(request.IdempotencyContext) > 0 && request.ID != uuid.Nil {
		requestHash = recordartifact.Digest("pgbase-create-request-v1", []byte(request.Collection), canonicalRequest, request.ID[:], request.IdempotencyContext)
	} else if len(request.IdempotencyContext) > 0 {
		requestHash = recordartifact.Digest("pgbase-create-request-v1", []byte(request.Collection), canonicalRequest, request.IdempotencyContext)
	} else if request.ID != uuid.Nil {
		requestHash = recordartifact.Digest("pgbase-create-request-v1", []byte(request.Collection), canonicalRequest, request.ID[:])
	}
	compatibility := CompatibilityNone
	var keyHash []byte
	if request.IdempotencyKey == "" {
		compatibility = CompatibilityMissingIdempotencyKey
	} else {
		if !validIdempotencyKey(request.IdempotencyKey) {
			return MutationResult{}, &Error{Code: CodeInvalidIdempotencyKey}
		}
		digest := recordartifact.Digest("pgbase-idempotency-key-v1", []byte(request.IdempotencyKey))
		keyHash = digest[:]
	}
	recordID := request.ID
	if recordID == uuid.Nil {
		recordID, err = uuid.NewV7()
		if err != nil {
			return MutationResult{}, fmt.Errorf("generate record id: %w", err)
		}
	}
	eventID, err := uuid.NewV7()
	if err != nil {
		return MutationResult{}, fmt.Errorf("generate record event id: %w", err)
	}
	validator, err := s.newValidator()
	if err != nil {
		return MutationResult{}, err
	}
	membershipWriter, err := s.members.writer(ctx)
	if err != nil {
		return MutationResult{}, unavailableMembership(err)
	}
	var idempotencyLock *int64
	if len(keyHash) > 0 {
		lock := advisoryLockKey(request.Principal, []byte(request.Collection), keyHash)
		idempotencyLock = &lock
	}
	err = s.createTx(ctx, request.Collection, idempotencyLock, func(tx pgx.Tx, catalog schema.Catalog, collection schema.Collection) error {
		if collection.IsView() {
			return recordaccess.ErrUnsupported
		}
		if err := rejectHiddenInput(collection, request.Principal, request.Input); err != nil {
			return err
		}
		if err := authorizeUserRecordOperation(ctx, tx, collection, request.Principal, uuid.Nil); err != nil {
			return err
		}
		relationOperation = inputTouchesRelationField(collection, request.Input)
		createFence, err := membershipWriter.LockCreate(ctx, tx, collection.ID)
		if err != nil {
			return transactionalMembership(err)
		}
		if len(keyHash) > 0 {
			replayed, found, err := findIdempotentCreate(ctx, tx, request.Principal, collection.ID, keyHash, requestHash[:])
			if err != nil {
				return err
			}
			if found {
				if request.Principal.Kind != PrincipalAdministrator {
					if err := authorizeStoredRelationTargets(ctx, tx, catalog, collection, request.Principal, replayed.Record.Data); err != nil {
						return err
					}
					id, ok := replayed.Record.Data["id"].(string)
					if !ok {
						return query.ErrRecordNotFound
					}
					recordID, err := uuid.Parse(id)
					if err != nil {
						return query.ErrRecordNotFound
					}
					requestContext := mutationRuleContext(collection, request.Input, nil)
					if err := authorizeUserRecordOperationWithRequest(ctx, tx, collection, request.Principal, recordID, "create", &requestContext); err != nil {
						return err
					}
				}
				result = replayed
				return nil
			}
		}
		mutationInput, err := s.prepareGeneratedFields(ctx, tx, collection, request.Input, true)
		if err != nil {
			return err
		}
		if err := createFence.Reserve(ctx); err != nil {
			return transactionalMembership(err)
		}
		relations, err := prepareRelationWrite(catalog, collection, relation.CreateWrite, mutationInput)
		if err != nil {
			return err
		}
		statement, err := query.CompileInsert(collection, relations.Scalar(), recordID)
		if err != nil {
			return err
		}
		if err := relations.Lock(ctx, tx, recordID); err != nil {
			return normalizeRelationError(err)
		}
		if err := authorizeRelationTargets(ctx, tx, catalog, collection, request.Principal, relations); err != nil {
			return err
		}
		data, err := query.EncodeRecord(collection, tx.QueryRow(ctx, statement.SQL, statement.Args...))
		if err != nil {
			return query.NormalizeRecordError(err)
		}
		requestContext := mutationRuleContext(collection, request.Input, nil)
		if err := authorizeUserRecordOperationWithRequest(ctx, tx, collection, request.Principal, recordID, "create", &requestContext); err != nil {
			return err
		}
		if err := createFence.RecordCreated(ctx, recordID); err != nil {
			return transactionalMembership(err)
		}
		if err := relations.Store(ctx, tx, recordID, data); err != nil {
			return normalizeRelationError(err)
		}
		if err := authorizeStoredRelationTargets(ctx, tx, catalog, collection, request.Principal, data); err != nil {
			return err
		}
		if err := schema.ValidateRecordSize(data); err != nil {
			return err
		}
		digest, err := recordartifact.SchemaDigest(collection)
		if err != nil {
			return err
		}
		etag := recordartifact.RevisionETag(collection.ID, recordID, digest, validator)
		if err := commitMutation(ctx, tx, request.Principal, request.RequestID, requestHash[:], artifactBatch{
			collection: collection, operation: "create",
			schemaVersion: catalog.Version, schemaDigest: digest,
			rows: []artifactRow{{
				eventID: eventID, recordID: recordID, newVersion: 1,
				newETag: etag, changedFields: committedUserFields(collection), changes: data,
				idempotencyKeyHash: keyHash, compatibility: compatibility, validator: validator,
				requestContext: &requestContext,
			}},
		}); err != nil {
			return err
		}
		if request.Principal.Kind == PrincipalUser {
			if _, err := tx.Exec(ctx, `INSERT INTO pgbase_meta.record_owners(collection_id, record_id, user_id) VALUES ($1,$2,$3)`, collection.ID, recordID, request.Principal.ID); err != nil {
				return err
			}
		}
		if len(keyHash) > 0 {
			body, err := recordartifact.CanonicalRecord(data)
			if err != nil {
				return err
			}
			if err := writeIdempotentCreate(ctx, tx, request.Principal, collection.ID, keyHash,
				requestHash[:], recordID, eventID, body, etag); err != nil {
				return err
			}
		}
		result = MutationResult{
			Record:  &Record{Data: data, ETag: etag, Version: 1},
			EventID: eventID, Compatibility: compatibility,
		}
		if _, batched := ctx.Value(batchTransactionKey{}).(pgx.Tx); !batched {
			request := BatchRequest{Principal: request.Principal, RequestID: request.RequestID, Operations: []BatchOperation{{Create: &request}}}
			if err := s.validateTransaction(ctx, tx, request, []MutationResult{result}); err != nil {
				return err
			}
			projectMutationResult(collection, request.Principal, &result)
		}
		return nil
	})
	if err != nil {
		return MutationResult{}, membershipBoundaryError(err)
	}
	return result, nil
}

func (s *Service) Update(ctx context.Context, request UpdateRequest) (result MutationResult, err error) {
	ctx, hooks := s.hookContext(ctx)
	defer hooks.after(ctx, &err)
	operationStarted := time.Now()
	relationOperation := false
	defer s.finishRelationRecordOperation(ctx, observability.OperationRelationWrite, operationStarted, &err, &relationOperation)
	if err := s.validateRequest(request.Principal, request.RequestID); err != nil {
		return MutationResult{}, err
	}
	if request.ID == uuid.Nil {
		return MutationResult{}, &Error{Code: CodeInvalidRequest}
	}
	canonicalRequest, err := canonicalInput(request.Input)
	if err != nil {
		return MutationResult{}, err
	}
	inputHash := recordartifact.Digest("pgbase-update-request-v1", []byte(request.Collection), request.ID[:], canonicalRequest)
	if len(request.SidecarContext) != 0 {
		inputHash = recordartifact.Digest("pgbase-update-request-v1", []byte(request.Collection), request.ID[:], canonicalRequest, request.SidecarContext)
	}
	eventID, err := uuid.NewV7()
	if err != nil {
		return MutationResult{}, fmt.Errorf("generate record event id: %w", err)
	}
	validator, err := s.newValidator()
	if err != nil {
		return MutationResult{}, err
	}
	membershipWriter, err := s.members.writer(ctx)
	if err != nil {
		return MutationResult{}, unavailableMembership(err)
	}
	compatibility := CompatibilityNone
	if !request.IfMatch.Present {
		compatibility = CompatibilityLastWriteWins
	}

	err = s.updateTx(ctx, request.Collection, func(tx pgx.Tx, catalog schema.Catalog, collection schema.Collection) error {
		if collection.IsView() {
			return recordaccess.ErrUnsupported
		}
		if err := rejectHiddenInput(collection, request.Principal, request.Input); err != nil {
			return err
		}
		mutationInput, err := s.prepareGeneratedFields(ctx, tx, collection, request.Input, false)
		if err != nil {
			return err
		}
		relationOperation = inputTouchesRelationField(collection, request.Input)
		if _, err := membershipWriter.Begin(ctx, tx); err != nil {
			return transactionalMembership(err)
		}
		relations, err := prepareRelationWrite(catalog, collection, relation.PatchWrite, mutationInput)
		if err != nil {
			return err
		}
		var statement query.Statement
		if len(request.SidecarFields) != 0 {
			statement, err = query.CompileUpdateWithSidecar(collection, request.ID, relations.Scalar())
		} else {
			statement, err = query.CompileUpdate(collection, request.ID, relations.Scalar())
		}
		if err != nil {
			return err
		}
		if err := relations.Lock(ctx, tx, request.ID); err != nil {
			return normalizeRelationError(err)
		}
		if err := authorizeRelationTargets(ctx, tx, catalog, collection, request.Principal, relations); err != nil {
			return err
		}
		before, err := readRecord(ctx, tx, collection, request.ID, !relations.LocksSource())
		if err != nil {
			return err
		}
		requestContext := mutationRuleContext(collection, request.Input, before)
		if err := authorizeUserRecordOperationWithRequest(ctx, tx, collection, request.Principal, request.ID, "update", &requestContext); err != nil {
			return err
		}
		if err := validateEntityTagSet(request.IfMatch); err != nil {
			return err
		}
		if err := relations.Snapshot(ctx, tx, request.ID, before); err != nil {
			return normalizeRelationError(err)
		}
		digest, err := recordartifact.SchemaDigest(collection)
		if err != nil {
			return err
		}
		previousVersion, previousValidator, revisionPresent, err := recordartifact.ReadRevision(ctx, tx, collection.ID, request.ID)
		if err != nil {
			return err
		}
		previousETag, err := recordartifact.ETag(collection.ID, request.ID, digest, before, previousValidator, revisionPresent)
		if err != nil {
			return err
		}
		if request.IfMatch.Present && !request.IfMatch.matches(previousETag) {
			return &Error{Code: CodePreconditionFailed, CurrentETag: previousETag}
		}

		after, err := query.EncodeRecord(collection, tx.QueryRow(ctx, statement.SQL, statement.Args...))
		if err != nil {
			return query.NormalizeRecordError(err)
		}
		if err := relations.Store(ctx, tx, request.ID, after); err != nil {
			return normalizeRelationError(err)
		}
		if err := authorizeStoredRelationTargets(ctx, tx, catalog, collection, request.Principal, after); err != nil {
			return err
		}
		if err := schema.ValidateRecordSize(after); err != nil {
			return err
		}
		newVersion := previousVersion + 1
		newETag := recordartifact.RevisionETag(collection.ID, request.ID, digest, validator)
		if err := commitMutation(ctx, tx, request.Principal, request.RequestID, inputHash[:], artifactBatch{
			collection: collection, operation: "update",
			schemaVersion: catalog.Version, schemaDigest: digest,
			rows: []artifactRow{{
				eventID: eventID, recordID: request.ID,
				previousVersion: &previousVersion, newVersion: newVersion,
				previousETag: previousETag, newETag: newETag,
				changedFields: mutationChangedFields(mutationInput, request.SidecarFields), changes: after,
				compatibility: compatibility, validator: validator,
				requestContext: &requestContext,
			}},
		}); err != nil {
			return err
		}
		result = MutationResult{
			Record:  &Record{Data: after, ETag: newETag, Version: newVersion},
			EventID: eventID, Compatibility: compatibility,
		}
		if _, batched := ctx.Value(batchTransactionKey{}).(pgx.Tx); !batched {
			request := BatchRequest{Principal: request.Principal, RequestID: request.RequestID, Operations: []BatchOperation{{Update: &request}}}
			if err := s.validateTransaction(ctx, tx, request, []MutationResult{result}); err != nil {
				return err
			}
			projectMutationResult(collection, request.Principal, &result)
		}
		return nil
	})
	if err != nil {
		return MutationResult{}, membershipBoundaryError(err)
	}
	return result, nil
}

func (s *Service) Delete(ctx context.Context, request DeleteRequest) (result MutationResult, err error) {
	ctx, hooks := s.hookContext(ctx)
	defer hooks.after(ctx, &err)
	operationStarted := time.Now()
	relationOperation := false
	defer s.finishRelationRecordOperation(ctx, observability.OperationRelationDelete, operationStarted, &err, &relationOperation)
	if err := s.validateRequest(request.Principal, request.RequestID); err != nil {
		return MutationResult{}, err
	}
	if request.ID == uuid.Nil {
		return MutationResult{}, &Error{Code: CodeInvalidRequest}
	}
	inputHash := recordartifact.Digest("pgbase-delete-request-v1", []byte(request.Collection), request.ID[:])
	eventID, err := uuid.NewV7()
	if err != nil {
		return MutationResult{}, fmt.Errorf("generate record event id: %w", err)
	}
	compatibility := CompatibilityNone
	if !request.IfMatch.Present {
		compatibility = CompatibilityLastWriteWins
	}
	identityCache := newDeleteArtifactIdentityCache(eventID)
	membershipWriter, err := s.members.writer(ctx)
	if err != nil {
		return MutationResult{}, unavailableMembership(err)
	}

	err = s.deleteTx(ctx, request.Collection, func(tx pgx.Tx, catalog schema.Catalog, collection schema.Collection) error {
		if collection.IsView() {
			return recordaccess.ErrUnsupported
		}
		if err := authorizeUserRecordOperation(ctx, tx, collection, request.Principal, request.ID, "delete"); err != nil {
			return err
		}
		relationOperation = collectionParticipatesInRelations(catalog, collection)
		writeFence, err := membershipWriter.Begin(ctx, tx)
		if err != nil {
			return transactionalMembership(err)
		}
		var operationErr error
		result, operationErr = s.deleteGraphInTransaction(
			ctx, tx, catalog, collection, request, identityCache, inputHash, compatibility, writeFence,
		)
		return operationErr
	})
	if err != nil {
		return MutationResult{}, membershipBoundaryError(err)
	}
	return result, nil
}

func authorizeUserRecordOperation(ctx context.Context, tx pgx.Tx, collection schema.Collection, principal Principal, recordID uuid.UUID, actions ...string) error {
	action := "view"
	if recordID == uuid.Nil {
		action = "create"
	}
	if len(actions) > 0 {
		action = actions[0]
	}
	return authorizeUserRecordOperationWithRequest(ctx, tx, collection, principal, recordID, action, nil)
}

func authorizeUserRecordOperationWithRequest(ctx context.Context, tx pgx.Tx, collection schema.Collection, principal Principal, recordID uuid.UUID, action string, request *accessrule.RequestContext) error {
	if principal.Kind == PrincipalAdministrator {
		return nil
	}
	if err := recordaccess.AuthorizeSubject(ctx, tx, collection.AccessCollectionID(), principal, action); err != nil {
		return err
	}
	if recordID == uuid.Nil || principal.Kind == identity.Service {
		return nil
	}
	allowed, err := recordaccess.AllowsRecordWithRequest(ctx, tx, policyCollection(collection), principal, action, recordID, action == "create", request)
	if err != nil {
		return err
	}
	if !allowed {
		return query.ErrRecordNotFound
	}
	return nil
}

func mutationRuleContext(collection schema.Collection, input map[string]json.RawMessage, before map[string]any) accessrule.RequestContext {
	result := accessrule.RequestContext{Present: make(map[string]bool, len(input)), Changed: make(map[string]bool, len(input))}
	for name, raw := range input {
		result.Present[name] = true
		if before == nil {
			result.Changed[name] = true
			continue
		}
		previous, exists := before[name]
		if !exists {
			result.Changed[name] = true
			continue
		}
		var field schema.Field
		for _, candidate := range collection.Fields {
			if candidate.Name == name {
				field = candidate
				break
			}
		}
		if field.Name == "" || !query.FieldInputEqualsStored(field, raw, previous) {
			result.Changed[name] = true
		}
	}
	return result
}

func (s *Service) validateRequest(principal Principal, requestID string) error {
	if s == nil || s.pool == nil || s.schemas == nil || s.reader == nil || s.random == nil {
		return errors.New("record operation service is unavailable")
	}
	if !principal.Valid() {
		return &Error{Code: CodeInvalidPrincipal}
	}
	if !validRequestID(requestID) {
		return &Error{Code: CodeInvalidRequest}
	}
	return nil
}

func validRequestID(value string) bool {
	if len(value) < 8 || len(value) > 128 {
		return false
	}
	for _, character := range value {
		if !(character >= 'a' && character <= 'z') && !(character >= 'A' && character <= 'Z') &&
			!(character >= '0' && character <= '9') && character != '_' && character != '-' {
			return false
		}
	}
	return true
}

func validIdempotencyKey(value string) bool {
	if len(value) < 16 || len(value) > 128 {
		return false
	}
	for index := 0; index < len(value); index++ {
		character := value[index]
		if !(character >= 'a' && character <= 'z') && !(character >= 'A' && character <= 'Z') &&
			!(character >= '0' && character <= '9') && character != '.' && character != '_' &&
			character != '~' && character != '-' {
			return false
		}
	}
	return true
}

func validateEntityTagSet(tags EntityTagSet) error {
	if !tags.Present {
		if tags.Any || len(tags.Values) != 0 {
			return &Error{Code: CodeInvalidPrecondition}
		}
		return nil
	}
	if tags.Any {
		if len(tags.Values) != 0 {
			return &Error{Code: CodeInvalidPrecondition}
		}
		return nil
	}
	if len(tags.Values) == 0 || len(tags.Values) > maximumEntityTags {
		return &Error{Code: CodeInvalidPrecondition}
	}
	total := 0
	for _, tag := range tags.Values {
		total += len(tag)
		if total > maximumIfMatchBytes || !validEntityTag(tag) {
			return &Error{Code: CodeInvalidPrecondition}
		}
	}
	return nil
}

func validEntityTag(tag string) bool {
	if strings.HasPrefix(tag, "W/") {
		tag = tag[2:]
	}
	if len(tag) < 2 || tag[0] != '"' || tag[len(tag)-1] != '"' {
		return false
	}
	for index := 1; index < len(tag)-1; index++ {
		character := tag[index]
		if character == 0x21 || (character >= 0x23 && character <= 0x7e) || character >= 0x80 {
			continue
		}
		return false
	}
	return true
}

func (tags EntityTagSet) matches(current string) bool {
	if tags.Any {
		return true
	}
	for _, candidate := range tags.Values {
		if !strings.HasPrefix(candidate, "W/") && candidate == current {
			return true
		}
	}
	return false
}

func (s *Service) newValidator() ([]byte, error) {
	validator := make([]byte, 32)
	if _, err := io.ReadFull(s.random, validator); err != nil {
		return nil, fmt.Errorf("generate record validator: %w", err)
	}
	return validator, nil
}

// createTx owns the idempotent-create order. The advisory lock is taken before
// EnsureCurrent performs the first snapshot-bearing catalog query, so a
// completed creator is either still holding the lock (409 in progress) or
// committed before this snapshot and therefore visible for replay.
func (s *Service) createTx(
	ctx context.Context,
	collectionName string,
	idempotencyLock *int64,
	operation func(pgx.Tx, schema.Catalog, schema.Collection) error,
) error {
	isolation := pgx.RepeatableRead
	if idempotencyLock != nil {
		// PostgreSQL establishes a snapshot before evaluating a SELECT-list
		// advisory-lock function. Read Committed makes the subsequent catalog and
		// replay lookup observe a creator that committed in that tiny interval.
		// The schema_state FOR SHARE lock still pins DDL for the whole transaction.
		isolation = pgx.ReadCommitted
	}
	return s.pinnedTx(ctx, collectionName, isolation, idempotencyLock, operation)
}

func (s *Service) updateTx(ctx context.Context, collectionName string, operation func(pgx.Tx, schema.Catalog, schema.Collection) error) error {
	return s.pinnedTx(ctx, collectionName, pgx.RepeatableRead, nil, operation)
}

// deleteTx owns the delete-graph order. EnsureCurrent takes schema_state FOR
// SHARE first; the graph then takes every participating collection advisory
// lock before reverse discovery. Read Committed makes a relation writer that
// committed while the graph waited visible to the first discovery statement,
// preventing a stale-snapshot phantom.
func (s *Service) deleteTx(ctx context.Context, collectionName string, operation func(pgx.Tx, schema.Catalog, schema.Collection) error) error {
	return s.pinnedTx(ctx, collectionName, pgx.ReadCommitted, nil, operation)
}

func (s *Service) pinnedTx(
	ctx context.Context,
	collectionName string,
	isolation pgx.TxIsoLevel,
	idempotencyLock *int64,
	operation func(pgx.Tx, schema.Catalog, schema.Collection) error,
) error {
	if tx, ok := ctx.Value(batchTransactionKey{}).(pgx.Tx); ok {
		if idempotencyLock != nil {
			var locked bool
			if err := tx.QueryRow(ctx, "SELECT pg_try_advisory_xact_lock($1)", *idempotencyLock).Scan(&locked); err != nil {
				return err
			}
			if !locked {
				return &Error{Code: CodeIdempotencyInProgress}
			}
		}
		catalog, err := s.schemas.EnsureCurrent(ctx, tx)
		if err != nil {
			return err
		}
		collection, ok := catalog.Collections[collectionName]
		if !ok {
			return query.ErrCollectionNotFound
		}
		return operation(tx, catalog, collection)
	}
	return database.Retry(ctx, func() error {
		if hooks, _ := ctx.Value(mutationHookKey{}).(*mutationHookRun); hooks != nil {
			hooks.events = nil
		}
		return s.pool.InTx(ctx, pgx.TxOptions{IsoLevel: isolation}, func(tx pgx.Tx) error {
			if idempotencyLock != nil {
				var locked bool
				if err := tx.QueryRow(ctx, "SELECT pg_try_advisory_xact_lock($1)", *idempotencyLock).Scan(&locked); err != nil {
					return err
				}
				if !locked {
					return &Error{Code: CodeIdempotencyInProgress}
				}
			}
			catalog, err := s.schemas.EnsureCurrent(ctx, tx)
			if err != nil {
				return err
			}
			collection, ok := catalog.Collections[collectionName]
			if !ok {
				return query.ErrCollectionNotFound
			}
			return operation(tx, catalog, collection)
		})
	}, func(cause database.RetryCause, exhausted bool) {
		s.record(ctx, observability.TransactionRetryEvent(cause, exhausted))
		if s.logger == nil {
			return
		}
		message := "retrying record operation"
		if exhausted {
			message = "record operation retries exhausted"
		}
		s.logger.WarnContext(ctx, message, "cause", cause)
	})
}

func (s *Service) record(ctx context.Context, event observability.Event) {
	if s != nil && s.recorder != nil {
		s.recorder.Record(ctx, event)
	}
}
