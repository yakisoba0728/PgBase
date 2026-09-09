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
	batchRequestIDs := []string{}
	batchIdempotencyKeys := []string{}
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
			if kind == HookRecordBatchRequest {
				batchEvent := event.Data.(*BatchRequestEvent)
				batchRequestIDs = append(batchRequestIDs, batchEvent.RequestID)
				if batchEvent.Operations[0].Create != nil && batchEvent.Operations[0].Create.IdempotencyKey == "n5-batch-create-0001" {
					batchEvent.Operations[0].Create.IdempotencyKey = "n5-batch-rewritten-0001"
				}
			}
			return nil
		}, After: func(_ context.Context, event *HookEvent) {
			if event.Err == nil {
				mu.Lock()
				success[kind]++
				if kind == HookRecordBatchRequest {
					batchEvent := event.Data.(*BatchRequestEvent)
					if batchEvent.Operations[0].Create != nil {
						batchIdempotencyKeys = append(batchIdempotencyKeys, batchEvent.Operations[0].Create.IdempotencyKey)
					}
					for _, result := range batchEvent.Results {
						batchReplay = batchReplay || result.Replayed
						if result.Record != nil {
							result.Record.Data["title"] = "after hook"
						}
					}
				} else if kind == HookRecordCreateRequest {
					result := event.Data.(*RecordRequestEvent).Result
					if result != nil && result.Record != nil {
						result.Record.Data["title"] = "after hook"
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
	batch := publicUsersRequest(t, app, http.MethodPost, "/api/v1/collections/batch", map[string]any{"operations": []map[string]any{{"operation": "create", "collection": "n5_records", "input": map[string]any{"title": "batch"}, "idempotencyKey": "n5-batch-create-0001"}}}, "", cookie, map[string]string{"X-PgBase-CSRF": csrf, "X-Request-ID": "n5_batch_request_one"})
	assertPublicUsersStatus(t, batch, http.StatusOK)
	var batchBody struct {
		Results []struct {
			Record map[string]any `json:"record"`
		} `json:"results"`
	}
	decodePublicUsersJSON(t, batch, &batchBody)
	if batchBody.Results[0].Record["title"] != "batch" {
		t.Fatalf("after hook changed batch response: %#v", batchBody)
	}
	replay := publicUsersRequest(t, app, http.MethodPost, "/api/v1/collections/batch", map[string]any{"operations": []map[string]any{{"operation": "create", "collection": "n5_records", "input": map[string]any{"title": "batch"}, "idempotencyKey": "n5-batch-create-0001"}}}, "", cookie, map[string]string{"X-PgBase-CSRF": csrf, "X-Request-ID": "n5_batch_request_two"})
	assertPublicUsersStatus(t, replay, http.StatusOK)
	var vetoAfter error
	vetoAfterCount := 0
	removeVeto, err := registry.On(HookRecordBatchRequest, Hook{Before: func(context.Context, *HookEvent) error { return errors.New("batch veto") }, After: func(_ context.Context, event *HookEvent) { vetoAfterCount++; vetoAfter = event.Err }})
	if err != nil {
		t.Fatal(err)
	}
	vetoed := publicUsersRequest(t, app, http.MethodPost, "/api/v1/collections/batch", map[string]any{"operations": []map[string]any{{"operation": "create", "collection": "n5_records", "input": map[string]any{"title": "blocked"}, "idempotencyKey": "n5-batch-veto-0001"}}}, "", cookie, map[string]string{"X-PgBase-CSRF": csrf})
	assertPublicUsersStatus(t, vetoed, http.StatusInternalServerError)
	removeVeto()
	if vetoAfter == nil || vetoAfterCount != 1 {
		t.Fatal("vetoed batch did not receive paired final error")
	}
	var panicAfter error
	panicAfterCount := 0
	removePanic, err := registry.On(HookRecordCreateRequest, Hook{Before: func(context.Context, *HookEvent) error { panic("hook panic") }, After: func(_ context.Context, event *HookEvent) { panicAfterCount++; panicAfter = event.Err }})
	if err != nil {
		t.Fatal(err)
	}
	panicked := publicUsersRequest(t, app, http.MethodPost, base, map[string]any{"title": "panic"}, "", cookie, map[string]string{"X-PgBase-CSRF": csrf, "Idempotency-Key": "n5-record-panic-0001"})
	assertPublicUsersStatus(t, panicked, http.StatusInternalServerError)
	removePanic()
	if panicAfter == nil || panicAfterCount != 1 {
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
	if len(batchRequestIDs) < 2 || batchRequestIDs[0] != "n5_batch_request_one" || batchRequestIDs[1] != "n5_batch_request_two" {
		t.Errorf("batch hook request identities=%v", batchRequestIDs)
	}
	if len(batchIdempotencyKeys) < 2 || batchIdempotencyKeys[0] != "n5-batch-create-0001" || batchIdempotencyKeys[1] != "n5-batch-create-0001" {
		t.Errorf("batch hook idempotency identities=%v", batchIdempotencyKeys)
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
	applyRequestIDs := []string{}
	_, err = registry.On(HookSchemaImportApply, Hook{Before: func(_ context.Context, event *HookEvent) error {
		apply := event.Data.(*SchemaImportRequestEvent)
		applyRequestIDs = append(applyRequestIDs, apply.RequestID)
		if len(apply.Document.Collections) > 0 {
			apply.Document.Collections[0].Name = "hook_mutated_schema"
		}
		return nil
	}, After: func(_ context.Context, event *HookEvent) {
		if event.Err == nil && event.Data.(*SchemaImportRequestEvent).Result != nil {
			committed++
			event.Data.(*SchemaImportRequestEvent).Result.Status = "after hook"
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
	headers := map[string]string{"X-PgBase-CSRF": csrf, "PgBase-Operation-ID": uuid.NewString(), "Idempotency-Key": "n5-schema-import-0001", "X-Request-ID": "n5_schema_request_one"}
	vetoed := publicUsersRequest(t, app, http.MethodPost, "/api/admin/schema/import/apply", body, "", cookie, headers)
	assertPublicUsersStatus(t, vetoed, http.StatusInternalServerError)
	if vetoAfter == nil {
		t.Fatal("schema veto did not receive paired final error")
	}
	removeVeto()
	headers["X-Request-ID"] = "n5_schema_request_two"
	committedResponse := publicUsersRequest(t, app, http.MethodPost, "/api/admin/schema/import/apply", body, "", cookie, headers)
	assertPublicUsersStatus(t, committedResponse, http.StatusOK)
	var appliedResult struct {
		Status string `json:"status"`
	}
	decodePublicUsersJSON(t, committedResponse, &appliedResult)
	if appliedResult.Status == "after hook" {
		t.Fatal("after hook changed schema response")
	}
	headers["X-Request-ID"] = "n5_schema_request_three"
	replayedResponse := publicUsersRequest(t, app, http.MethodPost, "/api/admin/schema/import/apply", body, "", cookie, headers)
	assertPublicUsersStatus(t, replayedResponse, http.StatusOK)
	if previewed != 1 || committed != 2 || len(applyRequestIDs) != 3 || applyRequestIDs[1] != "n5_schema_request_two" || applyRequestIDs[2] != "n5_schema_request_three" {
		t.Fatalf("schema hook results preview=%d committed=%d", previewed, committed)
	}
}

func TestLifecycleMultipartBatchRejectsAttachmentRetargetingThroughHTTP(t *testing.T) {
	registry := &HookRegistry{}
	mode := "rewrite"
	_, err := registry.On(HookRecordBatchRequest, Hook{Before: func(_ context.Context, event *HookEvent) error {
		operations := event.Data.(*BatchRequestEvent).Operations
		switch mode {
		case "rewrite":
			operations[0].Create.Input["title"] = json.RawMessage(`"rewritten"`)
		case "reorder":
			operations[0], operations[1] = operations[1], operations[0]
		case "retarget":
			operations[0].Create.Collection = "n5_hook_other"
		}
		return nil
	}})
	if err != nil {
		t.Fatal(err)
	}
	app := newPublicUsersAppOptions(t, true, func(options *Options) {
		options.Hooks = registry
		options.Files = FileOptions{Driver: "local", LocalDirectory: t.TempDir(), MaximumBytes: 1024, MaximumImagePixels: 1_000_000, ThumbnailDimension: 64, AllowedTypes: []string{"text/plain"}}
	})
	cookie, csrf := setupPublicUsersAdministrator(t, app)
	create := func(name string) {
		change := map[string]any{"operation": "create", "name": name, "fields": []map[string]any{{"name": "title", "type": "text"}, {"name": "attachment", "type": "file"}}}
		plan := publicUsersRequest(t, app, http.MethodPost, "/api/admin/collections/plan", change, "", cookie, map[string]string{"X-PgBase-CSRF": csrf})
		assertPublicUsersStatus(t, plan, http.StatusOK)
		var planned struct {
			BaseVersion int64  `json:"baseVersion"`
			Checksum    string `json:"checksum"`
		}
		decodePublicUsersJSON(t, plan, &planned)
		change["baseVersion"], change["checksum"] = planned.BaseVersion, planned.Checksum
		assertPublicUsersStatus(t, publicUsersRequest(t, app, http.MethodPost, "/api/admin/collections/apply", change, "", cookie, map[string]string{"X-PgBase-CSRF": csrf}), http.StatusCreated)
	}
	create("n5_hook_files")
	create("n5_hook_other")
	requests := map[string]any{"requests": []map[string]any{
		{"method": "POST", "url": "/api/v1/collections/n5_hook_files/records", "headers": map[string]string{"Idempotency-Key": "n5-hook-file-0001"}, "body": map[string]any{"title": "client", "attachment": map[string]any{"$file": "upload"}}},
		{"method": "POST", "url": "/api/v1/collections/n5_hook_other/records", "headers": map[string]string{"Idempotency-Key": "n5-hook-other-0001"}, "body": map[string]any{"title": "other"}},
	}}
	accepted := multipartBatchRequest(t, app, cookie, csrf, requests, map[string][]byte{"upload": []byte("attachment")})
	assertPublicUsersStatus(t, accepted, http.StatusOK)
	var output struct {
		Results []struct {
			Body map[string]any `json:"body"`
		} `json:"results"`
	}
	decodePublicUsersJSON(t, accepted, &output)
	if output.Results[0].Body["title"] != "rewritten" {
		t.Fatalf("mutable multipart field rewrite=%#v", output)
	}
	mode = "reorder"
	reordered := multipartBatchRequest(t, app, cookie, csrf, requests, map[string][]byte{"upload": []byte("reordered")})
	assertPublicUsersStatus(t, reordered, http.StatusBadRequest)
	mode = "retarget"
	retargeted := multipartBatchRequest(t, app, cookie, csrf, requests, map[string][]byte{"upload": []byte("retargeted")})
	assertPublicUsersStatus(t, retargeted, http.StatusBadRequest)
	page := publicUsersRequest(t, app, http.MethodGet, "/api/v1/collections/n5_hook_files/records", nil, "", cookie, nil)
	assertPublicUsersStatus(t, page, http.StatusOK)
	var listing struct {
		Items []map[string]any `json:"items"`
	}
	decodePublicUsersJSON(t, page, &listing)
	if len(listing.Items) != 1 {
		t.Fatalf("unsafe multipart hooks wrote records: %#v", listing.Items)
	}
}

func TestLifecycleMutableBatchStillUsesPublicUserAuthorization(t *testing.T) {
	registry := &HookRegistry{}
	_, err := registry.On(HookRecordBatchRequest, Hook{Before: func(_ context.Context, event *HookEvent) error {
		event.Data.(*BatchRequestEvent).Operations[0].Create.Collection = "n5_hook_denied"
		return nil
	}})
	if err != nil {
		t.Fatal(err)
	}
	app := newPublicUsersAppOptions(t, true, func(options *Options) { options.Hooks = registry })
	cookie, csrf := setupPublicUsersAdministrator(t, app)
	createPublicUsersCollection(t, app, cookie, csrf, "n5_hook_allowed")
	createPublicUsersCollection(t, app, cookie, csrf, "n5_hook_denied")
	enable := publicUsersRequest(t, app, http.MethodPut, "/api/admin/collections/n5_hook_allowed/user-access", map[string]any{"enabled": true}, "", cookie, map[string]string{"X-PgBase-CSRF": csrf})
	assertPublicUsersStatus(t, enable, http.StatusOK)
	user := registerPublicUser(t, app, "n5-hook-user@example.test", publicUserOnePass)
	batch := publicUsersRequest(t, app, http.MethodPost, "/api/v1/user/collections/batch", map[string]any{"operations": []map[string]any{{"operation": "create", "collection": "n5_hook_allowed", "input": map[string]any{"title": "must not write"}}}}, user.Token, nil, nil)
	assertPublicUsersStatus(t, batch, http.StatusForbidden)
	page := publicUsersRequest(t, app, http.MethodGet, "/api/v1/user/collections/n5_hook_allowed/records", nil, user.Token, nil, nil)
	assertPublicUsersStatus(t, page, http.StatusOK)
	var listing struct {
		Items []map[string]any `json:"items"`
	}
	decodePublicUsersJSON(t, page, &listing)
	if len(listing.Items) != 0 {
		t.Fatalf("denied hook rewrite committed records: %#v", listing.Items)
	}
}

func TestLifecycleJSONBatchRejectsReorderedIdempotentCreatesThroughHTTP(t *testing.T) {
	registry := &HookRegistry{}
	reorder := false
	_, err := registry.On(HookRecordBatchRequest, Hook{Before: func(_ context.Context, event *HookEvent) error {
		if reorder {
			operations := event.Data.(*BatchRequestEvent).Operations
			operations[0], operations[1] = operations[1], operations[0]
		}
		return nil
	}})
	if err != nil {
		t.Fatal(err)
	}
	app := newPublicUsersAppOptions(t, true, func(options *Options) { options.Hooks = registry })
	cookie, csrf := setupPublicUsersAdministrator(t, app)
	createPublicUsersCollection(t, app, cookie, csrf, "n5_identity_first")
	createPublicUsersCollection(t, app, cookie, csrf, "n5_identity_second")
	body := map[string]any{"operations": []map[string]any{
		{"operation": "create", "collection": "n5_identity_first", "input": map[string]any{"title": "first"}, "idempotencyKey": "n5-identity-first-0001"},
		{"operation": "create", "collection": "n5_identity_second", "input": map[string]any{"title": "second"}, "idempotencyKey": "n5-identity-second-0001"},
	}}
	initial := publicUsersRequest(t, app, http.MethodPost, "/api/v1/collections/batch", body, "", cookie, map[string]string{"X-PgBase-CSRF": csrf})
	assertPublicUsersStatus(t, initial, http.StatusOK)
	reorder = true
	replay := publicUsersRequest(t, app, http.MethodPost, "/api/v1/collections/batch", body, "", cookie, map[string]string{"X-PgBase-CSRF": csrf})
	assertPublicUsersStatus(t, replay, http.StatusBadRequest)
	for _, collection := range []string{"n5_identity_first", "n5_identity_second"} {
		page := publicUsersRequest(t, app, http.MethodGet, "/api/v1/collections/"+collection+"/records", nil, "", cookie, nil)
		assertPublicUsersStatus(t, page, http.StatusOK)
		var listing struct {
			Items []map[string]any `json:"items"`
		}
		decodePublicUsersJSON(t, page, &listing)
		if len(listing.Items) != 1 {
			t.Fatalf("%s records after rejected replay=%#v", collection, listing.Items)
		}
	}
}

func TestLifecycleJSONBatchRejectsSameCollectionIdempotentReorderThroughHTTP(t *testing.T) {
	registry := &HookRegistry{}
	reorder := false
	_, err := registry.On(HookRecordBatchRequest, Hook{Before: func(_ context.Context, event *HookEvent) error {
		if reorder {
			operations := event.Data.(*BatchRequestEvent).Operations
			operations[0], operations[1] = operations[1], operations[0]
		}
		return nil
	}})
	if err != nil {
		t.Fatal(err)
	}
	app := newPublicUsersAppOptions(t, true, func(options *Options) { options.Hooks = registry })
	cookie, csrf := setupPublicUsersAdministrator(t, app)
	createPublicUsersCollection(t, app, cookie, csrf, "n5_identity_same")
	body := map[string]any{"operations": []map[string]any{
		{"operation": "create", "collection": "n5_identity_same", "input": map[string]any{"title": "first"}, "idempotencyKey": "n5-identity-same-first-0001"},
		{"operation": "create", "collection": "n5_identity_same", "input": map[string]any{"title": "second"}, "idempotencyKey": "n5-identity-same-second-0001"},
	}}
	initial := publicUsersRequest(t, app, http.MethodPost, "/api/v1/collections/batch", body, "", cookie, map[string]string{"X-PgBase-CSRF": csrf})
	assertPublicUsersStatus(t, initial, http.StatusOK)
	reorder = true
	replay := publicUsersRequest(t, app, http.MethodPost, "/api/v1/collections/batch", body, "", cookie, map[string]string{"X-PgBase-CSRF": csrf})
	assertPublicUsersStatus(t, replay, http.StatusBadRequest)
	page := publicUsersRequest(t, app, http.MethodGet, "/api/v1/collections/n5_identity_same/records", nil, "", cookie, nil)
	assertPublicUsersStatus(t, page, http.StatusOK)
	var listing struct {
		Items []map[string]any `json:"items"`
	}
	decodePublicUsersJSON(t, page, &listing)
	if len(listing.Items) != 2 {
		t.Fatalf("same-collection records after rejected replay=%#v", listing.Items)
	}
}
