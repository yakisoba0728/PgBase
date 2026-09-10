package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/yakisoba0728/PgBase/internal/database"
	"github.com/yakisoba0728/PgBase/internal/lifecycle"
	"github.com/yakisoba0728/PgBase/internal/query"
	"github.com/yakisoba0728/PgBase/internal/recordops"
	"github.com/yakisoba0728/PgBase/internal/schema"
)

type SchemaService interface {
	Preview(context.Context, schema.Change) (schema.Plan, error)
	Catalog(context.Context) (schema.Catalog, error)
	// One Apply serves both wire shapes, because the difference between them is
	// the intent, not the operation. A guarded client sends an operation
	// identity and gets a durable receipt it can read back after a dropped
	// response; a headerless one sends schema.LegacyApply, whose preflight is
	// what keeps the error ordering and bodies that wire has always had.
	Apply(context.Context, schema.ApplyIntent) (schema.ApplyResult, error)
	OperationStatus(context.Context, schema.OperationRef) (schema.OperationReceipt, error)
	ListOperations(context.Context, schema.OperationQuery) (schema.OperationPage, error)
}

type schemaEndpoints struct {
	service SchemaService
	records RecordService
	hooks   *lifecycle.Registry
}

func (e schemaEndpoints) scaffolds(w http.ResponseWriter, r *http.Request) {
	provider, ok := e.service.(interface {
		Scaffolds() schema.CollectionScaffolds
	})
	if !ok {
		writeSchemaError(w, r, errors.New("schema scaffolds are unavailable"))
		return
	}
	writeJSON(w, http.StatusOK, provider.Scaffolds())
}

func (e schemaEndpoints) truncate(w http.ResponseWriter, r *http.Request) {
	service, ok := e.records.(interface {
		Truncate(context.Context, recordops.TruncateRequest) (recordops.TruncateResult, error)
	})
	if !ok {
		writeSchemaError(w, r, errors.New("record truncate is unavailable"))
		return
	}
	var request struct {
		Name string `json:"name"`
	}
	if decodeStrictJSON(r, &request) != nil || request.Name == "" || request.Name != r.PathValue("collection") {
		writeAPIError(w, r, APIError{Status: http.StatusBadRequest, Code: "invalid_request", Message: "Exact collection name confirmation is required"})
		return
	}
	principal, ok := AdminPrincipalFromContext(r.Context())
	if !ok {
		writeAPIError(w, r, APIError{Status: http.StatusUnauthorized, Code: "authentication_required", Message: "Administrator authentication is required"})
		return
	}
	result, err := service.Truncate(r.Context(), recordops.TruncateRequest{Principal: recordops.Principal{Kind: recordops.PrincipalAdministrator, ID: principal.Administrator.ID}, RequestID: requestID(r.Context()), Collection: request.Name})
	if err != nil {
		writeSchemaError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func (e schemaEndpoints) beginRequest(r *http.Request, operation, name string) (*CollectionRequestEvent, func(error), error) {
	event := &CollectionRequestEvent{Operation: operation, Name: name, RequestID: requestID(r.Context())}
	if principal, ok := AdminPrincipalFromContext(r.Context()); ok {
		event.ActorID = principal.Administrator.ID
	}
	finish, err := beginLifecycle(r.Context(), e.hooks, "collection.request."+operation, event)
	return event, finish, err
}

type plannedFieldResponse struct {
	ID          uuid.UUID                `json:"id,omitzero"`
	Options     schema.FieldOptions      `json:"options,omitzero"`
	Name        string                   `json:"name"`
	Type        schema.FieldType         `json:"type"`
	Required    bool                     `json:"required"`
	Unique      bool                     `json:"unique"`
	Indexed     bool                     `json:"indexed"`
	Default     json.RawMessage          `json:"default,omitempty"`
	Relation    *plannedRelationResponse `json:"relation,omitempty"`
	Ordinal     int                      `json:"ordinal"`
	Hidden      bool                     `json:"hidden"`
	Presentable bool                     `json:"presentable"`
	Help        string                   `json:"help,omitempty"`
	System      bool                     `json:"system,omitempty"`
}

type relationStorageResponse struct {
	Kind schema.RelationStorageKind `json:"kind"`
	Name string                     `json:"name"`
}

type plannedRelationResponse struct {
	TargetCollection   string                      `json:"targetCollection"`
	TargetCollectionID *uuid.UUID                  `json:"targetCollectionId,omitempty"`
	Cardinality        schema.RelationCardinality  `json:"cardinality"`
	MinSelect          int                         `json:"minSelect"`
	MaxSelect          int                         `json:"maxSelect"`
	OnDelete           schema.RelationDeleteAction `json:"onDelete"`
	ValueCodecVersion  int                         `json:"valueCodecVersion"`
	Storage            relationStorageResponse     `json:"storage"`
}

type plannedCollectionResponse struct {
	Name              string                 `json:"name"`
	CompatibilityName string                 `json:"compatibilityName,omitempty"`
	Type              schema.CollectionType  `json:"type"`
	System            bool                   `json:"system,omitempty"`
	ValidationMode    schema.ValidationMode  `json:"validationMode"`
	RecordID          *schema.RecordIDPolicy `json:"recordId,omitempty"`
	TableName         string                 `json:"tableName"`
	Fields            []plannedFieldResponse `json:"fields"`
	Indexes           []schema.PlannedIndex  `json:"indexes,omitempty"`
	View              *schema.PlannedView    `json:"view,omitempty"`
}

type schemaPlanResponse struct {
	BaseVersion int64                     `json:"baseVersion"`
	Checksum    string                    `json:"checksum"`
	Definition  plannedCollectionResponse `json:"definition"`
	SQL         []string                  `json:"sql"`
}

type fieldResponse struct {
	Options     schema.FieldOptions `json:"options,omitzero"`
	ID          uuid.UUID           `json:"id"`
	Name        string              `json:"name"`
	ColumnName  string              `json:"columnName"`
	Type        schema.FieldType    `json:"type"`
	Required    bool                `json:"required"`
	Unique      bool                `json:"unique"`
	Indexed     bool                `json:"indexed"`
	Default     json.RawMessage     `json:"default,omitempty"`
	Relation    *relationResponse   `json:"relation,omitempty"`
	Ordinal     int                 `json:"ordinal"`
	Hidden      bool                `json:"hidden"`
	Presentable bool                `json:"presentable"`
	Help        string              `json:"help,omitempty"`
	System      bool                `json:"system,omitempty"`
}

type relationResponse struct {
	TargetCollectionID uuid.UUID                   `json:"targetCollectionId"`
	Cardinality        schema.RelationCardinality  `json:"cardinality"`
	MinSelect          int                         `json:"minSelect"`
	MaxSelect          int                         `json:"maxSelect"`
	OnDelete           schema.RelationDeleteAction `json:"onDelete"`
	ValueCodecVersion  int                         `json:"valueCodecVersion"`
	Storage            relationStorageResponse     `json:"storage"`
}

type collectionResponse struct {
	ID                uuid.UUID              `json:"id"`
	Name              string                 `json:"name"`
	CompatibilityName string                 `json:"compatibilityName,omitempty"`
	Type              schema.CollectionType  `json:"type,omitempty"`
	System            bool                   `json:"system,omitempty"`
	ValidationMode    schema.ValidationMode  `json:"validationMode,omitempty"`
	RecordID          *schema.RecordIDPolicy `json:"recordId,omitempty"`
	TableName         string                 `json:"tableName"`
	Fields            []fieldResponse        `json:"fields"`
	Indexes           []schema.Index         `json:"indexes,omitempty"`
	View              *schema.ViewSpec       `json:"view,omitempty"`
	CreatedAt         time.Time              `json:"createdAt"`
}

type collectionDeleteResponse struct {
	Name    string `json:"name"`
	Deleted bool   `json:"deleted"`
}

type collectionChangeRequest struct {
	Operation         string                 `json:"operation"`
	Name              string                 `json:"name"`
	CollectionID      *uuid.UUID             `json:"collectionId,omitempty"`
	CompatibilityName *string                `json:"compatibilityName,omitempty"`
	Type              *schema.CollectionType `json:"type,omitempty"`
	System            *bool                  `json:"system,omitempty"`
	ValidationMode    *schema.ValidationMode `json:"validationMode,omitempty"`
	RecordID          *schema.RecordIDPolicy `json:"recordId,omitempty"`
	// Fields is a pointer so an update can distinguish an omitted key (which
	// would otherwise silently drop every column) from an explicit empty list.
	Fields      *[]schema.FieldInput `json:"fields"`
	Indexes     *[]schema.IndexInput `json:"indexes"`
	View        *schema.ViewInput    `json:"view,omitempty"`
	ViewQuery   *string              `json:"viewQuery,omitempty"`
	Checksum    string               `json:"checksum"`
	BaseVersion *int64               `json:"baseVersion"`
}

func (e schemaEndpoints) plan(w http.ResponseWriter, r *http.Request) {
	var request collectionChangeRequest
	if err := decodeStrictJSON(r, &request); err != nil {
		writeAPIError(w, r, APIError{Status: http.StatusBadRequest, Code: "invalid_request", Message: "Invalid request"})
		return
	}
	if e.service == nil {
		writeSchemaError(w, r, errors.New("schema service is unavailable"))
		return
	}
	change, ok := request.change()
	if !ok {
		writeAPIError(w, r, APIError{Status: http.StatusBadRequest, Code: "invalid_request", Message: "Invalid request"})
		return
	}
	plan, err := e.service.Preview(r.Context(), change)
	if err != nil {
		writeSchemaError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, schemaPlanResponse{
		BaseVersion: plan.BaseVersion,
		Checksum:    plan.Checksum,
		Definition:  plannedCollectionDTO(plan.Definition),
		SQL:         append([]string{}, plan.Statements...),
	})
}

func (r collectionChangeRequest) fieldInputs() []schema.FieldInput {
	if r.Fields == nil {
		return nil
	}
	return *r.Fields
}

func (r collectionChangeRequest) resolvedView() (*schema.ViewInput, bool) {
	if r.View != nil && r.ViewQuery != nil {
		return nil, false
	}
	if r.ViewQuery != nil {
		return &schema.ViewInput{SQL: *r.ViewQuery}, true
	}
	return r.View, true
}

func (e schemaEndpoints) apply(w http.ResponseWriter, r *http.Request) {
	var request collectionChangeRequest
	if err := decodeStrictJSON(r, &request); err != nil || request.BaseVersion == nil {
		writeAPIError(w, r, APIError{Status: http.StatusBadRequest, Code: "invalid_request", Message: "Invalid request"})
		return
	}
	principal, ok := AdminPrincipalFromContext(r.Context())
	if !ok {
		writeAPIError(w, r, APIError{Status: http.StatusUnauthorized, Code: "authentication_required", Message: "Administrator authentication is required"})
		return
	}
	if e.service == nil {
		writeSchemaError(w, r, errors.New("schema service is unavailable"))
		return
	}
	_, finish, hookErr := e.beginRequest(r, request.Operation, request.Name)
	if hookErr != nil {
		finish(hookErr)
		writeSchemaError(w, r, hookErr)
		return
	}
	tracked := &collectionHookResponseWriter{ResponseWriter: w}
	w = tracked
	defer func() {
		if tracked.status >= 400 {
			finish(errors.New("collection request failed"))
		} else {
			finish(nil)
		}
	}()
	identity, err := applyIdentityFrom(r)
	if err != nil {
		writeAPIError(w, r, APIError{Status: http.StatusBadRequest, Code: "invalid_request", Message: "Invalid request"})
		return
	}
	if identity.guarded {
		e.applyGuarded(w, r, request, principal.Administrator.ID, identity)
		return
	}
	// A headerless apply keeps the body and status it has always had. Only
	// the header is new, and it says what the client gave up: without an
	// operation identity a dropped response leaves no way to learn the
	// outcome.
	w.Header().Set("PgBase-Compatibility", schemaApplyUnguardedCompatibility)
	change, ok := request.change()
	if !ok {
		writeAPIError(w, r, APIError{Status: http.StatusBadRequest, Code: "invalid_request", Message: "Invalid request"})
		return
	}
	result, err := e.service.Apply(r.Context(), schema.LegacyApply(
		change, schema.Plan{Checksum: request.Checksum, BaseVersion: *request.BaseVersion},
		principal.Administrator.ID,
	))
	if err != nil {
		writeSchemaError(w, r, err)
		return
	}
	// The status is a property of what the client asked for, not of what the
	// kernel reports back, so this reads the request the way it always did.
	switch request.Operation {
	case "create":
		writeJSON(w, http.StatusCreated, collectionDTO(result.Collection))
	case "delete":
		writeJSON(w, http.StatusOK, collectionDeleteResponse{Name: request.Name, Deleted: true})
	default:
		writeJSON(w, http.StatusOK, collectionDTO(result.Collection))
	}
}

// change maps the request onto the sealed schema intent. An update with no
// fields key is rejected here, before either apply path runs, because an
// omitted key would otherwise read as "drop every column".
func (c collectionChangeRequest) change() (schema.Change, bool) {
	view, valid := c.resolvedView()
	if !valid {
		return schema.Change{}, false
	}
	switch c.Operation {
	case "create":
		indexes := []schema.IndexInput(nil)
		if c.Indexes != nil {
			indexes = *c.Indexes
		}
		input := schema.CreateCollectionInput{Name: c.Name, RecordID: c.RecordID, Fields: c.fieldInputs(), Indexes: indexes, View: view}
		if c.CompatibilityName != nil {
			input.CompatibilityName = *c.CompatibilityName
		}
		if c.Type != nil {
			input.Type = *c.Type
		}
		if c.System != nil {
			input.System = *c.System
		}
		if c.ValidationMode != nil {
			input.ValidationMode = *c.ValidationMode
		}
		return schema.NewCreateChange(input), true
	case "update":
		if view != nil {
			if c.Fields != nil || c.Indexes != nil {
				return schema.Change{}, false
			}
			return schema.NewUpdateChange(schema.UpdateCollectionInput{Name: c.Name, CollectionID: c.CollectionID, CompatibilityName: c.CompatibilityName, Type: c.Type, System: c.System, ValidationMode: c.ValidationMode, RecordID: c.RecordID, View: view}), true
		}
		if c.Fields == nil && c.CollectionID == nil {
			return schema.Change{}, false
		}
		return schema.NewUpdateChange(schema.UpdateCollectionInput{Name: c.Name, CollectionID: c.CollectionID, CompatibilityName: c.CompatibilityName, Type: c.Type, System: c.System, ValidationMode: c.ValidationMode, RecordID: c.RecordID, Fields: c.fieldInputs(), Indexes: c.Indexes}), true
	case "delete":
		return schema.NewDeleteChange(c.Name), true
	default:
		return schema.Change{}, false
	}
}

func (e schemaEndpoints) applyGuarded(
	w http.ResponseWriter, r *http.Request,
	request collectionChangeRequest, actorID uuid.UUID, identity applyIdentity,
) {
	change, ok := request.change()
	if !ok {
		writeAPIError(w, r, APIError{Status: http.StatusBadRequest, Code: "invalid_request", Message: "Invalid request"})
		return
	}
	// The kernel derives the canonical request hash from this typed intent.
	// A malformed identity or checksum is refused before any domain call, so
	// a rejected request never reaches the ledger.
	intent, err := schema.GuardedApply(
		change, schema.Plan{Checksum: request.Checksum, BaseVersion: *request.BaseVersion},
		actorID, identity.operationID, identity.key,
	)
	if err != nil {
		writeAPIError(w, r, APIError{Status: http.StatusBadRequest, Code: "invalid_request", Message: "Invalid request"})
		return
	}
	w.Header().Set(operationIDHeader, identity.operationID.String())
	result, err := e.service.Apply(r.Context(), intent)
	if err != nil {
		e.writeGuardedApplyError(w, r, actorID, identity, result.Receipt, err)
		return
	}
	w.Header().Set("Idempotency-Replayed", strconv.FormatBool(result.Replayed))
	if result.Replayed {
		// The ledger stores the receipt, not the collection the first attempt
		// returned, so a replay answers with what it actually knows instead
		// of a collection body reconstructed from nothing.
		receipt := result.Receipt
		writeJSON(w, http.StatusOK, schemaOperationOutcome{
			OperationID: identity.operationID, Status: receipt.Status,
			StatusURL: operationStatusURL(identity.operationID), Receipt: &receipt,
		})
		return
	}
	switch {
	case result.Deleted:
		writeJSON(w, http.StatusOK, collectionDeleteResponse{Name: request.Name, Deleted: true})
	case request.Operation == "create":
		writeJSON(w, http.StatusCreated, collectionDTO(result.Collection))
	default:
		writeJSON(w, http.StatusOK, collectionDTO(result.Collection))
	}
}

func (e schemaEndpoints) catalog(w http.ResponseWriter, r *http.Request) {
	if e.service == nil {
		writeSchemaError(w, r, errors.New("schema service is unavailable"))
		return
	}
	_, finish, hookErr := e.beginRequest(r, "list", "")
	if hookErr != nil {
		finish(hookErr)
		writeSchemaError(w, r, hookErr)
		return
	}
	catalog, err := e.service.Catalog(r.Context())
	finish(err)
	if err != nil {
		writeSchemaError(w, r, err)
		return
	}
	if r.URL.Query().Get("compat") != "pocketbase" {
		collections := make(map[string]collectionResponse, len(catalog.Collections))
		for name, collection := range catalog.Collections {
			collections[name] = collectionDTO(collection)
		}
		writeJSON(w, http.StatusOK, map[string]any{"version": catalog.Version, "collections": collections})
		return
	}
	page, perPage := 1, 30
	if raw := r.URL.Query().Get("page"); raw != "" {
		value, parseErr := strconv.Atoi(raw)
		if parseErr != nil || value < 1 {
			writeAPIError(w, r, APIError{Status: 400, Code: "invalid_request", Message: "Invalid request"})
			return
		}
		page = value
	}
	if raw := r.URL.Query().Get("perPage"); raw != "" {
		value, parseErr := strconv.Atoi(raw)
		if parseErr != nil || value < 1 || value > 200 {
			writeAPIError(w, r, APIError{Status: 400, Code: "invalid_request", Message: "Invalid request"})
			return
		}
		perPage = value
	}
	fields := catalogScalarFields()
	filterText := strings.TrimSpace(r.URL.Query().Get("filter"))
	var predicate query.ScalarPredicate
	if filterText != "" {
		predicate, err = query.CompileScalarPredicate(filterText, fields)
		if err != nil {
			writeAPIError(w, r, APIError{Status: 400, Code: "invalid_request", Message: "Invalid request"})
			return
		}
	}
	items := make([]schema.Collection, 0, len(catalog.Collections))
	for _, collection := range catalog.Collections {
		if filterText != "" && !predicate.Evaluate(catalogScalarValues(collection)) {
			continue
		}
		items = append(items, collection)
	}
	sorts, parseErr := query.ParseSort(r.URL.Query().Get("sort"))
	if parseErr != nil {
		writeAPIError(w, r, APIError{Status: 400, Code: "invalid_request", Message: "Invalid request"})
		return
	}
	if len(sorts) == 0 {
		sorts = []query.SortKey{{Field: "name"}}
	}
	for _, term := range sorts {
		if _, ok := fields[term.Field]; !ok {
			writeAPIError(w, r, APIError{Status: 400, Code: "invalid_request", Message: "Invalid request"})
			return
		}
	}
	tieDescending := sorts[len(sorts)-1].Desc
	sort.Slice(items, func(i, j int) bool {
		left, right := catalogScalarValues(items[i]), catalogScalarValues(items[j])
		for _, term := range sorts {
			comparison := compareCatalogScalar(fields[term.Field], left[term.Field], right[term.Field])
			if comparison == 0 {
				continue
			}
			if term.Desc {
				return comparison > 0
			}
			return comparison < 0
		}
		if items[i].ID != items[j].ID {
			return items[i].ID.String() < items[j].ID.String()
		}
		// Test fixtures and imported legacy rows can omit IDs. Preserve the
		// historical deterministic name order until they are assigned one.
		if tieDescending {
			return items[i].Name > items[j].Name
		}
		return items[i].Name < items[j].Name
	})
	total := len(items)
	start := total
	if page-1 <= total/perPage {
		start = (page - 1) * perPage
	}
	end := start + perPage
	if end > total {
		end = total
	}
	totalPages := (total + perPage - 1) / perPage
	responses := make([]collectionResponse, 0, end-start)
	for _, item := range items[start:end] {
		responses = append(responses, collectionDTO(item))
	}
	writeJSON(w, http.StatusOK, map[string]any{"page": page, "perPage": perPage, "totalItems": total, "totalPages": totalPages, "items": responses})
}

func catalogScalarFields() map[string]query.ScalarKind {
	return map[string]query.ScalarKind{
		"id": query.ScalarString, "name": query.ScalarString, "compatibilityName": query.ScalarString,
		"type": query.ScalarString, "system": query.ScalarBool, "validationMode": query.ScalarString,
		"tableName": query.ScalarString, "created": query.ScalarTime, "fieldCount": query.ScalarNumber, "indexCount": query.ScalarNumber,
	}
}

func catalogScalarValues(collection schema.Collection) map[string]query.ScalarValue {
	return map[string]query.ScalarValue{
		"id": {String: collection.ID.String()}, "name": {String: collection.Name}, "compatibilityName": {String: collection.CompatibilityName},
		"type": {String: string(collection.Type)}, "system": {Bool: collection.System}, "validationMode": {String: string(collection.ValidationMode)},
		"tableName": {String: collection.TableName}, "created": {Time: collection.CreatedAt.UTC()}, "fieldCount": {Number: float64(len(collection.Fields))}, "indexCount": {Number: float64(len(collection.Indexes))},
	}
}

func compareCatalogScalar(kind query.ScalarKind, left, right query.ScalarValue) int {
	switch kind {
	case query.ScalarBool:
		if left.Bool == right.Bool {
			return 0
		}
		if !left.Bool {
			return -1
		}
		return 1
	case query.ScalarNumber:
		if left.Number < right.Number {
			return -1
		}
		if left.Number > right.Number {
			return 1
		}
		return 0
	case query.ScalarTime:
		if left.Time.Before(right.Time) {
			return -1
		}
		if left.Time.After(right.Time) {
			return 1
		}
		return 0
	default:
		if left.String < right.String {
			return -1
		}
		if left.String > right.String {
			return 1
		}
		return 0
	}
}

func (e schemaEndpoints) collection(w http.ResponseWriter, r *http.Request) {
	if e.service == nil {
		writeSchemaError(w, r, errors.New("schema service is unavailable"))
		return
	}
	catalog, err := e.service.Catalog(r.Context())
	if err != nil {
		writeSchemaError(w, r, err)
		return
	}
	key := strings.ToLower(r.PathValue("collection"))
	for _, collection := range catalog.Collections {
		if strings.ToLower(collection.Name) == key || strings.ToLower(collection.CompatibilityName) == key || strings.ToLower(collection.ID.String()) == key {
			event, finish, hookErr := e.beginRequest(r, "view", collection.Name)
			if hookErr != nil {
				finish(hookErr)
				writeSchemaError(w, r, hookErr)
				return
			}
			event.Collection = &collection
			finish(nil)
			writeJSON(w, http.StatusOK, collectionDTO(collection))
			return
		}
	}
	writeSchemaError(w, r, schema.ErrCollectionNotFound)
}

type collectionHookResponseWriter struct {
	http.ResponseWriter
	status int
}

func (w *collectionHookResponseWriter) WriteHeader(status int) {
	w.status = status
	w.ResponseWriter.WriteHeader(status)
}

func (w *collectionHookResponseWriter) Write(body []byte) (int, error) {
	if w.status == 0 {
		w.status = http.StatusOK
	}
	return w.ResponseWriter.Write(body)
}

func (e schemaEndpoints) sampleView(w http.ResponseWriter, r *http.Request) {
	service, ok := e.service.(interface {
		SampleView(context.Context, string) (schema.ViewSample, error)
	})
	if !ok {
		writeSchemaError(w, r, errors.New("schema service is unavailable"))
		return
	}
	var request struct {
		Query string `json:"query"`
	}
	if err := decodeStrictJSON(r, &request); err != nil {
		writeAPIError(w, r, APIError{Status: http.StatusBadRequest, Code: "invalid_request", Message: "Invalid request"})
		return
	}
	result, err := service.SampleView(r.Context(), request.Query)
	if err != nil {
		writeSchemaError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func plannedCollectionDTO(collection schema.PlannedCollection) plannedCollectionResponse {
	fields := make([]plannedFieldResponse, 0, len(collection.Fields))
	for _, field := range collection.Fields {
		fields = append(fields, plannedFieldResponse{
			ID: field.ID, Options: field.Options,
			Name: field.Name, Type: field.Type, Required: field.Required, Unique: field.Unique,
			Indexed: field.Indexed, Default: append(json.RawMessage(nil), field.Default...),
			Relation: plannedRelationDTO(field.Relation), Ordinal: field.Ordinal, Hidden: field.Hidden, Presentable: field.Presentable, Help: field.Help, System: field.System,
		})
	}
	return plannedCollectionResponse{Name: collection.Name, CompatibilityName: collection.CompatibilityName, Type: collection.Type, System: collection.System, ValidationMode: collection.ValidationMode, RecordID: recordIDPolicyDTO(collection.RecordID), TableName: collection.TableName, Fields: fields, Indexes: append([]schema.PlannedIndex(nil), collection.Indexes...), View: collection.View}
}

func collectionDTO(collection schema.Collection) collectionResponse {
	fields := make([]fieldResponse, 0, len(collection.Fields))
	for _, field := range collection.Fields {
		fields = append(fields, fieldResponse{
			Options: field.Options,
			ID:      field.ID, Name: field.Name, ColumnName: field.ColumnName, Type: field.Type,
			Required: field.Required, Unique: field.Unique, Indexed: field.Indexed,
			Default:  append(json.RawMessage(nil), field.Default...),
			Relation: relationDTO(field.Relation), Ordinal: field.Ordinal, Hidden: field.Hidden, Presentable: field.Presentable, Help: field.Help, System: field.System,
		})
	}
	return collectionResponse{
		ID: collection.ID, Name: collection.Name, CompatibilityName: collection.CompatibilityName, Type: collection.Type, System: collection.System, ValidationMode: collection.ValidationMode, RecordID: recordIDPolicyDTO(collection.RecordID), TableName: collection.TableName,
		Fields: fields, Indexes: append([]schema.Index(nil), collection.Indexes...), View: collection.View, CreatedAt: collection.CreatedAt.UTC(),
	}
}

func recordIDPolicyDTO(policy schema.RecordIDPolicy) *schema.RecordIDPolicy {
	if policy.Kind != schema.RecordIDText {
		return nil
	}
	copy := policy
	return &copy
}

func plannedRelationDTO(relation *schema.PlannedRelation) *plannedRelationResponse {
	if relation == nil {
		return nil
	}
	var targetCollectionID *uuid.UUID
	if relation.TargetCollectionID != nil {
		value := *relation.TargetCollectionID
		targetCollectionID = &value
	}
	return &plannedRelationResponse{
		TargetCollection:   relation.TargetCollection,
		TargetCollectionID: targetCollectionID,
		Cardinality:        relation.Cardinality,
		MinSelect:          relation.MinSelect,
		MaxSelect:          relation.MaxSelect,
		OnDelete:           relation.OnDelete,
		ValueCodecVersion:  relation.ValueCodecVersion,
		Storage:            relationStorageDTO(relation.Storage),
	}
}

func relationDTO(relation *schema.RelationSpec) *relationResponse {
	if relation == nil {
		return nil
	}
	return &relationResponse{
		TargetCollectionID: relation.TargetCollectionID,
		Cardinality:        relation.Cardinality,
		MinSelect:          relation.MinSelect,
		MaxSelect:          relation.MaxSelect,
		OnDelete:           relation.OnDelete,
		ValueCodecVersion:  relation.ValueCodecVersion,
		Storage:            relationStorageDTO(relation.Storage),
	}
}

func relationStorageDTO(storage schema.RelationStorage) relationStorageResponse {
	return relationStorageResponse{Kind: storage.Kind, Name: storage.Name}
}

func writeSchemaError(w http.ResponseWriter, r *http.Request, err error) {
	apiError := APIError{Status: http.StatusInternalServerError, Code: "internal_error", Message: "Internal server error"}
	var validation *schema.ValidationError
	switch {
	case errors.As(err, &validation):
		status := http.StatusUnprocessableEntity
		code := "validation_failed"
		message := "Schema input is invalid"
		for _, detail := range validation.Details {
			if detail.Code == "collection_referenced" {
				status = http.StatusConflict
				code = "collection_referenced"
				message = "Collection is referenced"
				break
			}
		}
		details := make(map[string][]string, len(validation.Details))
		for _, detail := range validation.Details {
			details[detail.Field] = append(details[detail.Field], detail.Code)
		}
		apiError = APIError{Status: status, Code: code, Message: message, Details: details}
	case errors.Is(err, schema.ErrCollectionNotFound):
		apiError = APIError{Status: http.StatusNotFound, Code: "not_found", Message: "Not found"}
	case errors.Is(err, schema.ErrDataConstraint):
		apiError = APIError{Status: http.StatusUnprocessableEntity, Code: "validation_failed", Message: "Existing data violates the schema change"}
	case errors.Is(err, schema.ErrCatalogLimitExceeded):
		apiError = APIError{Status: http.StatusUnprocessableEntity, Code: "catalog_limit_exceeded", Message: "Schema catalog exceeds the supported size"}
	case errors.Is(err, schema.ErrConflict):
		apiError = APIError{Status: http.StatusConflict, Code: "schema_conflict", Message: "Schema plan conflicts with current state"}
	case errors.Is(err, schema.ErrSynchronizationUnavailable), database.Classify(err) == database.KindUnavailable:
		w.Header().Set("Retry-After", "1")
		apiError = APIError{Status: http.StatusServiceUnavailable, Code: "schema_unavailable", Message: "Schema service is unavailable"}
	}
	writeAPIError(w, r, apiError)
}
