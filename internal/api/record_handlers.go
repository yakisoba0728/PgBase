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
	"github.com/yakisoba0728/PgBase/internal/accessrule"
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

type pocketBaseListService interface {
	ListPocketBase(context.Context, recordops.PocketBaseListRequest) (recordops.PocketBasePage, error)
}

const recordEnrichmentHeader = "PgBase-Enrichment"

func ruleRequestContext(r *http.Request) *accessrule.RequestContext {
	queryValues := map[string][]string{}
	for key, values := range r.URL.Query() {
		queryValues[key] = append([]string(nil), values...)
	}
	headers := map[string][]string{}
	for key, values := range r.Header {
		normalized := strings.ReplaceAll(strings.ToLower(key), "-", "_")
		headers[normalized] = append([]string(nil), values...)
	}
	auth := map[string]any{}
	if current, ok := r.Context().Value(userPrincipalKey{}).(authenticatedUser); ok {
		auth["id"] = current.principal.User.ID
		auth["email"] = current.principal.User.Email
		auth["verified"] = current.principal.User.Verified
		auth["created"] = current.principal.User.CreatedAt
	}
	return &accessrule.RequestContext{Method: r.Method, Query: queryValues, Headers: headers, Auth: auth, Context: map[string]any{"requestId": requestID(r.Context())}}
}

type recordEndpoints struct {
	service  RecordService
	files    FileService
	hooks    *lifecycle.Registry
	settings batchSettings
}

func (e recordEndpoints) enrich(ctx context.Context, collection string, record map[string]any) error {
	if e.hooks == nil || record == nil {
		return nil
	}
	event := &RecordEnrichEvent{Collection: collection, Record: record}
	finish, err := beginLifecycle(ctx, e.hooks, "record.enrich", event)
	if err != nil {
		finish(err)
		return err
	}
	finish(nil)
	return nil
}

func (e recordEndpoints) list(w http.ResponseWriter, r *http.Request) {
	values, err := neturl.ParseQuery(r.URL.RawQuery)
	if err != nil {
		writeInvalidRecordRequest(w, r)
		return
	}
	if duplicateSingleton(values, "limit", "cursor", "filter", "sort", "expand", "fields", "compat", "page", "perPage", "skipTotal") {
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
	pbPage, pbRequested, ok := parsePocketBasePage(values)
	if !ok {
		writeInvalidRecordRequest(w, r)
		return
	}
	event := &RecordRequestEvent{Actor: principal, RequestID: requestID(r.Context()), Collection: r.PathValue("collection"), Limit: limit, Cursor: values.Get("cursor"), Filter: values.Get("filter"), Sort: values.Get("sort"), Expand: values.Get("expand"), Fields: values.Get("fields")}
	if pbRequested {
		event.Page, event.PerPage, event.SkipTotal = pbPage.Page, pbPage.PerPage, pbPage.SkipTotal
	}
	finish, err := beginLifecycle(r.Context(), e.hooks, "record.list", event)
	if err != nil {
		finish(err)
		writeRecordError(w, r, err)
		return
	}
	if (!pbRequested && (event.Limit < 1 || event.Limit > 200)) || (pbRequested && (event.Page < 1 || event.PerPage < 1 || event.PerPage > 1000)) {
		finish(errors.New("record list hook produced an invalid limit"))
		writeInvalidRecordRequest(w, r)
		return
	}
	request := recordops.ListRequest{
		Principal: principal, RequestID: requestID(r.Context()), Collection: r.PathValue("collection"),
		Cursor: event.Cursor, Limit: event.Limit, Filter: event.Filter, Sort: event.Sort, Expand: event.Expand, Fields: event.Fields,
		PocketBaseCompatibility: values.Get("compat") == "pocketbase",
		RuleContext:             ruleRequestContext(r),
	}
	if pbRequested {
		service, supported := e.service.(pocketBaseListService)
		if !supported {
			finish(errors.New("PocketBase list adapter is unavailable"))
			writeRecordError(w, r, errors.New("record service is unavailable"))
			return
		}
		page, listErr := service.ListPocketBase(r.Context(), recordops.PocketBaseListRequest{ListRequest: request, Page: event.Page, PerPage: event.PerPage, SkipTotal: event.SkipTotal})
		finish(listErr)
		if listErr != nil {
			writeRecordError(w, r, listErr)
			return
		}
		for index := range page.Items {
			if err := e.enrich(r.Context(), event.Collection, page.Items[index]); err != nil {
				writeRecordError(w, r, err)
				return
			}
		}
		writeJSON(w, http.StatusOK, page)
		return
	}
	page, err := e.service.List(r.Context(), request)
	finish(err)
	if err != nil {
		writeRecordError(w, r, err)
		return
	}
	for index := range page.Items {
		if err := e.enrich(r.Context(), event.Collection, page.Items[index]); err != nil {
			writeRecordError(w, r, err)
			return
		}
	}
	writeJSON(w, http.StatusOK, page)
}

func parsePocketBasePage(values neturl.Values) (recordops.PocketBaseListRequest, bool, bool) {
	requested := values.Get("compat") == "pocketbase" && (values.Has("page") || values.Has("perPage") || values.Has("skipTotal"))
	if !requested {
		return recordops.PocketBaseListRequest{}, false, true
	}
	page, perPage, skipTotal := 1, 30, false
	var err error
	if raw := values.Get("page"); raw != "" {
		page, err = strconv.Atoi(raw)
		if err != nil {
			return recordops.PocketBaseListRequest{}, true, false
		}
	}
	if raw := values.Get("perPage"); raw != "" {
		perPage, err = strconv.Atoi(raw)
		if err != nil {
			return recordops.PocketBaseListRequest{}, true, false
		}
	}
	if raw := values.Get("skipTotal"); raw != "" {
		skipTotal, err = strconv.ParseBool(raw)
		if err != nil {
			return recordops.PocketBaseListRequest{}, true, false
		}
	}
	page, perPage = normalizePocketBaseHTTPPage(page, perPage)
	if page-1 > int(^uint(0)>>1)/perPage {
		return recordops.PocketBaseListRequest{}, true, false
	}
	return recordops.PocketBaseListRequest{Page: page, PerPage: perPage, SkipTotal: skipTotal}, true, true
}

func normalizePocketBaseHTTPPage(page, perPage int) (int, int) {
	if page <= 0 {
		page = 1
	}
	if perPage <= 0 {
		perPage = 30
	} else if perPage > 1000 {
		perPage = 1000
	}
	return page, perPage
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
		Principal: principal, RequestID: requestID(r.Context()), Collection: r.PathValue("collection"), PublicID: id,
		Expand: event.Expand, Fields: event.Fields, RuleContext: ruleRequestContext(r),
		PocketBaseCompatibility: values.Get("compat") == "pocketbase",
	})
	finish(err)
	if err != nil {
		writeRecordError(w, r, err)
		return
	}
	if err := e.enrich(r.Context(), event.Collection, record.Data); err != nil {
		writeRecordError(w, r, err)
		return
	}
	w.Header().Set("ETag", record.ETag)
	writeJSON(w, http.StatusOK, record.Data)
}

func (e recordEndpoints) create(w http.ResponseWriter, r *http.Request) {
	projection, ok := compileMutationProjection(w, r)
	if !ok {
		return
	}
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
			Input: event.Input, IdempotencyKey: idempotencyKey, RuleContext: ruleRequestContext(r),
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
	if !ok || createdID == "" {
		writeRecordError(w, r, errors.New("record service returned an invalid created record id"))
		return
	}
	w.Header().Set("ETag", result.Record.ETag)
	w.Header().Set("Idempotency-Replayed", strconv.FormatBool(result.Replayed))
	prefix := "/api/v1/collections/"
	switch principal.Kind {
	case identity.User:
		prefix = "/api/v1/user/collections/"
	case identity.Service:
		prefix = "/api/v1/service/collections/"
	case identity.Anonymous:
		prefix = "/api/v1/public/collections/"
	}
	w.Header().Set("Location", prefix+neturl.PathEscape(r.PathValue("collection"))+"/records/"+neturl.PathEscape(createdID))
	writeCompatibilityHeaders(w, result.Compatibility)
	if err := e.enrich(r.Context(), event.Collection, result.Record.Data); err != nil {
		writeCommittedRecordFallback(w, http.StatusCreated, createdID)
		return
	}
	writeJSON(w, http.StatusCreated, projection.Apply(result.Record.Data))
}

func (e recordEndpoints) update(w http.ResponseWriter, r *http.Request) {
	projection, ok := compileMutationProjection(w, r)
	if !ok {
		return
	}
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
			PublicID: id, Input: event.Input, IfMatch: ifMatch, RuleContext: ruleRequestContext(r),
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
	if err := e.enrich(r.Context(), event.Collection, result.Record.Data); err != nil {
		writeCommittedRecordFallback(w, http.StatusOK, id)
		return
	}
	writeJSON(w, http.StatusOK, projection.Apply(result.Record.Data))
}

func writeCommittedRecordFallback(w http.ResponseWriter, status int, id string) {
	w.Header().Set(recordEnrichmentHeader, "failed")
	exposed := w.Header().Get("Access-Control-Expose-Headers")
	if exposed == "" {
		w.Header().Set("Access-Control-Expose-Headers", recordEnrichmentHeader)
	} else if !headerListContains(exposed, recordEnrichmentHeader) {
		w.Header().Set("Access-Control-Expose-Headers", exposed+", "+recordEnrichmentHeader)
	}
	writeJSON(w, status, map[string]string{"id": id})
}

func headerListContains(value, name string) bool {
	for _, item := range strings.Split(value, ",") {
		if strings.EqualFold(strings.TrimSpace(item), name) {
			return true
		}
	}
	return false
}

func compileMutationProjection(w http.ResponseWriter, r *http.Request) (query.Projection, bool) {
	values, err := neturl.ParseQuery(r.URL.RawQuery)
	if err != nil || duplicateSingleton(values, "fields") {
		writeInvalidRecordRequest(w, r)
		return query.Projection{}, false
	}
	projection, err := query.CompileProjection(values.Get("fields"))
	if err != nil {
		writeInvalidRecordRequest(w, r)
		return query.Projection{}, false
	}
	return projection, true
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
			Principal: principal, RequestID: requestID(r.Context()), Collection: r.PathValue("collection"), PublicID: id, IfMatch: ifMatch, RuleContext: ruleRequestContext(r),
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

func recordID(w http.ResponseWriter, r *http.Request) (string, bool) {
	id := r.PathValue("id")
	if id == "" || len(id) > 1024 {
		writeInvalidRecordRequest(w, r)
		return "", false
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
	case recordops.CodeInvalidRecordID:
		apiError = APIError{Status: http.StatusBadRequest, Code: "invalid_record_id", Message: "Record id is invalid"}
	case recordops.CodeRecordIDConflict:
		apiError = APIError{Status: http.StatusConflict, Code: "record_id_conflict", Message: "Record id already exists"}
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
