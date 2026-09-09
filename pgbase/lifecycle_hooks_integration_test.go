//go:build integration

package pgbase

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/webisnoteasy/PgBase/internal/filestore"
)

func TestLifecycleRecordAndFileHooksThroughHTTP(t *testing.T) {
	registry := &HookRegistry{}
	var mu sync.Mutex
	events := map[string]int{}
	for _, kind := range []string{HookAppBoot, HookRequest, HookCollectionCreate, HookRecordBefore, HookRecordAfter, HookFileUpload, HookFileDelete, HookFileToken, HookFileDownload, HookStorageSettingsList} {
		_, err := registry.On(kind, Hook{After: func(_ context.Context, e *HookEvent) {
			if e.Err == nil {
				mu.Lock()
				events[e.Kind]++
				mu.Unlock()
			}
		}})
		if err != nil {
			t.Fatal(err)
		}
	}
	fixture := newPublicUsersAppOptions(t, true, func(o *Options) {
		o.Hooks = registry
		o.Files = FileOptions{Driver: "local", LocalDirectory: t.TempDir(), MaximumBytes: 1024, MaximumImagePixels: 1_000_000, ThumbnailDimension: 64, DownloadTokenTTL: time.Minute, AllowedTypes: []string{"text/plain"}}
	})
	cookie, csrf := setupPublicUsersAdministrator(t, fixture)
	settingsResponse := publicUsersRequest(t, fixture, "GET", "/api/admin/storage/files", nil, "", cookie, nil)
	assertPublicUsersStatus(t, settingsResponse, 200)
	var settings filestore.StorageSettings
	decodePublicUsersJSON(t, settingsResponse, &settings)
	removeStorage, err := registry.On(HookStorageSettingsUpdate, Hook{Before: func(context.Context, *HookEvent) error { return errors.New("storage veto") }})
	if err != nil {
		t.Fatal(err)
	}
	transformCalls := 0
	_, err = registry.On(HookFileTransform, Hook{Before: func(context.Context, *HookEvent) error {
		transformCalls++
		return nil
	}})
	if err != nil {
		t.Fatal(err)
	}
	storageUpdate := publicUsersRequest(t, fixture, "PUT", "/api/admin/storage/files", map[string]any{"driver": "local", "revision": settings.Revision}, "", cookie, map[string]string{"X-PgBase-CSRF": csrf})
	if storageUpdate.Code < 400 {
		t.Fatal("vetoed storage update succeeded")
	}
	removeStorage()
	change := map[string]any{"operation": "create", "name": "hook_documents", "fields": []map[string]any{{"name": "title", "type": "text"}, {"name": "attachment", "type": "file", "options": map[string]any{"thumbs": []string{"10x10"}}}}}
	plan := publicUsersRequest(t, fixture, "POST", "/api/admin/collections/plan", change, "", cookie, map[string]string{"X-PgBase-CSRF": csrf})
	assertPublicUsersStatus(t, plan, 200)
	var planned struct {
		BaseVersion int64  `json:"baseVersion"`
		Checksum    string `json:"checksum"`
	}
	decodePublicUsersJSON(t, plan, &planned)
	change["baseVersion"] = planned.BaseVersion
	change["checksum"] = planned.Checksum
	assertPublicUsersStatus(t, publicUsersRequest(t, fixture, "POST", "/api/admin/collections/apply", change, "", cookie, map[string]string{"X-PgBase-CSRF": csrf}), 201)
	created := publicUsersRequest(t, fixture, "POST", "/api/v1/collections/hook_documents/records", map[string]any{"title": "observed"}, "", cookie, map[string]string{"X-PgBase-CSRF": csrf})
	assertPublicUsersStatus(t, created, 201)
	var record struct {
		ID string `json:"id"`
	}
	decodePublicUsersJSON(t, created, &record)
	veto, err := registry.On(HookFileUpload, Hook{Before: func(_ context.Context, e *HookEvent) error {
		if e.Data.(*FileEvent).File.Filename == "blocked.txt" {
			return errors.New("blocked upload")
		}
		return nil
	}})
	if err != nil {
		t.Fatal(err)
	}
	base := "/api/admin/collections/hook_documents/records/" + record.ID + "/files/attachment"
	rejected := multipartFileRequest(t, fixture, base, "", cookie, "blocked.txt", "text/plain", []byte("blocked"), map[string]string{"X-PgBase-CSRF": csrf})
	if rejected.Code < 400 {
		t.Fatal("vetoed file committed")
	}
	veto()
	uploaded := multipartFileRequest(t, fixture, base, "", cookie, "allowed.txt", "text/plain", []byte("allowed"), map[string]string{"X-PgBase-CSRF": csrf})
	assertPublicUsersStatus(t, uploaded, 201)
	var file struct {
		ID string `json:"id"`
	}
	decodePublicUsersJSON(t, uploaded, &file)
	token := publicUsersRequest(t, fixture, http.MethodPost, "/api/admin/files/"+file.ID+"/token", map[string]any{"variant": "original"}, "", cookie, map[string]string{"X-PgBase-CSRF": csrf})
	assertPublicUsersStatus(t, token, http.StatusOK)
	var tokenBody struct{ Token string }
	decodePublicUsersJSON(t, token, &tokenBody)
	download := publicUsersRequest(t, fixture, http.MethodGet, "/api/admin/files/"+file.ID+"?variant=original&token="+tokenBody.Token, nil, "", cookie, nil)
	assertPublicUsersStatus(t, download, http.StatusOK)
	transformToken := publicUsersRequest(t, fixture, http.MethodPost, "/api/admin/files/"+file.ID+"/token", map[string]any{"variant": "10x10"}, "", cookie, map[string]string{"X-PgBase-CSRF": csrf})
	assertPublicUsersStatus(t, transformToken, http.StatusOK)
	decodePublicUsersJSON(t, transformToken, &tokenBody)
	transform := publicUsersRequest(t, fixture, http.MethodGet, "/api/admin/files/"+file.ID+"?variant=10x10&token="+tokenBody.Token, nil, "", cookie, nil)
	assertPublicUsersStatus(t, transform, http.StatusBadRequest)
	assertPublicUsersStatus(t, publicUsersRequest(t, fixture, "DELETE", "/api/admin/files/"+file.ID, nil, "", cookie, map[string]string{"X-PgBase-CSRF": csrf}), 204)
	mu.Lock()
	defer mu.Unlock()
	for _, kind := range []string{HookAppBoot, HookRequest, HookCollectionCreate, HookRecordBefore, HookRecordAfter, HookFileUpload, HookFileDelete, HookFileToken, HookFileDownload, HookStorageSettingsList} {
		if events[kind] < 1 {
			t.Errorf("hook %s not observed: %v", kind, events)
		}
	}
	if transformCalls != 1 {
		t.Errorf("file.transform calls=%d, want 1", transformCalls)
	}
}

func TestLifecycleRecordRequestAndBatchHooksThroughHTTP(t *testing.T) {
	registry := &HookRegistry{}
	var mu sync.Mutex
	success := map[string]int{}
	batchReplay := false
	for _, kind := range []string{HookRecordListRequest, HookRecordViewRequest, HookRecordCreateRequest, HookRecordUpdateRequest, HookRecordDeleteRequest, HookRecordBatchRequest} {
		kind := kind
		_, err := registry.On(kind, Hook{Before: func(_ context.Context, event *HookEvent) error {
			if kind == HookRecordCreateRequest {
				input := event.Data.(*RecordRequestEvent).Input
				if string(input["title"]) == `"make invalid"` {
					input["title"] = json.RawMessage(`123`)
				} else {
					input["title"] = json.RawMessage(`"rewritten by hook"`)
				}
			}
			return nil
		}, After: func(_ context.Context, event *HookEvent) {
			if event.Err == nil {
				mu.Lock()
				success[kind]++
				if kind == HookRecordBatchRequest {
					for _, result := range event.Data.(*BatchRequestEvent).Results {
						batchReplay = batchReplay || result.Replayed
					}
				}
				mu.Unlock()
			}
		}})
		if err != nil {
			t.Fatal(err)
		}
	}
	app := newPublicUsersAppOptions(t, true, func(options *Options) { options.Hooks = registry })
	cookie, csrf := setupPublicUsersAdministrator(t, app)
	change := map[string]any{"operation": "create", "name": "n5_records", "fields": []map[string]any{{"name": "title", "type": "text"}}}
	plan := publicUsersRequest(t, app, http.MethodPost, "/api/admin/collections/plan", change, "", cookie, map[string]string{"X-PgBase-CSRF": csrf})
	assertPublicUsersStatus(t, plan, http.StatusOK)
	var planned struct {
		BaseVersion int64  `json:"baseVersion"`
		Checksum    string `json:"checksum"`
	}
	decodePublicUsersJSON(t, plan, &planned)
	change["baseVersion"], change["checksum"] = planned.BaseVersion, planned.Checksum
	assertPublicUsersStatus(t, publicUsersRequest(t, app, http.MethodPost, "/api/admin/collections/apply", change, "", cookie, map[string]string{"X-PgBase-CSRF": csrf}), http.StatusCreated)
	base := "/api/v1/collections/n5_records/records"
	created := publicUsersRequest(t, app, http.MethodPost, base, map[string]any{"title": "client title"}, "", cookie, map[string]string{"X-PgBase-CSRF": csrf, "Idempotency-Key": "n5-record-create-0001"})
	assertPublicUsersStatus(t, created, http.StatusCreated)
	var record struct{ ID, Title string }
	decodePublicUsersJSON(t, created, &record)
	if record.Title != "rewritten by hook" {
		t.Fatalf("create hook did not alter service input: %#v", record)
	}
	path, etag := base+"/"+record.ID, created.Header().Get("ETag")
	assertPublicUsersStatus(t, publicUsersRequest(t, app, http.MethodGet, base, nil, "", cookie, nil), http.StatusOK)
	assertPublicUsersStatus(t, publicUsersRequest(t, app, http.MethodGet, path, nil, "", cookie, nil), http.StatusOK)
	updated := publicUsersRequest(t, app, http.MethodPatch, path, map[string]any{"title": "updated"}, "", cookie, map[string]string{"X-PgBase-CSRF": csrf, "If-Match": etag})
	assertPublicUsersStatus(t, updated, http.StatusOK)
	assertPublicUsersStatus(t, publicUsersRequest(t, app, http.MethodDelete, path, nil, "", cookie, map[string]string{"X-PgBase-CSRF": csrf, "If-Match": updated.Header().Get("ETag")}), http.StatusNoContent)
	invalid := publicUsersRequest(t, app, http.MethodPost, base, map[string]any{"title": "make invalid"}, "", cookie, map[string]string{"X-PgBase-CSRF": csrf, "Idempotency-Key": "n5-record-invalid-0001"})
	assertPublicUsersStatus(t, invalid, http.StatusUnprocessableEntity)
	batch := publicUsersRequest(t, app, http.MethodPost, "/api/v1/collections/batch", map[string]any{"operations": []map[string]any{{"operation": "create", "collection": "n5_records", "input": map[string]any{"title": "batch"}, "idempotencyKey": "n5-batch-create-0001"}}}, "", cookie, map[string]string{"X-PgBase-CSRF": csrf})
	assertPublicUsersStatus(t, batch, http.StatusOK)
	replay := publicUsersRequest(t, app, http.MethodPost, "/api/v1/collections/batch", map[string]any{"operations": []map[string]any{{"operation": "create", "collection": "n5_records", "input": map[string]any{"title": "batch"}, "idempotencyKey": "n5-batch-create-0001"}}}, "", cookie, map[string]string{"X-PgBase-CSRF": csrf})
	assertPublicUsersStatus(t, replay, http.StatusOK)
	var vetoAfter error
	removeVeto, err := registry.On(HookRecordBatchRequest, Hook{Before: func(context.Context, *HookEvent) error { return errors.New("batch veto") }, After: func(_ context.Context, event *HookEvent) { vetoAfter = event.Err }})
	if err != nil {
		t.Fatal(err)
	}
	vetoed := publicUsersRequest(t, app, http.MethodPost, "/api/v1/collections/batch", map[string]any{"operations": []map[string]any{{"operation": "create", "collection": "n5_records", "input": map[string]any{"title": "blocked"}, "idempotencyKey": "n5-batch-veto-0001"}}}, "", cookie, map[string]string{"X-PgBase-CSRF": csrf})
	assertPublicUsersStatus(t, vetoed, http.StatusInternalServerError)
	removeVeto()
	if vetoAfter == nil {
		t.Fatal("vetoed batch did not receive paired final error")
	}
	var panicAfter error
	removePanic, err := registry.On(HookRecordCreateRequest, Hook{Before: func(context.Context, *HookEvent) error { panic("hook panic") }, After: func(_ context.Context, event *HookEvent) { panicAfter = event.Err }})
	if err != nil {
		t.Fatal(err)
	}
	panicked := publicUsersRequest(t, app, http.MethodPost, base, map[string]any{"title": "panic"}, "", cookie, map[string]string{"X-PgBase-CSRF": csrf, "Idempotency-Key": "n5-record-panic-0001"})
	assertPublicUsersStatus(t, panicked, http.StatusInternalServerError)
	removePanic()
	if panicAfter == nil {
		t.Fatal("panicked hook did not receive paired final error")
	}
	mu.Lock()
	defer mu.Unlock()
	for _, kind := range []string{HookRecordListRequest, HookRecordViewRequest, HookRecordCreateRequest, HookRecordUpdateRequest, HookRecordDeleteRequest, HookRecordBatchRequest} {
		if success[kind] == 0 {
			t.Errorf("successful %s hook count=%d", kind, success[kind])
		}
	}
	if !batchReplay {
		t.Error("batch replay did not expose its final replay outcome")
	}
}

func TestLifecycleSchemaImportHooksThroughHTTP(t *testing.T) {
	registry := &HookRegistry{}
	var previewed, committed int
	var vetoAfter error
	_, err := registry.On(HookSchemaImportPreview, Hook{After: func(_ context.Context, event *HookEvent) {
		if event.Err == nil {
			previewed++
		}
	}})
	if err != nil {
		t.Fatal(err)
	}
	_, err = registry.On(HookSchemaImportApply, Hook{After: func(_ context.Context, event *HookEvent) {
		if event.Err == nil && event.Data.(*SchemaImportRequestEvent).Result != nil {
			committed++
		}
	}})
	if err != nil {
		t.Fatal(err)
	}
	removeVeto, err := registry.On(HookSchemaImportApply, Hook{Before: func(context.Context, *HookEvent) error { return errors.New("schema veto") }, After: func(_ context.Context, event *HookEvent) { vetoAfter = event.Err }})
	if err != nil {
		t.Fatal(err)
	}
	app := newPublicUsersAppOptions(t, true, func(options *Options) { options.Hooks = registry })
	cookie, csrf := setupPublicUsersAdministrator(t, app)
	createPublicUsersCollection(t, app, cookie, csrf, "n5_schema_source")
	exported := publicUsersRequest(t, app, http.MethodGet, "/api/admin/schema/export", nil, "", cookie, nil)
	assertPublicUsersStatus(t, exported, http.StatusOK)
	var document json.RawMessage
	decodePublicUsersJSON(t, exported, &document)
	preview := publicUsersRequest(t, app, http.MethodPost, "/api/admin/schema/import/preview", document, "", cookie, map[string]string{"X-PgBase-CSRF": csrf})
	assertPublicUsersStatus(t, preview, http.StatusOK)
	var plan struct {
		Document    json.RawMessage `json:"document"`
		BaseVersion int64           `json:"baseVersion"`
		Checksum    string          `json:"checksum"`
	}
	decodePublicUsersJSON(t, preview, &plan)
	body := map[string]any{"document": plan.Document, "baseVersion": plan.BaseVersion, "checksum": plan.Checksum}
	headers := map[string]string{"X-PgBase-CSRF": csrf, "PgBase-Operation-ID": uuid.NewString(), "Idempotency-Key": "n5-schema-import-0001"}
	vetoed := publicUsersRequest(t, app, http.MethodPost, "/api/admin/schema/import/apply", body, "", cookie, headers)
	assertPublicUsersStatus(t, vetoed, http.StatusInternalServerError)
	if vetoAfter == nil {
		t.Fatal("schema veto did not receive paired final error")
	}
	removeVeto()
	committedResponse := publicUsersRequest(t, app, http.MethodPost, "/api/admin/schema/import/apply", body, "", cookie, headers)
	assertPublicUsersStatus(t, committedResponse, http.StatusOK)
	if previewed != 1 || committed != 1 {
		t.Fatalf("schema hook results preview=%d committed=%d", previewed, committed)
	}
}
