//go:build integration

package pgbase

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/yakisoba0728/PgBase/internal/recordops"
)

func TestBatchHookRewritesHTTPResultsAfterCommittedMutations(t *testing.T) {
	registry := &HookRegistry{}
	fixture := newPublicUsersAppOptions(t, true, func(options *Options) { options.Hooks = registry })
	cookie, csrf := setupPublicUsersAdministrator(t, fixture)
	createPublicUsersCollection(t, fixture, cookie, csrf, "batch_hook_results")
	seed := publicUsersRequest(t, fixture, http.MethodPost, "/api/v1/collections/batch_hook_results/records", map[string]any{"title": "seed"}, "", cookie, map[string]string{"X-PgBase-CSRF": csrf})
	assertPublicUsersStatus(t, seed, http.StatusCreated)
	var record struct{ ID string }
	decodePublicUsersJSON(t, seed, &record)
	_, err := registry.On(HookRecordBatchRequest, Hook{Before: func(_ context.Context, event *HookEvent) error {
		batch := event.Data.(*BatchRequestEvent)
		batch.Operations = []recordops.BatchOperation{
			{Update: &recordops.UpdateRequest{Collection: "batch_hook_results", PublicID: record.ID, Input: map[string]json.RawMessage{"title": json.RawMessage(`"updated"`)}}},
			{Create: &recordops.CreateRequest{Collection: "batch_hook_results", Input: map[string]json.RawMessage{"title": json.RawMessage(`"added"`)}}},
		}
		return nil
	}})
	if err != nil {
		t.Fatal(err)
	}
	response := publicUsersRequest(t, fixture, http.MethodPost, "/api/v1/collections/batch", map[string]any{"requests": []map[string]any{{"method": "POST", "url": "/api/v1/collections/batch_hook_results/records", "body": map[string]any{"title": "original"}}}}, "", cookie, map[string]string{"X-PgBase-CSRF": csrf})
	assertPublicUsersStatus(t, response, http.StatusOK)
	var output struct {
		Results []struct {
			Status int            `json:"status"`
			Body   map[string]any `json:"body"`
		} `json:"results"`
	}
	decodePublicUsersJSON(t, response, &output)
	if len(output.Results) != 2 || output.Results[0].Status != http.StatusOK || output.Results[1].Status != http.StatusCreated || output.Results[0].Body["title"] != "updated" || output.Results[1].Body["title"] != "added" {
		t.Fatalf("results=%+v", output.Results)
	}
	var count int
	if err := fixture.app.pool.QueryRow(t.Context(), `select count(*) from app.batch_hook_results`).Scan(&count); err != nil || count != 2 {
		t.Fatalf("committed records=%d err=%v", count, err)
	}
}
