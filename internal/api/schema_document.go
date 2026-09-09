package api

import (
	"context"
	"encoding/json"
	"net/http"

	"github.com/google/uuid"
	"github.com/webisnoteasy/PgBase/internal/accessrule"
	"github.com/webisnoteasy/PgBase/internal/identity"
	"github.com/webisnoteasy/PgBase/internal/schema"
)

type schemaDocumentService interface {
	ExportSchema(context.Context) (schema.SchemaDocument, error)
	PreviewSchemaImport(context.Context, schema.SchemaDocument) (schema.SchemaImportPlan, error)
	ApplySchemaImport(context.Context, schema.SchemaImportIntent) (schema.SchemaImportResult, error)
	SchemaImportStatus(context.Context, uuid.UUID, uuid.UUID) (schema.SchemaImportResult, error)
	ValidateSchemaImportPlan(schema.SchemaImportPlan, func(schema.Collection, json.RawMessage) error) error
}

func schemaDocumentRoutes(dependencies Dependencies) []route {
	service, ok := dependencies.Schema.(schemaDocumentService)
	if !ok {
		return nil
	}
	handler := schemaDocumentEndpoints{service: service}
	return []route{
		{method: "GET", pattern: "/api/admin/schema/export", access: accessAdminSameOriginRead, response: atomicJSON(maximumCatalogResponseBytes), deadline: deadlineDefault, handler: http.HandlerFunc(handler.export)},
		{method: "POST", pattern: "/api/admin/schema/import/preview", access: accessAdminSession, response: atomicJSON(maximumCatalogResponseBytes), deadline: deadlineDefault, maximumRequestBytes: maximumCatalogResponseBytes, schemaDocument: true, handler: http.HandlerFunc(handler.preview)},
		{method: "POST", pattern: "/api/admin/schema/import/apply", access: accessAdminSession, response: atomicJSON(maximumCatalogResponseBytes), deadline: deadlineSchemaApply, maximumRequestBytes: maximumCatalogResponseBytes, schemaDocument: true, handler: http.HandlerFunc(handler.apply)},
		{method: "GET", pattern: "/api/admin/schema/import/operations/{id}", access: accessAdminSameOriginRead, response: atomicJSON(maximumAdminResponseBytes), deadline: deadlineDefault, handler: http.HandlerFunc(handler.status)},
	}
}

type schemaDocumentEndpoints struct{ service schemaDocumentService }

func (e schemaDocumentEndpoints) status(w http.ResponseWriter, r *http.Request) {
	principal, ok := AdminPrincipalFromContext(r.Context())
	id, err := uuid.Parse(r.PathValue("id"))
	if !ok || err != nil || id == uuid.Nil {
		writeAPIError(w, r, APIError{Status: http.StatusNotFound, Code: "not_found", Message: "Operation not found"})
		return
	}
	result, err := e.service.SchemaImportStatus(r.Context(), principal.Administrator.ID, id)
	if err != nil {
		writeSchemaError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func (e schemaDocumentEndpoints) export(w http.ResponseWriter, r *http.Request) {
	document, err := e.service.ExportSchema(r.Context())
	if err != nil {
		writeSchemaError(w, r, err)
		return
	}
	w.Header().Set("Content-Disposition", `attachment; filename="pgbase-schema.json"`)
	writeJSON(w, http.StatusOK, document)
}

func (e schemaDocumentEndpoints) preview(w http.ResponseWriter, r *http.Request) {
	var document schema.SchemaDocument
	if decodeStrictJSON(r, &document) != nil {
		writeInvalidSchemaDocument(w, r)
		return
	}
	plan, err := e.service.PreviewSchemaImport(r.Context(), document)
	if err == nil {
		err = e.service.ValidateSchemaImportPlan(plan, validateCollectionRules)
	}
	if err != nil {
		writeSchemaError(w, r, err)
		return
	}
	changes := make([]schemaPlanResponse, len(plan.Changes))
	for i, change := range plan.Changes {
		changes[i] = schemaPlanResponse{BaseVersion: change.BaseVersion, Checksum: change.Checksum, Definition: plannedCollectionDTO(change.Definition), SQL: append([]string(nil), change.Statements...)}
	}
	writeJSON(w, http.StatusOK, struct {
		BaseVersion    int64                 `json:"baseVersion"`
		Checksum       string                `json:"checksum"`
		PolicyChecksum string                `json:"policyChecksum"`
		Changes        []schemaPlanResponse  `json:"changes"`
		Document       schema.SchemaDocument `json:"document"`
	}{plan.BaseVersion, plan.Checksum, plan.PolicyChecksum, changes, plan.Document})
}

func (e schemaDocumentEndpoints) apply(w http.ResponseWriter, r *http.Request) {
	var request struct {
		Document    schema.SchemaDocument `json:"document"`
		BaseVersion *int64                `json:"baseVersion"`
		Checksum    string                `json:"checksum"`
	}
	if decodeStrictJSON(r, &request) != nil || request.BaseVersion == nil || request.Checksum == "" {
		writeInvalidSchemaDocument(w, r)
		return
	}
	principal, ok := AdminPrincipalFromContext(r.Context())
	if !ok {
		writeAPIError(w, r, APIError{Status: http.StatusUnauthorized, Code: "authentication_required", Message: "Administrator authentication is required"})
		return
	}
	identity, identityErr := applyIdentityFrom(r)
	if identityErr != nil || !identity.guarded {
		writeInvalidSchemaDocument(w, r)
		return
	}
	result, err := e.service.ApplySchemaImport(r.Context(), schema.SchemaImportIntent{Document: request.Document, BaseVersion: *request.BaseVersion, Checksum: request.Checksum, Actor: principal.Administrator.ID, OperationID: identity.operationID, IdempotencyKey: identity.key, ValidateRules: validateCollectionRules})
	if err != nil {
		writeSchemaError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func validateCollectionRules(collection schema.Collection, raw json.RawMessage) error {
	rules, err := accessrule.Parse(raw)
	if err != nil {
		return err
	}
	for _, action := range []string{"list", "view", "create", "update", "delete"} {
		rule, _ := rules.Action(action)
		if _, _, err := accessrule.Compile(rule, collection, collection.TableName, identity.Subject{Kind: identity.Administrator, ID: uuid.New()}, 0, action == "create"); err != nil {
			return err
		}
	}
	return nil
}

func writeInvalidSchemaDocument(w http.ResponseWriter, r *http.Request) {
	writeAPIError(w, r, APIError{Status: http.StatusBadRequest, Code: "invalid_schema_document", Message: "Invalid schema document"})
}
