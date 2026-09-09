//go:build integration

package pgbase

import (
	"encoding/json"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/webisnoteasy/PgBase/internal/accessrule"
)

func TestN4FieldsQueriesRulesAndBatchOverHTTP(t *testing.T) {
	app := newPublicUsersApp(t, true)
	admin, csrf := setupPublicUsersAdministrator(t, app)
	change := map[string]any{"operation": "create", "name": "n4_http", "fields": []map[string]any{
		{"name": "slug", "type": "text", "required": true, "unique": true, "options": map[string]any{"autogeneratePattern": `item-[a-z0-9]{12}`}},
		{"name": "title", "type": "text", "required": true},
		{"name": "email", "type": "text", "required": true, "options": map[string]any{"format": "email", "allowedDomains": []string{" Example.COM. "}, "deniedDomains": []string{"Private.Example.com."}}},
		{"name": "location", "type": "geoPoint", "indexed": true},
	}}
	planAndApplySchemaChange(t, app, admin, csrf, change, http.StatusCreated, nil)
	headers := map[string]string{"X-PgBase-CSRF": csrf}
	assertPublicUsersStatus(t, publicUsersRequest(t, app, http.MethodPut, "/api/admin/collections/n4_http/user-access", map[string]any{"enabled": true}, "", admin, headers), http.StatusOK)

	visible := accessrule.Rule{Op: "all", Args: []accessrule.Rule{
		{Op: "eq", Field: "title:lower", Value: json.RawMessage(`"published"`)},
		{Op: "gt", Field: "location.lon", Value: json.RawMessage(`100`)},
	}}
	rules := accessrule.OwnerRules()
	rules.List, rules.View = visible, visible
	assertPublicUsersStatus(t, publicUsersRequest(t, app, http.MethodPut, "/api/admin/collections/n4_http/rules", map[string]any{"rules": rules}, "", admin, headers), http.StatusOK)

	user := registerPublicUser(t, app, "n4-http@example.test", publicUserOnePass)
	base := "/api/v1/user/collections/n4_http/records"
	created := publicUsersRequest(t, app, http.MethodPost, base, map[string]any{
		"title": "Published", "email": "reader@example.com", "location": map[string]any{"lon": 127.1, "lat": 37.4},
	}, user.Token, nil, map[string]string{"Idempotency-Key": "n4-http-create-0001"})
	assertPublicUsersStatus(t, created, http.StatusCreated)
	var record map[string]any
	decodePublicUsersJSON(t, created, &record)
	assertN4GeneratedSlug(t, record)

	replayed := publicUsersRequest(t, app, http.MethodPost, base, map[string]any{
		"title": "Published", "email": "reader@example.com", "location": map[string]any{"lon": 127.1, "lat": 37.4},
	}, user.Token, nil, map[string]string{"Idempotency-Key": "n4-http-create-0001"})
	assertPublicUsersStatus(t, replayed, http.StatusCreated)
	var replayedRecord map[string]any
	decodePublicUsersJSON(t, replayed, &replayedRecord)
	if replayedRecord["id"] != record["id"] || replayedRecord["slug"] != record["slug"] {
		t.Fatalf("idempotent replay changed generated record: first=%#v replay=%#v", record, replayedRecord)
	}

	denied := publicUsersRequest(t, app, http.MethodPost, base, map[string]any{
		"title": "Published", "email": "reader@child.private.example.com", "location": map[string]any{"lon": 127.1, "lat": 37.4},
	}, user.Token, nil, map[string]string{"Idempotency-Key": "n4-http-denied-0001"})
	if denied.Code < 400 {
		t.Fatalf("denied email domain status=%d body=%s", denied.Code, denied.Body.String())
	}

	filter := url.QueryEscape("title:lower = 'published' && location.lon > 100")
	listed := publicUsersRequest(t, app, http.MethodGet, base+"?filter="+filter, nil, user.Token, nil, nil)
	assertPublicUsersStatus(t, listed, http.StatusOK)
	var page struct {
		Items []map[string]any `json:"items"`
	}
	decodePublicUsersJSON(t, listed, &page)
	if len(page.Items) != 1 || page.Items[0]["id"] != record["id"] {
		t.Fatalf("lower/geo query or typed rules returned %#v", page.Items)
	}

	batch := map[string]any{"operations": []map[string]any{
		{"operation": "create", "collection": "n4_http", "input": map[string]any{"title": "Published", "email": "batch@example.com", "location": map[string]any{"lon": 128, "lat": 36}}, "idempotencyKey": "n4-http-batch-good-0001"},
		{"operation": "create", "collection": "n4_http", "input": map[string]any{"title": "Published", "email": "batch@private.example.com", "location": map[string]any{"lon": 128, "lat": 36}}, "idempotencyKey": "n4-http-batch-bad-0001"},
	}}
	failedBatch := publicUsersRequest(t, app, http.MethodPost, "/api/v1/user/collections/batch", batch, user.Token, nil, nil)
	if failedBatch.Code < 400 {
		t.Fatalf("denied batch status=%d body=%s", failedBatch.Code, failedBatch.Body.String())
	}
	listed = publicUsersRequest(t, app, http.MethodGet, base+"?filter="+url.QueryEscape("email = 'batch@example.com'"), nil, user.Token, nil, nil)
	assertPublicUsersStatus(t, listed, http.StatusOK)
	decodePublicUsersJSON(t, listed, &page)
	if len(page.Items) != 0 {
		t.Fatalf("failed batch committed generated row: %#v", page.Items)
	}
}

func assertN4GeneratedSlug(t *testing.T, record map[string]any) {
	t.Helper()
	slug, ok := record["slug"].(string)
	if !ok || !strings.HasPrefix(slug, "item-") || len(slug) != len("item-")+12 {
		t.Fatalf("generated slug=%#v", record["slug"])
	}
}
