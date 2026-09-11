package pgbase

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/yakisoba0728/PgBase/internal/api"
	"github.com/yakisoba0728/PgBase/internal/backup"
	"github.com/yakisoba0728/PgBase/internal/filestore"
	"github.com/yakisoba0728/PgBase/internal/identity"
	"github.com/yakisoba0728/PgBase/internal/mailer"
	"github.com/yakisoba0728/PgBase/internal/recordops"
	"github.com/yakisoba0728/PgBase/internal/schema"
)

func TestJavaScriptHookUsesPriorityAndTypedMailPatch(t *testing.T) {
	directory := t.TempDir()
	writeJavaScriptFixture(t, directory, `{
 "format":"pgbase.scripts/v1",
 "hooks":[{"event":"mail.send","priority":10,"before":{"module":"route.js","export":"before"},"after":{"module":"route.js","export":"after"}}]
}`, `
exports.before = function (event) {
  if (event.phase !== "before" || event.data.subject !== "from-go") throw new Error("priority");
  return {patch:{to:event.data.to,subject:"from-js",text:event.data.text},state:{value:"seventeen"}};
};
exports.after = function (event) {
  if (event.phase !== "after" || event.state.value !== "seventeen" || event.failed) throw new Error("state");
  return {};
};`)
	options, err := (JavaScriptOptions{Directory: directory}).normalized()
	if err != nil {
		t.Fatal(err)
	}
	runtime, err := prepareJavaScript(options)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = runtime.Close(context.Background()) })
	registry := &HookRegistry{}
	_, err = registry.On(HookMailSend, Hook{Priority: 10, Before: func(_ context.Context, event *HookEvent) error {
		event.Data.(*mailer.Message).Subject = "from-go"
		return nil
	}})
	if err != nil {
		t.Fatal(err)
	}
	if err := runtime.installHooks(registry); err != nil {
		t.Fatal(err)
	}
	message := &mailer.Message{Purpose: "verification", To: "person@example.test", Subject: "original", Text: "body"}
	if err := registry.Run(context.Background(), &HookEvent{Kind: HookMailSend, Data: message}, func() error { return nil }); err != nil {
		t.Fatal(err)
	}
	if message.Subject != "from-js" || message.Purpose != "verification" {
		t.Fatalf("message = %#v", message)
	}
}

func TestJavaScriptHookRejectsPatchForReadOnlyAuthEvent(t *testing.T) {
	directory := t.TempDir()
	writeJavaScriptFixture(t, directory, `{
 "format":"pgbase.scripts/v1",
 "hooks":[{"event":"auth.password","before":{"module":"route.js","export":"before"}}]
}`, `exports.before = function () { return {patch:{userId:"00000000-0000-0000-0000-000000000001"}}; };`)
	options, err := (JavaScriptOptions{Directory: directory}).normalized()
	if err != nil {
		t.Fatal(err)
	}
	runtime, err := prepareJavaScript(options)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = runtime.Close(context.Background()) })
	registry := &HookRegistry{}
	if err := runtime.installHooks(registry); err != nil {
		t.Fatal(err)
	}
	event := &HookEvent{Kind: HookAuthPassword, Data: &AuthEvent{Flow: "login", UserID: uuid.New()}}
	finish, err := registry.Begin(context.Background(), event)
	finish(err)
	if err == nil || !strings.Contains(err.Error(), "read-only") {
		t.Fatalf("read-only patch error = %v", err)
	}
}

func TestJavaScriptHookSafeDTOsCoverLifecycleEventsAndRedactStorageKeys(t *testing.T) {
	id := uuid.New()
	events := []*HookEvent{
		{Kind: HookRecordCreateRequest, Data: &api.RecordRequestEvent{Actor: identity.Subject{Kind: identity.User, ID: id}, RequestID: "request-1", Collection: "notes"}},
		{Kind: HookRecordBatchRequest, Data: &api.BatchRequestEvent{}},
		{Kind: HookSchemaImportPreview, Data: &api.SchemaImportRequestEvent{Mode: "preview", Document: schema.SchemaDocument{Format: schema.SchemaDocumentFormat}}},
		{Kind: HookCollectionCreate, Data: &schema.CollectionMutationEvent{Operation: "create", Name: "notes", ActorID: id}},
		{Kind: HookRecordBefore, Data: recordops.Mutation{EventID: id, Collection: "notes", RecordID: id, Operation: "create", ActorID: id}},
		{Kind: HookFileUpload, Data: &filestore.FileEvent{File: filestore.File{ID: id, CollectionID: id, RecordID: id, ObjectKey: "private/object", ThumbnailKey: "private/thumb"}, Principal: identity.Subject{Kind: identity.User, ID: id}}},
		{Kind: HookFileDownload, Data: &filestore.FileAccessEvent{FileID: id, CollectionID: id, RecordID: id, Principal: identity.Subject{Kind: identity.User, ID: id}}},
		{Kind: HookAppBoot, Data: &ApplicationEvent{Mode: "server", Address: "127.0.0.1:0"}},
		{Kind: HookAuthPassword, Data: &AuthEvent{Flow: "login", UserID: id}},
		{Kind: HookStorageSettingsList, Data: &StorageSettingsEvent{Driver: "local", Result: StorageSettingsResult{SecretConfigured: true}}},
		{Kind: HookBackupCreate, Data: &backup.LifecycleEvent{Operation: "create", BackupID: "backup-1", Job: backup.Job{ID: "job-1", Error: "postgres://secret"}}},
		{Kind: HookRealtimeMessage, Data: &api.RealtimeLifecycleEvent{UserID: id, Collection: "notes"}},
	}
	for _, event := range events {
		raw, err := safeJavaScriptHookData(event)
		if err != nil || !json.Valid(raw) {
			t.Fatalf("safe DTO %s = %s error=%v", event.Kind, raw, err)
		}
		wire := string(raw)
		if strings.Contains(wire, "ObjectKey") || strings.Contains(wire, "objectKey") || strings.Contains(wire, "private/object") ||
			strings.Contains(wire, "thumbnailKey") || strings.Contains(wire, "postgres://secret") {
			t.Fatalf("safe DTO %s exposed private data: %s", event.Kind, wire)
		}
		if strings.Contains(wire, `"Mode"`) || strings.Contains(wire, `"File"`) || strings.Contains(wire, `"Actor"`) {
			t.Fatalf("safe DTO %s retained Go field casing: %s", event.Kind, wire)
		}
		switch event.Kind {
		case HookRecordCreateRequest:
			if !strings.Contains(wire, `"requestID":"request-1"`) || !strings.Contains(wire, `"recordID"`) {
				t.Fatalf("record request hook field casing = %s", wire)
			}
		case HookRecordBefore:
			for _, key := range []string{`"eventID"`, `"recordID"`, `"actorID"`, `"version":0`} {
				if !strings.Contains(wire, key) {
					t.Fatalf("record mutation hook missing %s: %s", key, wire)
				}
			}
		}
	}
	if _, err := safeJavaScriptHookData(&HookEvent{Kind: "unknown", Data: struct{ Secret string }{Secret: "value"}}); err == nil {
		t.Fatal("unsupported hook data type was reflected into JavaScript")
	}
}

func TestJavaScriptHookPatchPreservesBatchIdentityAndLimitsSchemaMutationToPreview(t *testing.T) {
	recordID := uuid.New()
	batch := &api.BatchRequestEvent{Operations: []recordops.BatchOperation{
		{Create: &recordops.CreateRequest{Collection: "notes", Input: map[string]json.RawMessage{"title": json.RawMessage(`"old"`)}, IdempotencyKey: "stable-key"}},
		{Delete: &recordops.DeleteRequest{Collection: "notes", ID: recordID}},
	}}
	patch := json.RawMessage(`{"operations":[{"input":{"title":"new"}},{}]}`)
	if err := applyJavaScriptHookPatch(&HookEvent{Kind: HookRecordBatchRequest, Data: batch}, patch); err != nil {
		t.Fatal(err)
	}
	if string(batch.Operations[0].Create.Input["title"]) != `"new"` || batch.Operations[0].Create.IdempotencyKey != "stable-key" || batch.Operations[1].Delete.ID != recordID {
		t.Fatalf("batch identity or input changed incorrectly: %#v", batch.Operations)
	}
	if err := applyJavaScriptHookPatch(&HookEvent{Kind: HookRecordBatchRequest, Data: batch}, json.RawMessage(`{"operations":[{"operation":"delete"},{}]}`)); err == nil {
		t.Fatal("batch operation identity patch was accepted")
	}

	preview := &api.SchemaImportRequestEvent{Mode: "preview", Document: schema.SchemaDocument{Format: schema.SchemaDocumentFormat}}
	documentPatch := json.RawMessage(`{"document":{"format":"pgbase.schema/v1","collections":[{"name":"notes","userAccess":false,"rules":{}}]}}`)
	if err := applyJavaScriptHookPatch(&HookEvent{Kind: HookSchemaImportPreview, Data: preview}, documentPatch); err != nil || len(preview.Document.Collections) != 1 {
		t.Fatalf("schema preview patch document=%#v error=%v", preview.Document, err)
	}
	apply := &api.SchemaImportRequestEvent{Mode: "apply", Document: preview.Document}
	if err := applyJavaScriptHookPatch(&HookEvent{Kind: HookSchemaImportApply, Data: apply}, documentPatch); err == nil || !strings.Contains(err.Error(), "read-only") {
		t.Fatalf("schema apply patch error = %v", err)
	}
}
