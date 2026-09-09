//go:build integration

package pgbase

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"
)

func TestLifecycleRecordAndFileHooksThroughHTTP(t *testing.T) {
	registry := &HookRegistry{}
	var mu sync.Mutex
	events := map[string]int{}
	for _, kind := range []string{HookRequest, HookRecordBefore, HookRecordAfter, HookFileUpload, HookFileDelete} {
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
	change := map[string]any{"operation": "create", "name": "hook_documents", "fields": []map[string]any{{"name": "title", "type": "text"}, {"name": "attachment", "type": "file"}}}
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
	assertPublicUsersStatus(t, publicUsersRequest(t, fixture, "DELETE", "/api/admin/files/"+file.ID, nil, "", cookie, map[string]string{"X-PgBase-CSRF": csrf}), 204)
	mu.Lock()
	defer mu.Unlock()
	for _, kind := range []string{HookRequest, HookRecordBefore, HookRecordAfter, HookFileUpload, HookFileDelete} {
		if events[kind] < 1 {
			t.Errorf("hook %s not observed: %v", kind, events)
		}
	}
}
