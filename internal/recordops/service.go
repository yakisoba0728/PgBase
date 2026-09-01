package recordops

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	mathrand "math/rand/v2"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/webisnoteasy/PgBase/internal/database"
	"github.com/webisnoteasy/PgBase/internal/observability"
	"github.com/webisnoteasy/PgBase/internal/query"
	"github.com/webisnoteasy/PgBase/internal/relation"
	"github.com/webisnoteasy/PgBase/internal/schema"
)

const (
	maximumTxRetries      = 32
	maximumTxRetryDelay   = 100 * time.Millisecond
	idempotencyLifetime   = 24 * time.Hour
	maximumIfMatchBytes   = 1024
	maximumEntityTags     = 8
	recordCodecVersion    = 1
	outboxEnvelopeVersion = 1
)

type CatalogEnsurer interface {
	EnsureCurrent(context.Context, database.DBTX) (schema.Catalog, error)
}

// Service is the single supported writer for dynamic records. Query compilation
// remains in package query, while this type owns every transaction-level
// invariant around identity, revisions, audit, outbox, and replay.
type Service struct {
	pool     *database.Pool
	schemas  CatalogEnsurer
	reader   *query.Service
	logger   *slog.Logger
	recorder observability.Recorder
	random   io.Reader
}

func NewService(pool *database.Pool, schemas CatalogEnsurer, cursorSecret []byte, loggers ...*slog.Logger) *Service {
	var logger *slog.Logger
	if len(loggers) > 0 {
		logger = loggers[0]
	}
	return NewServiceWithRecorder(pool, schemas, cursorSecret, logger, nil)
}

func NewServiceWithRecorder(pool *database.Pool, schemas CatalogEnsurer, cursorSecret []byte, logger *slog.Logger, recorder observability.Recorder) *Service {
	return &Service{
		pool: pool, schemas: schemas,
		reader: query.NewServiceWithRecorder(pool, schemas, cursorSecret, logger, recorder),
		logger: logger, recorder: recorder, random: rand.Reader,
	}
}

func (s *Service) List(ctx context.Context, request ListRequest) (page RecordPage, err error) {
	operationStarted := time.Now()
	relationOperation := readTouchesRelationField(request.Expand, request.Filter, request.Sort)
	defer s.finishRelationRecordOperation(ctx, relationReadOperation(request.Expand), operationStarted, &err, &relationOperation)
	if err := s.validateRequest(request.Principal, request.RequestID); err != nil {
		return RecordPage{}, err
	}
	if request.Expand != "" {
		expandedPage, err := s.reader.ListExpanded(ctx, query.ListRequest{
			Principal:  expansionPrincipal(request.Principal),
			Collection: request.Collection,
			Cursor:     request.Cursor,
			Limit:      request.Limit,
			Filter:     request.Filter,
			Sort:       request.Sort,
			Expand:     request.Expand,
		})
		if err != nil {
			return RecordPage{}, err
		}
		result := RecordPage{Items: make([]map[string]any, len(expandedPage.Items)), NextCursor: expandedPage.NextCursor}
		for index, expanded := range expandedPage.Items {
			result.Items[index], err = expanded.Flatten()
			if err != nil {
				return RecordPage{}, err
			}
		}
		return result, nil
	}
	// The reader owns the pinned catalog, so it is the only layer that can tell
	// a bare relation field name (`sort=owner`) from a scalar column and report
	// the relation work the request actually reached.
	page, usedRelation, err := s.reader.ListRelationAwareUsage(ctx, query.RelationListRequest{
		Principal:  expansionPrincipal(request.Principal),
		Collection: request.Collection,
		Cursor:     request.Cursor,
		Limit:      request.Limit,
		Filter:     request.Filter,
		Sort:       request.Sort,
	})
	relationOperation = relationOperation || usedRelation
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
	var result Record
	err = s.inTx(ctx, request.Collection, func(tx pgx.Tx, catalog schema.Catalog, collection schema.Collection) error {
		var root, response map[string]any
		if request.Expand == "" {
			var err error
			root, err = readRecordWithRelations(ctx, tx, catalog, collection, request.ID, false)
			if err != nil {
				return err
			}
			response = root
		} else {
			base, err := readRecord(ctx, tx, collection, request.ID, false)
			if err != nil {
				return err
			}
			expanded, err := s.reader.ExpandBaseRecords(ctx, tx, catalog, collection, []map[string]any{base}, query.ExpandRequest{
				Principal: expansionPrincipal(request.Principal),
				Expand:    request.Expand,
			})
			if err != nil {
				return err
			}
			if len(expanded) != 1 {
				return errors.New("record expansion returned an invalid result")
			}
			root = expanded[0].Root
			response, err = expanded[0].Flatten()
			if err != nil {
				return err
			}
		}
		digest, err := schemaDigest(collection)
		if err != nil {
			return err
		}
		version, validator, present, err := readRevision(ctx, tx, collection.ID, request.ID)
		if err != nil {
			return err
		}
		etag, err := recordETag(collection.ID, request.ID, digest, root, validator, present)
		if err != nil {
			return err
		}
		result = Record{Data: response, ETag: etag, Version: version}
		return nil
	})
	if err != nil {
		return Record{}, err
	}
	return result, nil
}

func expansionPrincipal(principal Principal) query.Principal {
	return query.Principal{Kind: query.PrincipalKind(principal.Kind), ID: principal.ID}
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
	requestHash := digestParts("pgbase-create-request-v1", []byte(request.Collection), canonicalRequest)

	compatibility := CompatibilityNone
	var keyHash []byte
	if request.IdempotencyKey == "" {
		compatibility = CompatibilityMissingIdempotencyKey
	} else {
		if !validIdempotencyKey(request.IdempotencyKey) {
			return MutationResult{}, &Error{Code: CodeInvalidIdempotencyKey}
		}
		digest := digestParts("pgbase-idempotency-key-v1", []byte(request.IdempotencyKey))
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

	var idempotencyLock *int64
	if len(keyHash) > 0 {
		lock := advisoryLockKey(request.Principal, []byte(request.Collection), keyHash)
		idempotencyLock = &lock
	}
	err = s.inTxWithAdvisory(ctx, request.Collection, idempotencyLock, func(tx pgx.Tx, catalog schema.Catalog, collection schema.Collection) error {
		relationOperation = inputTouchesRelationField(collection, request.Input)
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
		if err := relations.replaceMulti(ctx, tx, recordID); err != nil {
			return err
		}
		if err := relations.hydrate(ctx, tx, recordID, data); err != nil {
			return err
		}
		digest, err := schemaDigest(collection)
		if err != nil {
			return err
		}
		etag := revisionETag(collection.ID, recordID, digest, validator)
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
		if err := writeRevision(ctx, tx, collection.ID, recordID, 0, 1, validator, eventID); err != nil {
			return err
		}
		if err := writeOutbox(ctx, tx, eventID, collection, recordID, "create", 1,
			mutationChanges(data, changed), false); err != nil {
			return err
		}
		if len(keyHash) > 0 {
			body, err := canonicalRecord(data)
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
		return MutationResult{}, err
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
	inputHash := digestParts("pgbase-update-request-v1", []byte(request.Collection), request.ID[:], canonicalRequest)
	eventID, err := uuid.NewV7()
	if err != nil {
		return MutationResult{}, fmt.Errorf("generate record event id: %w", err)
	}
	validator, err := s.newValidator()
	if err != nil {
		return MutationResult{}, err
	}
	compatibility := CompatibilityNone
	if !request.IfMatch.Present {
		compatibility = CompatibilityLastWriteWins
	}

	err = s.inTx(ctx, request.Collection, func(tx pgx.Tx, catalog schema.Catalog, collection schema.Collection) error {
		relationOperation = inputTouchesRelationField(collection, request.Input)
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
		digest, err := schemaDigest(collection)
		if err != nil {
			return err
		}
		previousVersion, previousValidator, revisionPresent, err := readRevision(ctx, tx, collection.ID, request.ID)
		if err != nil {
			return err
		}
		previousETag, err := recordETag(collection.ID, request.ID, digest, before, previousValidator, revisionPresent)
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
		newETag := revisionETag(collection.ID, request.ID, digest, validator)
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
		if err := writeRevision(ctx, tx, collection.ID, request.ID, previousVersion, newVersion, validator, eventID); err != nil {
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
		return MutationResult{}, err
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
	inputHash := digestParts("pgbase-delete-request-v1", []byte(request.Collection), request.ID[:])
	eventID, err := uuid.NewV7()
	if err != nil {
		return MutationResult{}, fmt.Errorf("generate record event id: %w", err)
	}
	compatibility := CompatibilityNone
	if !request.IfMatch.Present {
		compatibility = CompatibilityLastWriteWins
	}
	identityCache := newDeleteArtifactIdentityCache(eventID)

	err = s.inReadCommittedTx(ctx, request.Collection, func(tx pgx.Tx, catalog schema.Catalog, collection schema.Collection) error {
		relationOperation = collectionParticipatesInRelations(catalog, collection)
		var operationErr error
		result, operationErr = s.deleteGraphInTransaction(
			ctx, tx, catalog, collection, request, identityCache, inputHash, compatibility,
		)
		return operationErr
	})
	if err != nil {
		return MutationResult{}, err
	}
	return result, nil
}

func (s *Service) validateRequest(principal Principal, requestID string) error {
	if s == nil || s.pool == nil || s.schemas == nil || s.reader == nil || s.random == nil {
		return errors.New("record operation service is unavailable")
	}
	if principal.Kind != PrincipalAdministrator || principal.ID == uuid.Nil {
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

func (s *Service) inTx(ctx context.Context, collectionName string, operation func(pgx.Tx, schema.Catalog, schema.Collection) error) error {
	return s.inTxWithAdvisory(ctx, collectionName, nil, operation)
}

// inReadCommittedTx is reserved for delete-graph operations. EnsureCurrent
// takes schema_state FOR SHARE first; the graph then takes every participating
// collection advisory lock before reverse discovery. Read Committed makes a
// relation writer that committed while the graph waited visible to the first
// discovery statement, preventing a stale-snapshot phantom.
func (s *Service) inReadCommittedTx(ctx context.Context, collectionName string, operation func(pgx.Tx, schema.Catalog, schema.Collection) error) error {
	return s.inTxAtIsolation(ctx, collectionName, nil, pgx.ReadCommitted, operation)
}

// inTxWithAdvisory acquires a create-idempotency lock before EnsureCurrent
// performs the first snapshot-bearing catalog query. Consequently, a completed
// creator is either still holding the lock (409 in progress) or committed
// before this Repeatable Read snapshot and therefore visible for replay.
func (s *Service) inTxWithAdvisory(ctx context.Context, collectionName string, advisoryKey *int64, operation func(pgx.Tx, schema.Catalog, schema.Collection) error) error {
	isolation := pgx.RepeatableRead
	if advisoryKey != nil {
		// PostgreSQL establishes a snapshot before evaluating a SELECT-list
		// advisory-lock function. Read Committed makes the subsequent catalog and
		// replay lookup observe a creator that committed in that tiny interval.
		// The schema_state FOR SHARE lock still pins DDL for the whole transaction.
		isolation = pgx.ReadCommitted
	}
	return s.inTxAtIsolation(ctx, collectionName, advisoryKey, isolation, operation)
}

func (s *Service) inTxAtIsolation(
	ctx context.Context,
	collectionName string,
	advisoryKey *int64,
	isolation pgx.TxIsoLevel,
	operation func(pgx.Tx, schema.Catalog, schema.Collection) error,
) error {
	for attempt := 0; ; attempt++ {
		err := s.pool.InTx(ctx, pgx.TxOptions{IsoLevel: isolation}, func(tx pgx.Tx) error {
			if advisoryKey != nil {
				var locked bool
				if err := tx.QueryRow(ctx, "SELECT pg_try_advisory_xact_lock($1)", *advisoryKey).Scan(&locked); err != nil {
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
		cause, retryable := transactionRetryCause(err)
		if err == nil || !retryable {
			return err
		}
		if attempt >= maximumTxRetries {
			s.record(ctx, observability.TransactionRetryEvent(cause, true))
			if s.logger != nil {
				s.logger.WarnContext(ctx, "record operation retries exhausted", "cause", cause, "attempts", attempt+1)
			}
			return err
		}

		delay := transactionRetryDelay(attempt)
		timer := time.NewTimer(delay)
		select {
		case <-timer.C:
			if err := ctx.Err(); err != nil {
				return err
			}
			s.record(ctx, observability.TransactionRetryEvent(cause, false))
			if s.logger != nil {
				s.logger.WarnContext(ctx, "retrying record operation", "cause", cause, "attempt", attempt+1, "retry_in", delay)
			}
		case <-ctx.Done():
			if !timer.Stop() {
				select {
				case <-timer.C:
				default:
				}
			}
			return ctx.Err()
		}
	}
}

func transactionRetryDelay(attempt int) time.Duration {
	delay := 2 * time.Millisecond
	for step := 0; step < attempt && delay < maximumTxRetryDelay; step++ {
		delay *= 2
		if delay > maximumTxRetryDelay {
			delay = maximumTxRetryDelay
		}
	}
	half := delay / 2
	return half + time.Duration(mathrand.Int64N(int64(delay-half)+1))
}

func transactionRetryCause(err error) (observability.RetryCause, bool) {
	var postgresError *pgconn.PgError
	if !errors.As(err, &postgresError) {
		return "", false
	}
	switch postgresError.Code {
	case "0A000":
		return observability.RetryCauseCachedPlan, true
	case "40001":
		return observability.RetryCauseSerialization, true
	case "40P01":
		return observability.RetryCauseDeadlock, true
	default:
		return "", false
	}
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

func readRevision(ctx context.Context, tx pgx.Tx, collectionID, recordID uuid.UUID) (int64, []byte, bool, error) {
	var version int64
	var validator []byte
	err := tx.QueryRow(ctx, `SELECT version, validator FROM pgbase_meta.record_revisions
		WHERE collection_id = $1 AND record_id = $2`, collectionID, recordID).Scan(&version, &validator)
	if errors.Is(err, pgx.ErrNoRows) {
		return 0, nil, false, nil
	}
	if err != nil {
		return 0, nil, false, err
	}
	if version < 1 || len(validator) != 32 {
		return 0, nil, false, errors.New("invalid record revision state")
	}
	return version, validator, true, nil
}

func writeRevision(ctx context.Context, tx pgx.Tx, collectionID, recordID uuid.UUID, previousVersion, newVersion int64, validator []byte, eventID uuid.UUID) error {
	tag, err := tx.Exec(ctx, `INSERT INTO pgbase_meta.record_revisions
		(collection_id, record_id, version, validator, last_event_id, updated_at)
		VALUES ($1, $2, $3, $4, $5, transaction_timestamp())
		ON CONFLICT (collection_id, record_id) DO UPDATE SET
			version = EXCLUDED.version,
			validator = EXCLUDED.validator,
			last_event_id = EXCLUDED.last_event_id,
			updated_at = EXCLUDED.updated_at
		WHERE pgbase_meta.record_revisions.version = $6`,
		collectionID, recordID, newVersion, validator, eventID, previousVersion)
	if err != nil {
		return err
	}
	if tag.RowsAffected() != 1 {
		return errors.New("record revision changed outside the transaction kernel")
	}
	return nil
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
	digest := digestParts("pgbase-idempotency-lock-v1", []byte(principal.Kind), principal.ID[:], collectionScope, keyHash)
	return int64(binary.BigEndian.Uint64(digest[:8]))
}

func schemaDigest(collection schema.Collection) ([32]byte, error) {
	type digestRelation struct {
		TargetCollectionID uuid.UUID                   `json:"targetCollectionId"`
		Cardinality        schema.RelationCardinality  `json:"cardinality"`
		MinSelect          int                         `json:"minSelect"`
		MaxSelect          int                         `json:"maxSelect"`
		OnDelete           schema.RelationDeleteAction `json:"onDelete"`
		ValueCodecVersion  int                         `json:"valueCodecVersion"`
	}
	type digestField struct {
		ID       uuid.UUID        `json:"id"`
		Name     string           `json:"name"`
		Type     schema.FieldType `json:"type"`
		Ordinal  int              `json:"ordinal"`
		Required bool             `json:"required"`
		Unique   bool             `json:"unique"`
		Indexed  bool             `json:"indexed"`
		Default  json.RawMessage  `json:"default,omitempty"`
		Relation *digestRelation  `json:"relation,omitempty"`
	}
	fields := append([]schema.Field(nil), collection.Fields...)
	sort.Slice(fields, func(left, right int) bool {
		if fields[left].Ordinal == fields[right].Ordinal {
			return fields[left].ID.String() < fields[right].ID.String()
		}
		return fields[left].Ordinal < fields[right].Ordinal
	})
	encodedFields := make([]digestField, 0, len(fields))
	for _, field := range fields {
		var relation *digestRelation
		if field.Type == schema.TypeRelation {
			if field.Relation == nil {
				return [32]byte{}, fmt.Errorf("encode schema digest: invalid relation")
			}
			relation = &digestRelation{
				TargetCollectionID: field.Relation.TargetCollectionID,
				Cardinality:        field.Relation.Cardinality,
				MinSelect:          field.Relation.MinSelect,
				MaxSelect:          field.Relation.MaxSelect,
				OnDelete:           field.Relation.OnDelete,
				ValueCodecVersion:  field.Relation.ValueCodecVersion,
			}
		} else if field.Relation != nil {
			return [32]byte{}, fmt.Errorf("encode schema digest: invalid relation")
		}
		defaultValue := append(json.RawMessage(nil), field.Default...)
		if len(defaultValue) > 0 {
			canonical, err := canonicalJSON(defaultValue)
			if err != nil {
				return [32]byte{}, fmt.Errorf("encode schema digest: invalid default")
			}
			defaultValue = canonical
		}
		encodedFields = append(encodedFields, digestField{
			ID: field.ID, Name: field.Name, Type: field.Type, Ordinal: field.Ordinal,
			Required: field.Required, Unique: field.Unique, Indexed: field.Indexed,
			Default: defaultValue, Relation: relation,
		})
	}
	payload, err := json.Marshal(struct {
		CodecVersion int           `json:"codecVersion"`
		Fields       []digestField `json:"fields"`
	}{CodecVersion: recordCodecVersion, Fields: encodedFields})
	if err != nil {
		return [32]byte{}, fmt.Errorf("encode schema digest: %w", err)
	}
	return digestParts("pgbase-schema-v1", payload), nil
}

func recordETag(collectionID, recordID uuid.UUID, schemaDigest [32]byte, record map[string]any,
	validator []byte, revisionPresent bool,
) (string, error) {
	if revisionPresent {
		return revisionETag(collectionID, recordID, schemaDigest, validator), nil
	}
	body, err := canonicalRecord(record)
	if err != nil {
		return "", err
	}
	digest := digestParts("pgbase-record-etag-legacy-v1", collectionID[:], recordID[:], schemaDigest[:], body)
	return `"pgb0.` + base64.RawURLEncoding.EncodeToString(digest[:]) + `"`, nil
}

func revisionETag(collectionID, recordID uuid.UUID, schemaDigest [32]byte, validator []byte) string {
	digest := digestParts("pgbase-record-etag-v1", collectionID[:], recordID[:], validator, schemaDigest[:])
	return `"pgb1.` + base64.RawURLEncoding.EncodeToString(digest[:]) + `"`
}

func canonicalRecord(record map[string]any) ([]byte, error) {
	encoded, err := json.Marshal(record)
	if err != nil {
		return nil, fmt.Errorf("encode canonical record: %w", err)
	}
	return encoded, nil
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
		encoded, err := canonicalJSON(raw)
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

func canonicalJSON(raw []byte) ([]byte, error) {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	var value any
	if err := decoder.Decode(&value); err != nil {
		return nil, err
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		if err == nil {
			return nil, errors.New("multiple JSON values")
		}
		return nil, err
	}
	return json.Marshal(value)
}

func digestParts(domain string, parts ...[]byte) [32]byte {
	hash := sha256.New()
	writeDigestPart(hash, []byte(domain))
	for _, part := range parts {
		writeDigestPart(hash, part)
	}
	var digest [32]byte
	copy(digest[:], hash.Sum(nil))
	return digest
}

func writeDigestPart(destination io.Writer, part []byte) {
	var size [8]byte
	binary.BigEndian.PutUint64(size[:], uint64(len(part)))
	_, _ = destination.Write(size[:])
	_, _ = destination.Write(part)
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
