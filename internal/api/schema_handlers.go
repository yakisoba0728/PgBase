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
	Catalog(context.Context) (schema.Catalog, error)
}

type schemaEndpoints struct {
	service SchemaService
}

type plannedFieldResponse struct {
	Name     string           `json:"name"`
	Type     schema.FieldType `json:"type"`
	Required bool             `json:"required"`
	Unique   bool             `json:"unique"`
	Indexed  bool             `json:"indexed"`
	Default  json.RawMessage  `json:"default,omitempty"`
	Ordinal  int              `json:"ordinal"`
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
	ID         uuid.UUID        `json:"id"`
	Name       string           `json:"name"`
	ColumnName string           `json:"columnName"`
	Type       schema.FieldType `json:"type"`
	Required   bool             `json:"required"`
	Unique     bool             `json:"unique"`
	Indexed    bool             `json:"indexed"`
	Default    json.RawMessage  `json:"default,omitempty"`
	Ordinal    int              `json:"ordinal"`
}

type collectionResponse struct {
	ID        uuid.UUID       `json:"id"`
	Name      string          `json:"name"`
	TableName string          `json:"tableName"`
	Fields    []fieldResponse `json:"fields"`
	CreatedAt time.Time       `json:"createdAt"`
}

func (e schemaEndpoints) planCreate(w http.ResponseWriter, r *http.Request) {
	var input schema.CreateCollectionInput
	if err := decodeStrictJSON(r, &input); err != nil {
		writeAPIError(w, r, APIError{Status: http.StatusBadRequest, Code: "invalid_request", Message: "Invalid request"})
		return
	}
	if e.service == nil {
		writeSchemaError(w, r, errors.New("schema service is unavailable"))
		return
	}
	plan, err := e.service.PlanCreate(r.Context(), input)
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

func (e schemaEndpoints) applyCreate(w http.ResponseWriter, r *http.Request) {
	var request struct {
		Name        string              `json:"name"`
		Fields      []schema.FieldInput `json:"fields"`
		Checksum    string              `json:"checksum"`
		BaseVersion *int64              `json:"baseVersion"`
	}
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
	collection, err := e.service.ApplyCreate(r.Context(), schema.CreateCollectionInput{Name: request.Name, Fields: request.Fields}, request.Checksum, *request.BaseVersion, principal.Administrator.ID)
	if err != nil {
		writeSchemaError(w, r, err)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusCreated, collectionDTO(collection))
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
	collections := make(map[string]collectionResponse, len(catalog.Collections))
	for name, collection := range catalog.Collections {
		collections[name] = collectionDTO(collection)
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, struct {
		Version     int64                         `json:"version"`
		Collections map[string]collectionResponse `json:"collections"`
	}{Version: catalog.Version, Collections: collections})
}

func plannedCollectionDTO(collection schema.PlannedCollection) plannedCollectionResponse {
	fields := make([]plannedFieldResponse, 0, len(collection.Fields))
	for _, field := range collection.Fields {
		fields = append(fields, plannedFieldResponse{
			Name: field.Name, Type: field.Type, Required: field.Required, Unique: field.Unique,
			Indexed: field.Indexed, Default: append(json.RawMessage(nil), field.Default...), Ordinal: field.Ordinal,
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
			Default: append(json.RawMessage(nil), field.Default...), Ordinal: field.Ordinal,
		})
	}
	return collectionResponse{
		ID: collection.ID, Name: collection.Name, TableName: collection.TableName,
		Fields: fields, CreatedAt: collection.CreatedAt.UTC(),
	}
}

func writeSchemaError(w http.ResponseWriter, r *http.Request, err error) {
	apiError := APIError{Status: http.StatusInternalServerError, Code: "internal_error", Message: "Internal server error"}
	var validation *schema.ValidationError
	switch {
	case errors.As(err, &validation):
		details := make(map[string][]string, len(validation.Details))
		for _, detail := range validation.Details {
			details[detail.Field] = append(details[detail.Field], detail.Code)
		}
		apiError = APIError{Status: http.StatusBadRequest, Code: "validation_failed", Message: "Schema input is invalid", Details: details}
	case errors.Is(err, schema.ErrConflict):
		apiError = APIError{Status: http.StatusConflict, Code: "schema_conflict", Message: "Schema plan conflicts with current state"}
	case errors.Is(err, schema.ErrSynchronizationUnavailable), database.Classify(err) == database.KindUnavailable:
		w.Header().Set("Retry-After", "1")
		apiError = APIError{Status: http.StatusServiceUnavailable, Code: "schema_unavailable", Message: "Schema service is unavailable"}
	}
	writeAPIError(w, r, apiError)
}
