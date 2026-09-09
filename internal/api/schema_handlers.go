package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/google/uuid"
	"github.com/webisnoteasy/PgBase/internal/database"
	"github.com/webisnoteasy/PgBase/internal/schema"
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
	Name      string                 `json:"name"`
	TableName string                 `json:"tableName"`
	Fields    []plannedFieldResponse `json:"fields"`
	Indexes   []schema.PlannedIndex  `json:"indexes,omitempty"`
	View      *schema.PlannedView    `json:"view,omitempty"`
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
	ID        uuid.UUID        `json:"id"`
	Name      string           `json:"name"`
	TableName string           `json:"tableName"`
	Fields    []fieldResponse  `json:"fields"`
	Indexes   []schema.Index   `json:"indexes,omitempty"`
	View      *schema.ViewSpec `json:"view,omitempty"`
	CreatedAt time.Time        `json:"createdAt"`
}

type collectionDeleteResponse struct {
	Name    string `json:"name"`
	Deleted bool   `json:"deleted"`
}

type collectionChangeRequest struct {
	Operation string `json:"operation"`
	Name      string `json:"name"`
	// Fields is a pointer so an update can distinguish an omitted key (which
	// would otherwise silently drop every column) from an explicit empty list.
	Fields      *[]schema.FieldInput `json:"fields"`
	Indexes     *[]schema.IndexInput `json:"indexes"`
	View        *schema.ViewInput    `json:"view,omitempty"`
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
	switch c.Operation {
	case "create":
		indexes := []schema.IndexInput(nil)
		if c.Indexes != nil {
			indexes = *c.Indexes
		}
		return schema.NewCreateChange(schema.CreateCollectionInput{Name: c.Name, Fields: c.fieldInputs(), Indexes: indexes, View: c.View}), true
	case "update":
		if c.View != nil {
			if c.Fields != nil || c.Indexes != nil {
				return schema.Change{}, false
			}
			return schema.NewUpdateChange(schema.UpdateCollectionInput{Name: c.Name, View: c.View}), true
		}
		if c.Fields == nil {
			return schema.Change{}, false
		}
		return schema.NewUpdateChange(schema.UpdateCollectionInput{Name: c.Name, Fields: c.fieldInputs(), Indexes: c.Indexes}), true
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
	catalog, err := e.service.Catalog(r.Context())
	if err != nil {
		writeSchemaError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, catalog)
}

func plannedCollectionDTO(collection schema.PlannedCollection) plannedCollectionResponse {
	fields := make([]plannedFieldResponse, 0, len(collection.Fields))
	for _, field := range collection.Fields {
		fields = append(fields, plannedFieldResponse{
			ID: field.ID, Options: field.Options,
			Name: field.Name, Type: field.Type, Required: field.Required, Unique: field.Unique,
			Indexed: field.Indexed, Default: append(json.RawMessage(nil), field.Default...),
			Relation: plannedRelationDTO(field.Relation), Ordinal: field.Ordinal, Hidden: field.Hidden, Presentable: field.Presentable, Help: field.Help,
		})
	}
	return plannedCollectionResponse{Name: collection.Name, TableName: collection.TableName, Fields: fields, Indexes: append([]schema.PlannedIndex(nil), collection.Indexes...), View: collection.View}
}

func collectionDTO(collection schema.Collection) collectionResponse {
	fields := make([]fieldResponse, 0, len(collection.Fields))
	for _, field := range collection.Fields {
		fields = append(fields, fieldResponse{
			Options: field.Options,
			ID:      field.ID, Name: field.Name, ColumnName: field.ColumnName, Type: field.Type,
			Required: field.Required, Unique: field.Unique, Indexed: field.Indexed,
			Default:  append(json.RawMessage(nil), field.Default...),
			Relation: relationDTO(field.Relation), Ordinal: field.Ordinal, Hidden: field.Hidden, Presentable: field.Presentable, Help: field.Help,
		})
	}
	return collectionResponse{
		ID: collection.ID, Name: collection.Name, TableName: collection.TableName,
		Fields: fields, Indexes: append([]schema.Index(nil), collection.Indexes...), View: collection.View, CreatedAt: collection.CreatedAt.UTC(),
	}
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
