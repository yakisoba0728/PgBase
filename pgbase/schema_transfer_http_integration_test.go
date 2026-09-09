//go:build integration

package pgbase

import (
	"encoding/json"
	"net/http"
	"reflect"
	"testing"

	"github.com/google/uuid"
)

func TestSchemaTransferHTTPRoundtripAndReplay(t *testing.T) {
	app := newPublicUsersApp(t, true)
	cookie, csrf := setupPublicUsersAdministrator(t, app)
	headers := map[string]string{"X-PgBase-CSRF": csrf}
	createPublicUsersCollection(t, app, cookie, csrf, "portable_notes")
	rules := map[string]any{"list": map[string]any{"op": "eq", "field": "title", "value": "published"}, "view": map[string]any{"op": "eq", "field": "title", "value": "published"}, "create": map[string]any{"op": "owner"}, "update": map[string]any{"op": "owner"}, "delete": map[string]any{"op": "owner"}}
	assertPublicUsersStatus(t, publicUsersRequest(t, app, "PUT", "/api/admin/collections/portable_notes/rules", map[string]any{"rules": rules}, "", cookie, headers), 200)
	exported := publicUsersRequest(t, app, "GET", "/api/admin/schema/export", nil, "", cookie, nil)
	assertPublicUsersStatus(t, exported, 200)
	var document json.RawMessage
	decodePublicUsersJSON(t, exported, &document)
	assertPublicUsersStatus(t, publicUsersRequest(t, app, "POST", "/api/admin/schema/import/preview", document, "", cookie, nil), http.StatusForbidden)
	preview := publicUsersRequest(t, app, "POST", "/api/admin/schema/import/preview", document, "", cookie, headers)
	assertPublicUsersStatus(t, preview, 200)
	var plan struct {
		Document    json.RawMessage `json:"document"`
		BaseVersion int64           `json:"baseVersion"`
		Checksum    string          `json:"checksum"`
	}
	decodePublicUsersJSON(t, preview, &plan)
	op := uuid.NewString()
	applyHeaders := map[string]string{"X-PgBase-CSRF": csrf, "PgBase-Operation-ID": op, "Idempotency-Key": "http-schema-roundtrip-0001"}
	body := map[string]any{"document": plan.Document, "baseVersion": plan.BaseVersion, "checksum": plan.Checksum}
	apply := publicUsersRequest(t, app, "POST", "/api/admin/schema/import/apply", body, "", cookie, applyHeaders)
	assertPublicUsersStatus(t, apply, 200)
	var receipt map[string]any
	decodePublicUsersJSON(t, apply, &receipt)
	if receipt["status"] != "committed" {
		t.Fatalf("receipt=%v", receipt)
	}
	replay := publicUsersRequest(t, app, "POST", "/api/admin/schema/import/apply", body, "", cookie, applyHeaders)
	assertPublicUsersStatus(t, replay, 200)
	var replayReceipt map[string]any
	decodePublicUsersJSON(t, replay, &replayReceipt)
	if !reflect.DeepEqual(receipt, replayReceipt) {
		t.Fatalf("replay=%v initial=%v", replayReceipt, receipt)
	}
	status := publicUsersRequest(t, app, "GET", "/api/admin/schema/import/operations/"+op, nil, "", cookie, nil)
	assertPublicUsersStatus(t, status, 200)
	var statusReceipt map[string]any
	decodePublicUsersJSON(t, status, &statusReceipt)
	if !reflect.DeepEqual(receipt, statusReceipt) {
		t.Fatalf("status=%v initial=%v", statusReceipt, receipt)
	}
	body["baseVersion"] = plan.BaseVersion + 1
	assertPublicUsersStatus(t, publicUsersRequest(t, app, "POST", "/api/admin/schema/import/apply", body, "", cookie, applyHeaders), 409)
}

func TestSchemaTransferHTTPPreservesCrossCollectionRelationsAndIndexes(t *testing.T) {
	app := newPublicUsersApp(t, true)
	cookie, csrf := setupPublicUsersAdministrator(t, app)
	owner := json.RawMessage(`{"list":{"op":"owner"},"view":{"op":"owner"},"create":{"op":"owner"},"update":{"op":"owner"},"delete":{"op":"owner"}}`)
	relation := func(name, target string) map[string]any {
		return map[string]any{"name": name, "type": "relation", "indexed": true, "relation": map[string]any{"targetCollection": target, "cardinality": "single", "minSelect": 0, "maxSelect": 1, "onDelete": "restrict"}}
	}
	document := map[string]any{"format": "pgbase.schema/v1", "collections": []map[string]any{
		{"name": "portable_authors", "fields": []map[string]any{{"name": "name", "type": "text", "presentable": true, "help": "Author name"}, relation("favorite", "portable_articles")}, "rules": owner},
		{"name": "portable_articles", "fields": []map[string]any{{"name": "title", "type": "text"}, {"name": "edition", "type": "integer"}, relation("author", "portable_authors")}, "indexes": []map[string]any{{"name": "title_idx", "fields": []string{"title", "edition"}, "unique": true}}, "rules": owner},
	}}
	applyDocument := func(fixture *publicUsersFixture, administrator *http.Cookie, token string, doc any) {
		t.Helper()
		headers := map[string]string{"X-PgBase-CSRF": token}
		preview := publicUsersRequest(t, fixture, "POST", "/api/admin/schema/import/preview", doc, "", administrator, headers)
		assertPublicUsersStatus(t, preview, 200)
		var plan struct {
			Document    json.RawMessage `json:"document"`
			BaseVersion int64           `json:"baseVersion"`
			Checksum    string          `json:"checksum"`
		}
		decodePublicUsersJSON(t, preview, &plan)
		headers["PgBase-Operation-ID"], headers["Idempotency-Key"] = uuid.NewString(), uuid.NewString()
		body := map[string]any{"document": plan.Document, "baseVersion": plan.BaseVersion, "checksum": plan.Checksum}
		assertPublicUsersStatus(t, publicUsersRequest(t, fixture, "POST", "/api/admin/schema/import/apply", body, "", administrator, headers), 200)
	}
	applyDocument(app, cookie, csrf, document)
	exported := publicUsersRequest(t, app, "GET", "/api/admin/schema/export", nil, "", cookie, nil)
	assertPublicUsersStatus(t, exported, 200)
	var wire json.RawMessage
	decodePublicUsersJSON(t, exported, &wire)
	target := newPublicUsersApp(t, true)
	targetCookie, targetCSRF := setupPublicUsersAdministrator(t, target)
	applyDocument(target, targetCookie, targetCSRF, wire)
	fresh := publicUsersRequest(t, target, "GET", "/api/admin/schema/export", nil, "", targetCookie, nil)
	assertPublicUsersStatus(t, fresh, 200)
	var sourceDoc, targetDoc any
	if err := json.Unmarshal(wire, &sourceDoc); err != nil {
		t.Fatal(err)
	}
	decodePublicUsersJSON(t, fresh, &targetDoc)
	if !reflect.DeepEqual(sourceDoc, targetDoc) {
		t.Fatalf("portable roundtrip changed schema: source=%s target=%s", wire, fresh.Body.String())
	}
}
