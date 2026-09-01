package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"github.com/google/uuid"
	"github.com/webisnoteasy/PgBase/internal/database"
	"github.com/webisnoteasy/PgBase/internal/schema"
)

type SchemaService interface {
	PlanCreate(context.Context, schema.CreateCollectionInput) (schema.Plan, error)
	ApplyCreate(context.Context, schema.CreateCollectionInput, string, int64, uuid.UUID) (schema.Collection, error)
	PlanUpdate(context.Context, schema.UpdateCollectionInput) (schema.Plan, error)
	ApplyUpdate(context.Context, schema.UpdateCollectionInput, string, int64, uuid.UUID) (schema.Collection, error)
	PlanDelete(context.Context, string) (schema.Plan, error)
	ApplyDelete(context.Context, string, string, int64, uuid.UUID) error
	Catalog(context.Context) (schema.Catalog, error)
}

type schemaEndpoints struct {
	service SchemaService
}

type plannedFieldResponse struct {
	Name     string                   `json:"name"`
	Type     schema.FieldType         `json:"type"`
	Required bool                     `json:"required"`
	Unique   bool                     `json:"unique"`
	Indexed  bool                     `json:"indexed"`
	Default  json.RawMessage          `json:"default,omitempty"`
	Relation *plannedRelationResponse `json:"relation,omitempty"`
	Ordinal  int                      `json:"ordinal"`
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
}

type schemaPlanResponse struct {
	BaseVersion int64                     `json:"baseVersion"`
	Checksum    string                    `json:"checksum"`
	Definition  plannedCollectionResponse `json:"definition"`
	SQL         []string                  `json:"sql"`
}

type fieldResponse struct {
	ID         uuid.UUID         `json:"id"`
	Name       string            `json:"name"`
	ColumnName string            `json:"columnName"`
	Type       schema.FieldType  `json:"type"`
	Required   bool              `json:"required"`
	Unique     bool              `json:"unique"`
	Indexed    bool              `json:"indexed"`
	Default    json.RawMessage   `json:"default,omitempty"`
	Relation   *relationResponse `json:"relation,omitempty"`
	Ordinal    int               `json:"ordinal"`
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
	ID        uuid.UUID       `json:"id"`
	Name      string          `json:"name"`
	TableName string          `json:"tableName"`
	Fields    []fieldResponse `json:"fields"`
	CreatedAt time.Time       `json:"createdAt"`
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
	var plan schema.Plan
	var err error
	switch request.Operation {
	case "create":
		plan, err = e.service.PlanCreate(r.Context(), schema.CreateCollectionInput{Name: request.Name, Fields: request.fieldInputs()})
	case "update":
		if request.Fields == nil {
			writeAPIError(w, r, APIError{Status: http.StatusBadRequest, Code: "invalid_request", Message: "Invalid request"})
			return
		}
		plan, err = e.service.PlanUpdate(r.Context(), schema.UpdateCollectionInput{Name: request.Name, Fields: request.fieldInputs()})
	case "delete":
		plan, err = e.service.PlanDelete(r.Context(), request.Name)
	default:
		writeAPIError(w, r, APIError{Status: http.StatusBadRequest, Code: "invalid_request", Message: "Invalid request"})
		return
	}
	if err != nil {
		writeSchemaError(w, r, err)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, schemaPlanResponse{
		BaseVersion: plan.BaseVersion,
		Checksum:    plan.Checksum,
		Definition:  plannedCollectionDTO(plan.Definition),
		SQL:         append([]string(nil), plan.Statements...),
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
	w.Header().Set("Cache-Control", "no-store")
	switch request.Operation {
	case "create":
		collection, err := e.service.ApplyCreate(r.Context(), schema.CreateCollectionInput{Name: request.Name, Fields: request.fieldInputs()}, request.Checksum, *request.BaseVersion, principal.Administrator.ID)
		if err != nil {
			writeSchemaError(w, r, err)
			return
		}
		writeJSON(w, http.StatusCreated, collectionDTO(collection))
	case "update":
		if request.Fields == nil {
			writeAPIError(w, r, APIError{Status: http.StatusBadRequest, Code: "invalid_request", Message: "Invalid request"})
			return
		}
		collection, err := e.service.ApplyUpdate(r.Context(), schema.UpdateCollectionInput{Name: request.Name, Fields: request.fieldInputs()}, request.Checksum, *request.BaseVersion, principal.Administrator.ID)
		if err != nil {
			writeSchemaError(w, r, err)
			return
		}
		writeJSON(w, http.StatusOK, collectionDTO(collection))
	case "delete":
		if err := e.service.ApplyDelete(r.Context(), request.Name, request.Checksum, *request.BaseVersion, principal.Administrator.ID); err != nil {
			writeSchemaError(w, r, err)
			return
		}
		writeJSON(w, http.StatusOK, collectionDeleteResponse{Name: request.Name, Deleted: true})
	default:
		writeAPIError(w, r, APIError{Status: http.StatusBadRequest, Code: "invalid_request", Message: "Invalid request"})
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
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, catalog)
}

func plannedCollectionDTO(collection schema.PlannedCollection) plannedCollectionResponse {
	fields := make([]plannedFieldResponse, 0, len(collection.Fields))
	for _, field := range collection.Fields {
		fields = append(fields, plannedFieldResponse{
			Name: field.Name, Type: field.Type, Required: field.Required, Unique: field.Unique,
			Indexed: field.Indexed, Default: append(json.RawMessage(nil), field.Default...),
			Relation: plannedRelationDTO(field.Relation), Ordinal: field.Ordinal,
		})
	}
	return plannedCollectionResponse{Name: collection.Name, TableName: collection.TableName, Fields: fields}
}

func collectionDTO(collection schema.Collection) collectionResponse {
	fields := make([]fieldResponse, 0, len(collection.Fields))
	for _, field := range collection.Fields {
		fields = append(fields, fieldResponse{
			ID: field.ID, Name: field.Name, ColumnName: field.ColumnName, Type: field.Type,
			Required: field.Required, Unique: field.Unique, Indexed: field.Indexed,
			Default:  append(json.RawMessage(nil), field.Default...),
			Relation: relationDTO(field.Relation), Ordinal: field.Ordinal,
		})
	}
	return collectionResponse{
		ID: collection.ID, Name: collection.Name, TableName: collection.TableName,
		Fields: fields, CreatedAt: collection.CreatedAt.UTC(),
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
