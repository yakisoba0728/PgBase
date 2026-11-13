package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/yakisoba0728/PgBase/internal/lifecycle"
	"github.com/yakisoba0728/PgBase/internal/recordops"
)

type hookBatchService struct {
	fakeRecordService
	calls int
}

func (s *hookBatchService) Batch(_ context.Context, request recordops.BatchRequest) ([]recordops.MutationResult, error) {
	s.calls++
	return make([]recordops.MutationResult, len(request.Operations)), nil
}

func TestBatchHooksKeepHTTPResultStatusesAlignedWithOperations(t *testing.T) {
	registry := &lifecycle.Registry{}
	_, err := registry.On("record.batch", lifecycle.Hook{Before: func(_ context.Context, event *lifecycle.Event) error {
		batch := event.Data.(*BatchRequestEvent)
		batch.Operations[0] = recordops.BatchOperation{Create: &recordops.CreateRequest{Collection: "posts", Input: map[string]json.RawMessage{}}}
		batch.Operations = append(batch.Operations, recordops.BatchOperation{Update: &recordops.UpdateRequest{Collection: "posts", PublicID: "second", Input: map[string]json.RawMessage{}}})
		return nil
	}})
	if err != nil {
		t.Fatal(err)
	}
	service := &hookBatchService{}
	dependencies := recordTestDependencies(&fakeAdminAuth{authenticateAdmin: validSession().Administrator}, service)
	dependencies.LifecycleHooks = registry
	request := authenticatedRecordRequest(http.MethodPost, "/api/v1/collections/batch", `{"requests":[{"method":"DELETE","url":"/api/v1/collections/posts/records/first"}]}`, true)
	response := httptest.NewRecorder()
	mustRouter(t, dependencies).ServeHTTP(response, request)
	var result struct {
		Results []batchResult `json:"results"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if response.Code != http.StatusOK || service.calls != 1 || len(result.Results) != 2 {
		t.Fatalf("status=%d calls=%d body=%s", response.Code, service.calls, response.Body.String())
	}
	if result.Results[0].Status != http.StatusCreated || result.Results[1].Status != http.StatusOK {
		t.Fatalf("results=%+v", result.Results)
	}
}

func TestBatchHooksCannotExceedRequestLimit(t *testing.T) {
	registry := &lifecycle.Registry{}
	afterCalls := 0
	_, err := registry.On("record.batch", lifecycle.Hook{
		Before: func(_ context.Context, event *lifecycle.Event) error {
			batch := event.Data.(*BatchRequestEvent)
			operation := batch.Operations[0]
			batch.Operations = make([]recordops.BatchOperation, 51)
			for index := range batch.Operations {
				batch.Operations[index] = operation
			}
			return nil
		},
		After: func(_ context.Context, event *lifecycle.Event) {
			afterCalls++
			if event.Err == nil {
				t.Error("oversized batch reported success to hook")
			}
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	service := &hookBatchService{}
	dependencies := recordTestDependencies(&fakeAdminAuth{authenticateAdmin: validSession().Administrator}, service)
	dependencies.LifecycleHooks = registry
	request := authenticatedRecordRequest(http.MethodPost, "/api/v1/collections/batch", `{"operations":[{"operation":"delete","collection":"posts","id":"first"}]}`, true)
	response := httptest.NewRecorder()
	mustRouter(t, dependencies).ServeHTTP(response, request)
	if response.Code != http.StatusBadRequest || service.calls != 0 || afterCalls != 1 {
		t.Fatalf("status=%d calls=%d after=%d body=%s", response.Code, service.calls, afterCalls, response.Body.String())
	}
}
