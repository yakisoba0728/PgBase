package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/yakisoba0728/PgBase/internal/lifecycle"
	"github.com/yakisoba0728/PgBase/internal/recordops"
	"github.com/yakisoba0728/PgBase/internal/schema"
)

type panicLifecycleRecordService struct{ fakeRecordService }

func (*panicLifecycleRecordService) Create(context.Context, recordops.CreateRequest) (recordops.MutationResult, error) {
	panic("record service panic")
}

type panicLifecycleBatchService struct{ fakeRecordService }

func (*panicLifecycleBatchService) Batch(context.Context, recordops.BatchRequest) ([]recordops.MutationResult, error) {
	panic("batch service panic")
}

type panicLifecycleSchemaService struct{ fakeSchemaService }

func (*panicLifecycleSchemaService) ExportSchema(context.Context) (schema.SchemaDocument, error) {
	return schema.SchemaDocument{}, nil
}
func (*panicLifecycleSchemaService) PreviewSchemaImport(context.Context, schema.SchemaDocument) (schema.SchemaImportPlan, error) {
	return schema.SchemaImportPlan{}, nil
}
func (*panicLifecycleSchemaService) ApplySchemaImport(context.Context, schema.SchemaImportIntent) (schema.SchemaImportResult, error) {
	panic("schema service panic")
}
func (*panicLifecycleSchemaService) SchemaImportStatus(context.Context, uuid.UUID, uuid.UUID) (schema.SchemaImportResult, error) {
	return schema.SchemaImportResult{}, nil
}
func (*panicLifecycleSchemaService) ValidateSchemaImportPlan(schema.SchemaImportPlan, func(schema.Collection, json.RawMessage) error) error {
	return nil
}

func TestRequestLifecyclePairsServicePanicsThroughHandlers(t *testing.T) {
	tests := []struct {
		name, kind, method, path, body string
		service                        RecordService
	}{
		{"record", "record.create", http.MethodPost, "/api/v1/collections/posts/records", `{"title":"panic"}`, &panicLifecycleRecordService{}},
		{"batch", "record.batch", http.MethodPost, "/api/v1/collections/batch", `{"operations":[{"operation":"create","collection":"posts","input":{"title":"panic"},"idempotencyKey":"panic-batch-key-0001"}]}`, &panicLifecycleBatchService{}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			registry := &lifecycle.Registry{}
			calls := 0
			_, err := registry.On(test.kind, lifecycle.Hook{After: func(_ context.Context, event *lifecycle.Event) {
				calls++
				if event.Err == nil {
					t.Error("panic paired as success")
				}
			}})
			if err != nil {
				t.Fatal(err)
			}
			dependencies := recordTestDependencies(&fakeAdminAuth{authenticateAdmin: validSession().Administrator}, test.service)
			dependencies.LifecycleHooks = registry
			request := authenticatedRecordRequest(test.method, test.path, test.body, true)
			if test.name == "record" {
				request.Header.Set("Idempotency-Key", "panic-record-key-0001")
			}
			response := httptest.NewRecorder()
			mustRouter(t, dependencies).ServeHTTP(response, request)
			if response.Code != http.StatusInternalServerError || calls != 1 {
				t.Fatalf("status=%d after=%d body=%s", response.Code, calls, response.Body.String())
			}
		})
	}

	t.Run("schema", func(t *testing.T) {
		registry := &lifecycle.Registry{}
		calls := 0
		_, err := registry.On("schema.import.apply", lifecycle.Hook{After: func(_ context.Context, event *lifecycle.Event) {
			calls++
			if event.Err == nil {
				t.Error("panic paired as success")
			}
		}})
		if err != nil {
			t.Fatal(err)
		}
		service := &panicLifecycleSchemaService{}
		dependencies := schemaTestDependencies(&fakeAdminAuth{authenticateAdmin: validSession().Administrator}, service)
		dependencies.LifecycleHooks = registry
		request := httptest.NewRequest(http.MethodPost, "/api/admin/schema/import/apply", strings.NewReader(`{"document":{"format":"pgbase.schema/v1"},"baseVersion":1,"checksum":"sealed"}`))
		protectSchemaMutationRequest(request)
		request.Header.Set("Content-Type", "application/json")
		request.AddCookie(&http.Cookie{Name: adminCookieName, Value: "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA"})
		request.Header.Set(operationIDHeader, uuid.NewString())
		request.Header.Set(idempotencyKeyHeader, "panic-schema-key-0001")
		response := httptest.NewRecorder()
		mustRouter(t, dependencies).ServeHTTP(response, request)
		if response.Code != http.StatusInternalServerError || calls != 1 {
			t.Fatalf("status=%d after=%d body=%s", response.Code, calls, response.Body.String())
		}
	})

	t.Run("collection apply", func(t *testing.T) {
		registry := &lifecycle.Registry{}
		calls := 0
		_, err := registry.On("collection.request.create", lifecycle.Hook{After: func(_ context.Context, event *lifecycle.Event) {
			calls++
			if event.Err == nil {
				t.Error("panic paired as success")
			}
		}})
		if err != nil {
			t.Fatal(err)
		}
		service := &fakeSchemaService{applyHook: func(context.Context) (schema.ApplyResult, error) {
			panic("collection apply panic")
		}}
		dependencies := schemaTestDependencies(&fakeAdminAuth{authenticateAdmin: validSession().Administrator}, service)
		dependencies.LifecycleHooks = registry
		request := httptest.NewRequest(http.MethodPost, "/api/admin/collections/apply", strings.NewReader(`{"operation":"create","name":"posts","fields":[],"baseVersion":1,"checksum":"sealed"}`))
		protectSchemaMutationRequest(request)
		request.Header.Set("Content-Type", "application/json")
		request.AddCookie(&http.Cookie{Name: adminCookieName, Value: "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA"})
		response := httptest.NewRecorder()
		mustRouter(t, dependencies).ServeHTTP(response, request)
		if response.Code != http.StatusInternalServerError || calls != 1 {
			t.Fatalf("status=%d after=%d body=%s", response.Code, calls, response.Body.String())
		}
	})
}
