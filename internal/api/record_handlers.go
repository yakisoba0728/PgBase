package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	neturl "net/url"
	"strconv"
	"strings"

	"github.com/google/uuid"
	"github.com/yakisoba0728/PgBase/internal/database"
	"github.com/yakisoba0728/PgBase/internal/identity"
	"github.com/yakisoba0728/PgBase/internal/lifecycle"
	"github.com/yakisoba0728/PgBase/internal/query"
	"github.com/yakisoba0728/PgBase/internal/recordaccess"
	"github.com/yakisoba0728/PgBase/internal/recordops"
	"github.com/yakisoba0728/PgBase/internal/schema"
)

type RecordService interface {
	List(context.Context, recordops.ListRequest) (recordops.RecordPage, error)
	Get(context.Context, recordops.GetRequest) (recordops.Record, error)
	Create(context.Context, recordops.CreateRequest) (recordops.MutationResult, error)
	Update(context.Context, recordops.UpdateRequest) (recordops.MutationResult, error)
	Delete(context.Context, recordops.DeleteRequest) (recordops.MutationResult, error)
}

type recordEndpoints struct {
	service  RecordService
	files    FileService
	hooks    *lifecycle.Registry
	settings batchSettings
}

func (e recordEndpoints) list(w http.ResponseWriter, r *http.Request) {
	values, err := neturl.ParseQuery(r.URL.RawQuery)
	if err != nil {
		writeInvalidRecordRequest(w, r)
		return
	}
	if duplicateSingleton(values, "limit", "cursor", "filter", "sort", "expand", "fields", "compat") {
		writeInvalidRecordRequest(w, r)
		return
	}
	limit := 50
	if raw := values.Get("limit"); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil {
			writeInvalidRecordRequest(w, r)
			return
		}
		limit = parsed
		if limit <= 0 {
			limit = 50
		} else if limit > 200 {
			limit = 200
		}
	}
	if e.service == nil {
		writeRecordError(w, r, errors.New("record service is unavailable"))
		return
	}
	principal, ok := recordPrincipal(w, r)
	if !ok {
		return
	}
	event := &RecordRequestEvent{Actor: principal, RequestID: requestID(r.Context()), Collection: r.PathValue("collection"), Limit: limit, Cursor: values.Get("cursor"), Filter: values.Get("filter"), Sort: values.Get("sort"), Expand: values.Get("expand"), Fields: values.Get("fields")}
	finish, err := beginLifecycle(r.Context(), e.hooks, "record.list", event)
	if err != nil {
		finish(err)
		writeRecordError(w, r, err)
		return
	}
	if event.Limit < 1 || event.Limit > 200 {
		finish(errors.New("record list hook produced an invalid limit"))
		writeInvalidRecordRequest(w, r)
		return
	}
	page, err := e.service.List(r.Context(), recordops.ListRequest{
		Principal: principal, RequestID: requestID(r.Context()), Collection: r.PathValue("collection"),
		Cursor: event.Cursor, Limit: event.Limit, Filter: event.Filter, Sort: event.Sort, Expand: event.Expand, Fields: event.Fields,
		PocketBaseCompatibility: values.Get("compat") == "pocketbase",
	})
	finish(err)
	if err != nil {
		writeRecordError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, page)
}

func (e recordEndpoints) get(w http.ResponseWriter, r *http.Request) {
	values, err := neturl.ParseQuery(r.URL.RawQuery)
	if err != nil {
		writeInvalidRecordRequest(w, r)
		return
	}
	if duplicateSingleton(values, "expand", "fields", "compat") {
		writeInvalidRecordRequest(w, r)
		return
	}
	id, ok := recordID(w, r)
	if !ok {
		return
	}
	if e.service == nil {
		writeRecordError(w, r, errors.New("record service is unavailable"))
		return
	}
	principal, ok := recordPrincipal(w, r)
	if !ok {
		return
	}
	event := &RecordRequestEvent{Actor: principal, RequestID: requestID(r.Context()), Collection: r.PathValue("collection"), RecordID: id, Expand: values.Get("expand"), Fields: values.Get("fields")}
	finish, err := beginLifecycle(r.Context(), e.hooks, "record.view", event)
	if err != nil {
		finish(err)
		writeRecordError(w, r, err)
		return
	}
	record, err := e.service.Get(r.Context(), recordops.GetRequest{
		Principal: principal, RequestID: requestID(r.Context()), Collection: r.PathValue("collection"), ID: id,
		Expand: event.Expand, Fields: event.Fields,
		PocketBaseCompatibility: values.Get("compat") == "pocketbase",
	})
	finish(err)
	if err != nil {
		writeRecordError(w, r, err)
		return
	}
	w.Header().Set("ETag", record.ETag)
	writeJSON(w, http.StatusOK, record.Data)
}

func (e recordEndpoints) create(w http.ResponseWriter, r *http.Request) {
	idempotencyKey, ok := parseIdempotencyKey(r.Header)
	if !ok {
		writeRecordOperationError(w, r, &recordops.Error{Code: recordops.CodeInvalidIdempotencyKey})
		return
	}
	input, ok := recordInput(w, r)
	if !ok {
		return
	}
	if e.service == nil {
		writeRecordError(w, r, errors.New("record service is unavailable"))
		return
	}
	principal, ok := recordPrincipal(w, r)
	if !ok {
		return
	}
	event := &RecordRequestEvent{Actor: principal, RequestID: requestID(r.Context()), Collection: r.PathValue("collection"), IdempotencyKey: idempotencyKey, Input: input}
	finish, err := beginLifecycle(r.Context(), e.hooks, "record.create", event)
	if err != nil {
		finish(err)
		writeRecordError(w, r, err)
		return
	}
	var result recordops.MutationResult
	err = finishLifecycle(finish, func() error {
		result, err = e.service.Create(r.Context(), recordops.CreateRequest{
			Principal: principal, RequestID: requestID(r.Context()), Collection: r.PathValue("collection"),
			Input: event.Input, IdempotencyKey: idempotencyKey,
		})
		event.Result = cloneMutationResult(result)
		return err
	})
	if err != nil {
		writeRecordError(w, r, err)
		return
	}
	if result.Record == nil {
		writeRecordError(w, r, errors.New("record service returned no created record"))
		return
	}
	createdID, ok := result.Record.Data["id"].(string)
	parsedCreatedID, parseErr := uuid.Parse(createdID)
	if !ok || parseErr != nil || parsedCreatedID == uuid.Nil {
		writeRecordError(w, r, errors.New("record service returned an invalid created record id"))
		return
	}
	w.Header().Set("ETag", result.Record.ETag)
	w.Header().Set("Idempotency-Replayed", strconv.FormatBool(result.Replayed))
	prefix := "/api/v1/collections/"
	if principal.Kind == identity.User {
		prefix = "/api/v1/user/collections/"
	}
	w.Header().Set("Location", prefix+neturl.PathEscape(r.PathValue("collection"))+"/records/"+neturl.PathEscape(createdID))
	writeCompatibilityHeaders(w, result.Compatibility)
	writeJSON(w, http.StatusCreated, result.Record.Data)
}

func (e recordEndpoints) update(w http.ResponseWriter, r *http.Request) {
	id, ok := recordID(w, r)
	if !ok {
		return
	}
	input, ok := recordInput(w, r)
	if !ok {
		return
	}
	ifMatch, ok := parseIfMatch(r.Header)
	if !ok {
		// Preserve an invalid sentinel so the service can evaluate normal
		// schema/type validation before the precondition, as required by
		// RFC 9110 ordering. The service returns invalid_precondition only
		// after the patch itself is known to be valid.
		ifMatch = recordops.EntityTagSet{Present: true}
	}
	if e.service == nil {
		writeRecordError(w, r, errors.New("record service is unavailable"))
		return
	}
	principal, ok := recordPrincipal(w, r)
	if !ok {
		return
	}
	event := &RecordRequestEvent{Actor: principal, RequestID: requestID(r.Context()), Collection: r.PathValue("collection"), RecordID: id, Input: input}
	finish, err := beginLifecycle(r.Context(), e.hooks, "record.update", event)
	if err != nil {
		finish(err)
		writeRecordError(w, r, err)
		return
	}
	var result recordops.MutationResult
	err = finishLifecycle(finish, func() error {
		result, err = e.service.Update(r.Context(), recordops.UpdateRequest{
			Principal: principal, RequestID: requestID(r.Context()), Collection: r.PathValue("collection"),
			ID: id, Input: event.Input, IfMatch: ifMatch,
		})
		event.Result = cloneMutationResult(result)
		return err
	})
	if err != nil {
		writeRecordError(w, r, err)
		return
	}
	if result.Record == nil {
		writeRecordError(w, r, errors.New("record service returned no updated record"))
		return
	}
	w.Header().Set("ETag", result.Record.ETag)
	writeCompatibilityHeaders(w, result.Compatibility)
	writeJSON(w, http.StatusOK, result.Record.Data)
}

func (e recordEndpoints) delete(w http.ResponseWriter, r *http.Request) {
	id, ok := recordID(w, r)
	if !ok {
		return
	}
	ifMatch, ok := parseIfMatch(r.Header)
	if !ok {
		// Defer the malformed precondition result until the service has
		// established whether the target record exists. Missing records keep
		// their normal 404 result before precondition evaluation.
		ifMatch = recordops.EntityTagSet{Present: true}
	}
	if e.service == nil {
		writeRecordError(w, r, errors.New("record service is unavailable"))
		return
	}
	principal, ok := recordPrincipal(w, r)
	if !ok {
		return
	}
	event := &RecordRequestEvent{Actor: principal, RequestID: requestID(r.Context()), Collection: r.PathValue("collection"), RecordID: id}
	finish, err := beginLifecycle(r.Context(), e.hooks, "record.delete", event)
	if err != nil {
		finish(err)
		writeRecordError(w, r, err)
		return
	}
	var result recordops.MutationResult
	err = finishLifecycle(finish, func() error {
		result, err = e.service.Delete(r.Context(), recordops.DeleteRequest{
			Principal: principal, RequestID: requestID(r.Context()), Collection: r.PathValue("collection"), ID: id, IfMatch: ifMatch,
		})
		event.Result = cloneMutationResult(result)
		return err
	})
	if err != nil {
		writeRecordError(w, r, err)
		return
	}
	writeCompatibilityHeaders(w, result.Compatibility)
	w.WriteHeader(http.StatusNoContent)
}

func recordPrincipal(w http.ResponseWriter, r *http.Request) (recordops.Principal, bool) {
	if principal, ok := r.Context().Value(publicRecordPrincipalKey{}).(recordops.Principal); ok && principal.Valid() {
		return principal, true
	}
	if user, ok := r.Context().Value(userPrincipalKey{}).(authenticatedUser); ok {
		return recordops.Principal{Kind: identity.User, ID: user.principal.User.ID}, true
	}
	principal, ok := AdminPrincipalFromContext(r.Context())
	if !ok || principal.Administrator.ID == uuid.Nil {
		writeAPIError(w, r, APIError{Status: http.StatusUnauthorized, Code: "authentication_required", Message: "Administrator authentication is required"})
		return recordops.Principal{}, false
	}
	return recordops.Principal{Kind: recordops.PrincipalAdministrator, ID: principal.Administrator.ID}, true
}

func parseIdempotencyKey(header http.Header) (string, bool) {
	values := header.Values("Idempotency-Key")
	if len(values) == 0 {
		return "", true
	}
	if len(values) != 1 || len(values[0]) < 16 || len(values[0]) > 128 {
		return "", false
	}
	for _, character := range []byte(values[0]) {
		if !(character >= 'a' && character <= 'z') && !(character >= 'A' && character <= 'Z') &&
			!(character >= '0' && character <= '9') && character != '.' && character != '_' &&
			character != '~' && character != '-' {
			return "", false
		}
	}
	return values[0], true
}

func parseIfMatch(header http.Header) (recordops.EntityTagSet, bool) {
	values := header.Values("If-Match")
	if len(values) == 0 {
		return recordops.EntityTagSet{}, true
	}
	if len(values) != 1 || len(values[0]) == 0 || len(values[0]) > 1024 {
		return recordops.EntityTagSet{}, false
	}
	raw := values[0]
	position := skipOWS(raw, 0)
	if position < len(raw) && raw[position] == '*' {
		position = skipOWS(raw, position+1)
		if position != len(raw) {
			return recordops.EntityTagSet{}, false
		}
		return recordops.EntityTagSet{Present: true, Any: true}, true
	}

	parsed := recordops.EntityTagSet{Present: true, Values: make([]string, 0, 1)}
	for {
		position = skipOWS(raw, position)
		start := position
		if strings.HasPrefix(raw[position:], "W/") {
			position += 2
		}
		if position >= len(raw) || raw[position] != '"' {
			return recordops.EntityTagSet{}, false
		}
		position++
		for position < len(raw) && raw[position] != '"' {
			if !validETagCharacter(raw[position]) {
				return recordops.EntityTagSet{}, false
			}
			position++
		}
		if position >= len(raw) {
			return recordops.EntityTagSet{}, false
		}
		position++
		parsed.Values = append(parsed.Values, raw[start:position])
		if len(parsed.Values) > 8 {
			return recordops.EntityTagSet{}, false
		}
		position = skipOWS(raw, position)
		if position == len(raw) {
			return parsed, true
		}
		if raw[position] != ',' {
			return recordops.EntityTagSet{}, false
		}
		position++
		if skipOWS(raw, position) == len(raw) {
			return recordops.EntityTagSet{}, false
		}
	}
}

func skipOWS(value string, position int) int {
	for position < len(value) && (value[position] == ' ' || value[position] == '\t') {
		position++
	}
	return position
}

func validETagCharacter(character byte) bool {
	return character == 0x21 || (character >= 0x23 && character <= 0x7e) || character >= 0x80
}

func writeCompatibilityHeaders(w http.ResponseWriter, compatibility recordops.Compatibility) {
	if compatibility == recordops.CompatibilityNone {
		return
	}
	w.Header().Set("PgBase-Compatibility", string(compatibility))
}

func recordInput(w http.ResponseWriter, r *http.Request) (map[string]json.RawMessage, bool) {
	var input map[string]json.RawMessage
	if err := decodeStrictJSON(r, &input); err != nil || input == nil {
		writeInvalidRecordRequest(w, r)
		return nil, false
	}
	return input, true
}

func recordID(w http.ResponseWriter, r *http.Request) (uuid.UUID, bool) {
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil || id == uuid.Nil {
		writeInvalidRecordRequest(w, r)
		return uuid.Nil, false
	}
	return id, true
}

func writeInvalidRecordRequest(w http.ResponseWriter, r *http.Request) {
	writeAPIError(w, r, APIError{Status: http.StatusBadRequest, Code: "invalid_request", Message: "Invalid request"})
}

func writeRecordError(w http.ResponseWriter, r *http.Request, err error) {
	if writeRecordOperationError(w, r, err) {
		return
	}
	apiError := APIError{Status: http.StatusInternalServerError, Code: "internal_error", Message: "Internal server error"}
	var validation *query.ValidationError
	databaseKind := database.Classify(err)
	switch {
	case errors.Is(err, recordaccess.ErrDenied), errors.Is(err, recordaccess.ErrUnsupported):
		apiError = APIError{Status: http.StatusForbidden, Code: "access_denied", Message: "User access is not available for this collection"}
	case errors.Is(err, recordaccess.ErrNotFound):
		apiError = APIError{Status: http.StatusNotFound, Code: "not_found", Message: "Record or collection not found"}
	case query.IsInvalidCursor(err):
		apiError = APIError{Status: http.StatusBadRequest, Code: "invalid_cursor", Message: "Cursor is invalid"}
	case errors.Is(err, query.ErrCollectionNotFound), errors.Is(err, query.ErrRecordNotFound):
		apiError = APIError{Status: http.StatusNotFound, Code: "not_found", Message: "Record or collection not found"}
	case errors.Is(err, schema.ErrConflict):
		apiError = APIError{Status: http.StatusConflict, Code: "schema_conflict", Message: "Schema changed during the request"}
	case databaseKind == database.KindConflict:
		apiError = APIError{Status: http.StatusConflict, Code: "record_conflict", Message: "Record conflicts with existing data"}
	case errors.As(err, &validation):
		details := make(map[string][]string, len(validation.Details))
		for _, detail := range validation.Details {
			details[detail.Field] = append(details[detail.Field], detail.Code)
		}
		apiError = APIError{Status: http.StatusUnprocessableEntity, Code: "validation_failed", Message: "Record input is invalid", Details: details}
	case databaseKind == database.KindValidation:
		apiError = APIError{Status: http.StatusUnprocessableEntity, Code: "validation_failed", Message: "Record input is invalid"}
	case databaseKind == database.KindUnavailable, databaseKind == database.KindCanceled:
		apiError = APIError{Status: http.StatusServiceUnavailable, Code: "record_unavailable", Message: "Record service is unavailable"}
	}
	if databaseKind == database.KindUnavailable {
		w.Header().Set("Retry-After", "1")
	}
	writeAPIError(w, r, apiError)
}

func writeRecordOperationError(w http.ResponseWriter, r *http.Request, err error) bool {
	var operationError *recordops.Error
	if !errors.As(err, &operationError) {
		return false
	}
	apiError := APIError{Status: http.StatusInternalServerError, Code: "internal_error", Message: "Internal server error"}
	switch operationError.Code {
	case recordops.CodeInvalidPrincipal:
		apiError = APIError{Status: http.StatusUnauthorized, Code: "authentication_required", Message: "Administrator authentication is required"}
	case recordops.CodeInvalidRequest:
		apiError = APIError{Status: http.StatusBadRequest, Code: "invalid_request", Message: "Invalid request"}
	case recordops.CodeInvalidIdempotencyKey:
		apiError = APIError{Status: http.StatusBadRequest, Code: "invalid_idempotency_key", Message: "Idempotency-Key is invalid"}
	case recordops.CodeIdempotencyConflict:
		apiError = APIError{Status: http.StatusConflict, Code: "idempotency_conflict", Message: "Idempotency-Key was already used for a different request"}
	case recordops.CodeIdempotencyInProgress:
		apiError = APIError{Status: http.StatusConflict, Code: "idempotency_in_progress", Message: "A request with this Idempotency-Key is already in progress"}
	case recordops.CodeInvalidPrecondition:
		apiError = APIError{Status: http.StatusBadRequest, Code: "invalid_precondition", Message: "If-Match is invalid"}
	case recordops.CodePreconditionFailed:
		apiError = APIError{Status: http.StatusPreconditionFailed, Code: "precondition_failed", Message: "Record has changed"}
		if operationError.CurrentETag != "" {
			w.Header().Set("ETag", operationError.CurrentETag)
		}
	case recordops.CodeUnavailable:
		apiError = APIError{Status: http.StatusServiceUnavailable, Code: "record_unavailable", Message: "Record service is unavailable"}
		w.Header().Set("Retry-After", "1")
	default:
		writeAPIError(w, r, apiError)
		return true
	}
	writeAPIError(w, r, apiError)
	return true
}
