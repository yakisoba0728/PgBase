package recordops

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/webisnoteasy/PgBase/internal/database"
	"github.com/webisnoteasy/PgBase/internal/membership"
	"github.com/webisnoteasy/PgBase/internal/observability"
	"github.com/webisnoteasy/PgBase/internal/query"
	"github.com/webisnoteasy/PgBase/internal/recordartifact"
	"github.com/webisnoteasy/PgBase/internal/relation"
	"github.com/webisnoteasy/PgBase/internal/schema"
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
	pool     *database.Pool
	schemas  *schema.Service
	reader   *query.Service
	logger   *slog.Logger
	recorder observability.Recorder
	random   io.Reader
	members  *membershipWriterProvider
}

func NewService(pool *database.Pool, schemas *schema.Service, cursorSecret []byte, loggers ...*slog.Logger) *Service {
	var logger *slog.Logger
	if len(loggers) > 0 {
		logger = loggers[0]
	}
	return NewServiceWithRecorder(pool, schemas, cursorSecret, logger, nil)
}

func NewServiceWithRecorder(pool *database.Pool, schemas *schema.Service, cursorSecret []byte, logger *slog.Logger, recorder observability.Recorder) *Service {
	return newService(pool, schemas, cursorSecret, logger, recorder, nil)
}

// NewServiceWithMembershipRuntime uses the process-wide membership runtime
// that the application registered and heartbeats. The older constructors keep
// their signatures and lazily register a Release-A writer for direct package
// users such as integration tests.
func NewServiceWithMembershipRuntime(
	pool *database.Pool,
	schemas *schema.Service,
	cursorSecret []byte,
	logger *slog.Logger,
	recorder observability.Recorder,
	runtime *membership.Runtime,
) *Service {
	return newService(pool, schemas, cursorSecret, logger, recorder, runtime)
}

func newService(
	pool *database.Pool,
	schemas *schema.Service,
	cursorSecret []byte,
	logger *slog.Logger,
	recorder observability.Recorder,
	runtime *membership.Runtime,
) *Service {
	return &Service{
		pool: pool, schemas: schemas,
		reader: query.NewServiceWithRecorder(pool, schemas, cursorSecret, logger, recorder),
		logger: logger, recorder: recorder, random: rand.Reader,
		members: newMembershipWriterProvider(pool, runtime),
	}
}

func (s *Service) List(ctx context.Context, request ListRequest) (page RecordPage, err error) {
	started := time.Now()
	if err := s.validateRequest(request.Principal, request.RequestID); err != nil {
		if readTouchesRelationField(request.Expand, request.Filter, request.Sort) {
			s.record(ctx, observability.OperationEvent(
				observability.ComponentRelation,
				relationReadOperation(request.Expand),
				relationOperationOutcome(err),
				time.Since(started),
			))
		}
		return RecordPage{}, err
	}
	page, err = s.reader.List(ctx, query.ListRequest{
		Principal:  request.Principal,
		Collection: request.Collection,
		Cursor:     request.Cursor,
		Limit:      request.Limit,
		Filter:     request.Filter,
		Sort:       request.Sort,
		Expand:     request.Expand,
	})
	if errors.Is(err, membership.ErrUnavailable) {
		return RecordPage{}, unavailableMembership(err)
	}
	return page, err
}

func (s *Service) Get(ctx context.Context, request GetRequest) (record Record, err error) {
	operationStarted := time.Now()
	relationOperation := readTouchesRelationField(request.Expand, "", "")
	defer s.finishRelationRecordOperation(ctx, relationReadOperation(request.Expand), operationStarted, &err, &relationOperation)
	if err := s.validateRequest(request.Principal, request.RequestID); err != nil {
		return Record{}, err
	}
	if request.ID == uuid.Nil {
		return Record{}, &Error{Code: CodeInvalidRequest}
	}
	return s.reader.Get(ctx, query.GetRequest{
		Principal: request.Principal, Collection: request.Collection, ID: request.ID, Expand: request.Expand,
	})
}

func (s *Service) Create(ctx context.Context, request CreateRequest) (result MutationResult, err error) {
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

	recordID, err := uuid.NewV7()
	if err != nil {
		return MutationResult{}, fmt.Errorf("generate record id: %w", err)
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
				result = replayed
				return nil
			}
		}
		if err := createFence.Reserve(ctx); err != nil {
			return transactionalMembership(err)
		}

		relations, err := prepareRelationMutation(catalog, collection, relation.CreateInput, request.Input)
		if err != nil {
			return err
		}
		statement, err := query.CompileInsert(collection, relations.compiler, recordID)
		if err != nil {
			return err
		}
		if err := relations.lock(ctx, tx, recordID, relation.SourcePending); err != nil {
			return err
		}
		data, err := query.EncodeRecord(collection, tx.QueryRow(ctx, statement.SQL, statement.Args...))
		if err != nil {
			return query.NormalizeRecordError(err)
		}
		if err := createFence.RecordCreated(ctx, recordID); err != nil {
			return transactionalMembership(err)
		}
		if err := relations.replaceMulti(ctx, tx, recordID); err != nil {
			return err
		}
		if err := relations.hydrate(ctx, tx, recordID, data); err != nil {
			return err
		}
		digest, err := recordartifact.SchemaDigest(collection)
		if err != nil {
			return err
		}
		etag := recordartifact.RevisionETag(collection.ID, recordID, digest, validator)
		record := &Record{Data: data, ETag: etag, Version: 1}
		changed := committedUserFields(collection)
		if err := writeAudit(ctx, tx, auditWrite{
			EventID: eventID, Principal: request.Principal, RequestID: request.RequestID,
			Collection: collection, RecordID: recordID, Operation: "create",
			SchemaVersion: catalog.Version, SchemaDigest: digest, NewVersion: 1,
			NewETag: etag, ChangedFields: changed, InputHash: requestHash[:],
			IdempotencyKeyHash: keyHash, Compatibility: compatibility,
		}); err != nil {
			return err
		}
		if err := recordartifact.WriteRevision(ctx, tx, collection.ID, recordID, 0, 1, validator, eventID); err != nil {
			return err
		}
		if err := writeOutbox(ctx, tx, eventID, collection, recordID, "create", 1,
			mutationChanges(data, changed), false); err != nil {
			return err
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
		result = MutationResult{Record: record, EventID: eventID, Compatibility: compatibility}
		return nil
	})
	if err != nil {
		return MutationResult{}, membershipBoundaryError(err)
	}
	return result, nil
}

func (s *Service) Update(ctx context.Context, request UpdateRequest) (result MutationResult, err error) {
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
		relationOperation = inputTouchesRelationField(collection, request.Input)
		if _, err := membershipWriter.Begin(ctx, tx); err != nil {
			return transactionalMembership(err)
		}
		relations, err := prepareRelationMutation(catalog, collection, relation.PatchInput, request.Input)
		if err != nil {
			return err
		}
		statement, err := query.CompileUpdate(collection, request.ID, relations.compiler)
		if err != nil {
			return err
		}
		if err := relations.lock(ctx, tx, request.ID, relation.SourceStored); err != nil {
			return err
		}
		before, err := readRecord(ctx, tx, collection, request.ID, !relations.present)
		if err != nil {
			return err
		}
		if err := validateEntityTagSet(request.IfMatch); err != nil {
			return err
		}
		if err := relations.hydrate(ctx, tx, request.ID, before); err != nil {
			return err
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
		if err := relations.replaceMulti(ctx, tx, request.ID); err != nil {
			return err
		}
		if err := relations.hydrate(ctx, tx, request.ID, after); err != nil {
			return err
		}
		newVersion := previousVersion + 1
		newETag := recordartifact.RevisionETag(collection.ID, request.ID, digest, validator)
		changed := sortedInputFields(request.Input)
		if err := writeAudit(ctx, tx, auditWrite{
			EventID: eventID, Principal: request.Principal, RequestID: request.RequestID,
			Collection: collection, RecordID: request.ID, Operation: "update",
			SchemaVersion: catalog.Version, SchemaDigest: digest,
			PreviousVersion: &previousVersion, NewVersion: newVersion,
			PreviousETag: previousETag, NewETag: newETag, ChangedFields: changed,
			InputHash: inputHash[:], Compatibility: compatibility,
		}); err != nil {
			return err
		}
		if err := recordartifact.WriteRevision(ctx, tx, collection.ID, request.ID, previousVersion, newVersion, validator, eventID); err != nil {
			return err
		}
		if err := writeOutbox(ctx, tx, eventID, collection, request.ID, "update", newVersion,
			mutationChanges(after, changed), false); err != nil {
			return err
		}
		result = MutationResult{
			Record:  &Record{Data: after, ETag: newETag, Version: newVersion},
			EventID: eventID, Compatibility: compatibility,
		}
		return nil
	})
	if err != nil {
		return MutationResult{}, membershipBoundaryError(err)
	}
	return result, nil
}

func (s *Service) Delete(ctx context.Context, request DeleteRequest) (result MutationResult, err error) {
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
	return database.Retry(ctx, func() error {
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

func (s *Service) finishRelationRecordOperation(
	ctx context.Context,
	operation observability.Operation,
	started time.Time,
	operationErr *error,
	enabled *bool,
) {
	if recovered := recover(); recovered != nil {
		if enabled != nil && *enabled {
			s.record(ctx, observability.OperationEvent(observability.ComponentRelation, operation, observability.OutcomeFailure, time.Since(started)))
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
	s.record(ctx, observability.OperationEvent(observability.ComponentRelation, operation, relationOperationOutcome(err), time.Since(started)))
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

func readRecord(ctx context.Context, tx pgx.Tx, collection schema.Collection, id uuid.UUID, lock bool) (map[string]any, error) {
	statement := query.CompileSelectByID(collection, id)
	if lock {
		statement.SQL += " FOR UPDATE"
	}
	record, err := query.EncodeRecord(collection, tx.QueryRow(ctx, statement.SQL, statement.Args...))
	return record, query.NormalizeRecordError(err)
}

type auditWrite struct {
	EventID            uuid.UUID
	Principal          Principal
	RequestID          string
	Collection         schema.Collection
	RecordID           uuid.UUID
	Operation          string
	SchemaVersion      int64
	SchemaDigest       [32]byte
	PreviousVersion    *int64
	NewVersion         int64
	PreviousETag       string
	NewETag            string
	ChangedFields      []string
	InputHash          []byte
	IdempotencyKeyHash []byte
	Compatibility      Compatibility
}

func writeAudit(ctx context.Context, tx pgx.Tx, event auditWrite) error {
	compatibility := "none"
	if event.Compatibility != CompatibilityNone {
		compatibility = string(event.Compatibility)
	}
	var previousETag, newETag any
	if event.PreviousETag != "" {
		previousETag = event.PreviousETag
	}
	if event.NewETag != "" {
		newETag = event.NewETag
	}
	var idempotencyHash any
	if len(event.IdempotencyKeyHash) > 0 {
		idempotencyHash = event.IdempotencyKeyHash
	}
	_, err := tx.Exec(ctx, `INSERT INTO pgbase_meta.record_audit_events
		(event_id, actor_kind, actor_id, request_id, collection_id, collection_name,
		 record_id, operation, schema_version, schema_digest, previous_version,
		 new_version, previous_etag, new_etag, changed_fields, input_hash,
		 idempotency_key_hash, compatibility, committed_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13,
			$14, $15, $16, $17, $18, transaction_timestamp())`,
		event.EventID, string(event.Principal.Kind), event.Principal.ID, event.RequestID,
		event.Collection.ID, event.Collection.Name, event.RecordID, event.Operation,
		event.SchemaVersion, event.SchemaDigest[:], event.PreviousVersion,
		event.NewVersion, previousETag, newETag, event.ChangedFields, event.InputHash,
		idempotencyHash, compatibility)
	return err
}

type outboxEnvelope struct {
	Version        int            `json:"version"`
	EventID        uuid.UUID      `json:"eventId"`
	CollectionID   uuid.UUID      `json:"collectionId"`
	CollectionName string         `json:"collectionName"`
	RecordID       uuid.UUID      `json:"recordId"`
	Operation      string         `json:"operation"`
	RecordVersion  int64          `json:"recordVersion"`
	Changes        map[string]any `json:"changes,omitempty"`
	Tombstone      bool           `json:"tombstone,omitempty"`
}

func writeOutbox(ctx context.Context, tx pgx.Tx, eventID uuid.UUID, collection schema.Collection,
	recordID uuid.UUID, operation string, version int64, changes map[string]any, tombstone bool,
) error {
	envelope, err := json.Marshal(outboxEnvelope{
		Version: outboxEnvelopeVersion, EventID: eventID, CollectionID: collection.ID,
		CollectionName: collection.Name, RecordID: recordID, Operation: operation,
		RecordVersion: version, Changes: changes, Tombstone: tombstone,
	})
	if err != nil {
		return fmt.Errorf("encode record outbox event: %w", err)
	}
	_, err = tx.Exec(ctx, `INSERT INTO pgbase_meta.record_outbox
		(event_id, topic, collection_id, collection_name, record_id, operation,
		 record_version, envelope, created_at)
		VALUES ($1, 'record.mutation.v1', $2, $3, $4, $5, $6, $7::jsonb, transaction_timestamp())`,
		eventID, collection.ID, collection.Name, recordID, operation, version, string(envelope))
	return err
}

func findIdempotentCreate(ctx context.Context, tx pgx.Tx, principal Principal, collectionID uuid.UUID,
	keyHash, requestHash []byte,
) (MutationResult, bool, error) {
	var storedHash []byte
	var recordID, eventID uuid.UUID
	var body []byte
	var etag string
	var expiresAt time.Time
	err := tx.QueryRow(ctx, `SELECT request_hash, record_id, event_id, response_body, etag, expires_at
		FROM pgbase_meta.record_idempotency
		WHERE actor_kind = $1 AND actor_id = $2 AND collection_id = $3 AND key_hash = $4`,
		string(principal.Kind), principal.ID, collectionID, keyHash).
		Scan(&storedHash, &recordID, &eventID, &body, &etag, &expiresAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return MutationResult{}, false, nil
	}
	if err != nil {
		return MutationResult{}, false, err
	}
	var now time.Time
	if err := tx.QueryRow(ctx, "SELECT transaction_timestamp()").Scan(&now); err != nil {
		return MutationResult{}, false, err
	}
	if !expiresAt.After(now) {
		if _, err := tx.Exec(ctx, `DELETE FROM pgbase_meta.record_idempotency
			WHERE actor_kind = $1 AND actor_id = $2 AND collection_id = $3 AND key_hash = $4`,
			string(principal.Kind), principal.ID, collectionID, keyHash); err != nil {
			return MutationResult{}, false, err
		}
		return MutationResult{}, false, nil
	}
	if !bytes.Equal(storedHash, requestHash) {
		return MutationResult{}, false, &Error{Code: CodeIdempotencyConflict}
	}
	data, err := decodeStoredRecord(body)
	if err != nil {
		return MutationResult{}, false, errors.New("decode stored idempotency result")
	}
	return MutationResult{
		Record:  &Record{Data: data, ETag: etag, Version: 1},
		EventID: eventID, Replayed: true,
	}, true, nil
}

func writeIdempotentCreate(ctx context.Context, tx pgx.Tx, principal Principal, collectionID uuid.UUID,
	keyHash, requestHash []byte, recordID, eventID uuid.UUID, body []byte, etag string,
) error {
	_, err := tx.Exec(ctx, `INSERT INTO pgbase_meta.record_idempotency
		(actor_kind, actor_id, collection_id, key_hash, request_hash, record_id,
		 event_id, response_body, etag, created_at, expires_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8::jsonb, $9,
			transaction_timestamp(), transaction_timestamp() + $10::interval)`,
		string(principal.Kind), principal.ID, collectionID, keyHash, requestHash,
		recordID, eventID, string(body), etag, "24 hours")
	return err
}

func advisoryLockKey(principal Principal, collectionScope, keyHash []byte) int64 {
	digest := recordartifact.Digest("pgbase-idempotency-lock-v1", []byte(principal.Kind), principal.ID[:], collectionScope, keyHash)
	return int64(binary.BigEndian.Uint64(digest[:8]))
}

func decodeStoredRecord(body []byte) (map[string]any, error) {
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.UseNumber()
	var data map[string]any
	if err := decoder.Decode(&data); err != nil {
		return nil, err
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		if err == nil {
			return nil, errors.New("multiple stored JSON values")
		}
		return nil, err
	}
	return data, nil
}

func canonicalInput(input map[string]json.RawMessage) ([]byte, error) {
	canonical := make(map[string]json.RawMessage, len(input))
	for name, raw := range input {
		encoded, err := recordartifact.CanonicalJSON(raw)
		if err != nil {
			return nil, &query.ValidationError{Details: []query.ValidationDetail{{Field: name, Code: "type_mismatch"}}}
		}
		canonical[name] = encoded
	}
	encoded, err := json.Marshal(canonical)
	if err != nil {
		return nil, fmt.Errorf("encode canonical record input: %w", err)
	}
	return encoded, nil
}

func sortedInputFields(input map[string]json.RawMessage) []string {
	fields := make([]string, 0, len(input))
	for name := range input {
		fields = append(fields, name)
	}
	sort.Strings(fields)
	return fields
}

func committedUserFields(collection schema.Collection) []string {
	fields := make([]schema.Field, len(collection.Fields))
	copy(fields, collection.Fields)
	sort.Slice(fields, func(left, right int) bool {
		if fields[left].Ordinal == fields[right].Ordinal {
			return fields[left].Name < fields[right].Name
		}
		return fields[left].Ordinal < fields[right].Ordinal
	})
	names := make([]string, 0, len(fields))
	for _, field := range fields {
		names = append(names, field.Name)
	}
	// Audit consumers expect changed field names to be lexically stable rather
	// than tied to physical field ordinal.
	sort.Strings(names)
	return names
}

func mutationChanges(record map[string]any, fields []string) map[string]any {
	if len(fields) == 0 {
		return nil
	}
	changes := make(map[string]any, len(fields))
	for _, field := range fields {
		changes[field] = record[field]
	}
	return changes
}
