package api

import (
	"context"
	"errors"
	"net/http"
	neturl "net/url"
	"strconv"
	"time"

	"github.com/google/uuid"
	"github.com/webisnoteasy/PgBase/internal/schema"
)

const (
	// operationIDHeader carries the operation identity the client generates
	// before the apply it can no longer observe. A server-generated ID would
	// be useless: the client needs it precisely in the case where it never
	// received the response.
	operationIDHeader = "PgBase-Operation-ID"
	// idempotencyKeyHeader is the same header the record routes use, so one
	// client vocabulary covers both mutation surfaces.
	idempotencyKeyHeader = "Idempotency-Key"
	// schemaOperationsPath is the recovery surface a guarded client polls
	// when its apply response never arrived.
	schemaOperationsPath = "/api/admin/schema-operations"
	// schemaApplyUnguardedCompatibility marks an apply that sent no operation
	// identity. Its outcome cannot be recovered after a dropped response, and
	// the token is the one the record routes already use for the same missing
	// header so operators read one vocabulary.
	schemaApplyUnguardedCompatibility = "missing-idempotency-key"
	// Mirrors the bound the schema service enforces on OperationQuery, so a
	// rejected limit is rejected identically whether it arrives over HTTP or in
	// process.
	maximumOperationHistoryLimit = 100
	// operationRecoveryReadTimeout bounds the ledger read that answers an
	// apply whose own context has already expired. It stays well inside the
	// response margin the schema apply deadline reserves, because that margin
	// is all the time left to deliver an answer.
	operationRecoveryReadTimeout = time.Second
)

// applyIdentity is the client-generated identity that turns an apply into a
// recoverable operation. A request either carries both headers or neither;
// half an identity is a client bug the ledger must not be asked to store.
type applyIdentity struct {
	guarded     bool
	operationID uuid.UUID
	key         string
}

var errMalformedApplyIdentity = errors.New("schema apply operation identity is malformed")

func applyIdentityFrom(r *http.Request) (applyIdentity, error) {
	operationIDs := r.Header.Values(operationIDHeader)
	keys := r.Header.Values(idempotencyKeyHeader)
	if len(operationIDs) == 0 && len(keys) == 0 {
		return applyIdentity{}, nil
	}
	// A duplicated header lets two proxies disagree about which identity the
	// ledger claimed, so exactly one of each is the only accepted shape.
	if len(operationIDs) != 1 || len(keys) != 1 {
		return applyIdentity{}, errMalformedApplyIdentity
	}
	operationID, err := uuid.Parse(operationIDs[0])
	if err != nil || operationID == uuid.Nil {
		return applyIdentity{}, errMalformedApplyIdentity
	}
	return applyIdentity{guarded: true, operationID: operationID, key: keys[0]}, nil
}

func operationStatusURL(operationID uuid.UUID) string {
	return schemaOperationsPath + "/" + operationID.String()
}

// schemaOperationOutcome answers an apply whose collection body cannot be
// produced: the operation was replayed, or its outcome had to be read back
// from the ledger instead of observed. It carries where to look next and
// nothing about the request that produced it.
type schemaOperationOutcome struct {
	OperationID uuid.UUID                `json:"operationId"`
	Status      schema.OperationStatus   `json:"status"`
	StatusURL   string                   `json:"statusUrl"`
	Receipt     *schema.OperationReceipt `json:"receipt,omitempty"`
}

func (e schemaEndpoints) operationStatus(w http.ResponseWriter, r *http.Request) {
	principal, ok := AdminPrincipalFromContext(r.Context())
	if !ok {
		writeAPIError(w, r, APIError{Status: http.StatusUnauthorized, Code: "authentication_required", Message: "Administrator authentication is required"})
		return
	}
	operationID, err := uuid.Parse(r.PathValue("id"))
	if err != nil || operationID == uuid.Nil {
		// A tampered ID answers exactly like an ID that belongs to another
		// administrator and like one that never existed. Anything else would
		// let a caller probe the ledger by watching which shape comes back.
		writeOperationNotFound(w, r)
		return
	}
	ref, err := schema.NewOperationRef(principal.Administrator.ID, operationID)
	if err != nil {
		writeOperationNotFound(w, r)
		return
	}
	receipt, err := e.service.OperationStatus(r.Context(), ref)
	if err != nil {
		writeSchemaOperationError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, receipt)
}

func (e schemaEndpoints) operationHistory(w http.ResponseWriter, r *http.Request) {
	principal, ok := AdminPrincipalFromContext(r.Context())
	if !ok {
		writeAPIError(w, r, APIError{Status: http.StatusUnauthorized, Code: "authentication_required", Message: "Administrator authentication is required"})
		return
	}
	values, err := neturl.ParseQuery(r.URL.RawQuery)
	if err != nil {
		writeAPIError(w, r, APIError{Status: http.StatusBadRequest, Code: "invalid_request", Message: "Invalid request"})
		return
	}
	limit := 0
	if raw := values.Get("limit"); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil || parsed < 0 || parsed > maximumOperationHistoryLimit {
			writeAPIError(w, r, APIError{Status: http.StatusBadRequest, Code: "invalid_request", Message: "Invalid request"})
			return
		}
		limit = parsed
	}
	// A cursor that is too long, forged, expired, or minted for a different
	// owner is one answer: the kernel decides all four and the transport does
	// not learn which of them it was.
	query, err := schema.NewOperationQuery(principal.Administrator.ID, limit, values.Get("cursor"))
	if err != nil {
		writeInvalidOperationCursor(w, r)
		return
	}
	page, err := e.service.ListOperations(r.Context(), query)
	if err != nil {
		writeSchemaOperationError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, page)
}

// writeGuardedApplyError answers a guarded apply that did not return a
// collection. Transport failures are never turned into a verdict here: the
// ledger owns whether an operation committed, and a request that cannot be
// decided ends pointing at where the answer will appear.
func (e schemaEndpoints) writeGuardedApplyError(
	w http.ResponseWriter, r *http.Request,
	actorID uuid.UUID, identity applyIdentity, receipt schema.OperationReceipt, err error,
) {
	var recovery *schema.OperationRecoveryError
	switch {
	case errors.As(err, &recovery):
		if receipt, known := recovery.Receipt(); known {
			writeSchemaOperationOutcome(w, r, identity.operationID, receipt.Status, &receipt)
			return
		}
		writeSchemaOperationOutcome(w, r, identity.operationID, schema.OperationUnknown, nil)
	case errors.Is(err, context.DeadlineExceeded), errors.Is(err, context.Canceled):
		// The request context expiring says nothing about the transaction. It
		// races the commit acknowledgement, so the answer is read from the
		// ledger rather than guessed from which side of the race returned.
		e.writeRecoveredApplyOutcome(w, r, actorID, identity)
	case errors.Is(err, schema.ErrOperationConflict):
		writeAPIError(w, r, APIError{
			Status: http.StatusConflict, Code: "schema_operation_conflict",
			Message: "Schema operation identity is already bound to a different request",
		})
	case errors.Is(err, schema.ErrOperationFailed):
		writeSchemaOperationFailure(w, r, receipt.FailureCode)
	default:
		writeSchemaError(w, r, err)
	}
}

func (e schemaEndpoints) writeRecoveredApplyOutcome(
	w http.ResponseWriter, r *http.Request, actorID uuid.UUID, identity applyIdentity,
) {
	ref, err := schema.NewOperationRef(actorID, identity.operationID)
	if err != nil {
		writeSchemaOperationOutcome(w, r, identity.operationID, schema.OperationUnknown, nil)
		return
	}
	// The request's own context is already dead, so the ledger read gets a
	// fresh bounded one. Reusing the expired context would guarantee the
	// unknown answer this exists to avoid.
	ctx, cancel := context.WithTimeout(context.WithoutCancel(r.Context()), operationRecoveryReadTimeout)
	defer cancel()
	receipt, err := e.service.OperationStatus(ctx, ref)
	if err != nil {
		writeSchemaOperationOutcome(w, r, identity.operationID, schema.OperationUnknown, nil)
		return
	}
	writeSchemaOperationOutcome(w, r, identity.operationID, receipt.Status, &receipt)
}

func writeSchemaOperationOutcome(
	w http.ResponseWriter, r *http.Request,
	operationID uuid.UUID, status schema.OperationStatus, receipt *schema.OperationReceipt,
) {
	if status == schema.OperationFailed {
		var failureCode schema.OperationFailureCode
		if receipt != nil {
			failureCode = receipt.FailureCode
		}
		writeSchemaOperationFailure(w, r, failureCode)
		return
	}
	outcome := schemaOperationOutcome{
		OperationID: operationID, Status: status,
		StatusURL: operationStatusURL(operationID), Receipt: receipt,
	}
	if status == schema.OperationCommitted {
		writeJSON(w, http.StatusOK, outcome)
		return
	}
	// Not yet decidable. The client is told where the verdict will appear
	// rather than being handed a failure the ledger may contradict.
	w.Header().Set("Location", outcome.StatusURL)
	w.Header().Set("Retry-After", "1")
	writeJSON(w, http.StatusAccepted, outcome)
}

// writeSchemaOperationFailure reports a terminal ledger failure. The bounded
// classification travels with it so a client learns whether to replan or to
// escalate without a second round trip; raw SQL, request and error text stay
// in the ledger where they never were.
func writeSchemaOperationFailure(w http.ResponseWriter, r *http.Request, failureCode schema.OperationFailureCode) {
	apiError := APIError{
		Status: http.StatusConflict, Code: "schema_operation_failed",
		Message: "Schema operation did not apply",
	}
	if failureCode != "" {
		apiError.Details = map[string][]string{"operation": {string(failureCode)}}
	}
	writeAPIError(w, r, apiError)
}

func writeSchemaOperationError(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case errors.Is(err, schema.ErrOperationNotFound):
		writeOperationNotFound(w, r)
	case errors.Is(err, schema.ErrInvalidOperation):
		writeInvalidOperationCursor(w, r)
	default:
		writeSchemaError(w, r, err)
	}
}

func writeOperationNotFound(w http.ResponseWriter, r *http.Request) {
	writeAPIError(w, r, APIError{Status: http.StatusNotFound, Code: "not_found", Message: "Not found"})
}

func writeInvalidOperationCursor(w http.ResponseWriter, r *http.Request) {
	writeAPIError(w, r, APIError{Status: http.StatusBadRequest, Code: "invalid_cursor", Message: "Cursor is invalid"})
}
