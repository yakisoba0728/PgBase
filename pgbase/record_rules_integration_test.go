//go:build integration

package pgbase

import (
	"context"
	"net/http"
	"testing"

	"github.com/yakisoba0728/PgBase/internal/accessrule"
)

func TestTypedRecordRulesFilterBeforePaginationAndRevokeCursors(t *testing.T) {
	app := newPublicUsersApp(t, true)
	admin, csrf := setupPublicUsersAdministrator(t, app)
	createPublicUsersCollection(t, app, admin, csrf, "notes")
	assertPublicUsersStatus(t, publicUsersRequest(t, app, "PUT", "/api/admin/collections/notes/user-access", map[string]any{"enabled": true}, "", admin, map[string]string{"X-PgBase-CSRF": csrf}), 200)
	owner := registerPublicUser(t, app, "rules-owner@example.com", publicUserOnePass)
	other := registerPublicUser(t, app, "rules-other@example.com", publicUserTwoPass)
	for _, title := range []string{"private", "published", "published"} {
		assertPublicUsersStatus(t, publicUsersRequest(t, app, "POST", "/api/v1/user/collections/notes/records", map[string]any{"title": title}, other.Token, nil, nil), 201)
	}
	rules := accessrule.OwnerRules()
	visible := accessrule.Rule{Op: "any", Args: []accessrule.Rule{{Op: "owner"}, {Op: "eq", Field: "title", Value: []byte(`"published"`)}}}
	rules.List, rules.View = visible, visible
	rules.Create = accessrule.Rule{Op: "ne", Field: "title", Value: []byte(`"forbidden"`)}
	set := func() {
		t.Helper()
		assertPublicUsersStatus(t, publicUsersRequest(t, app, "PUT", "/api/admin/collections/notes/rules", map[string]any{"rules": rules}, "", admin, map[string]string{"X-PgBase-CSRF": csrf}), 200)
	}
	set()
	page := publicUsersRequest(t, app, "GET", "/api/v1/user/collections/notes/records?limit=1", nil, owner.Token, nil, nil)
	assertPublicUsersStatus(t, page, 200)
	var listed struct {
		Items      []map[string]any `json:"items"`
		NextCursor string           `json:"nextCursor"`
	}
	decodePublicUsersJSON(t, page, &listed)
	if len(listed.Items) != 1 || listed.Items[0]["title"] != "published" || listed.NextCursor == "" {
		t.Fatalf("permission pagination=%s", page.Body.String())
	}
	published := listed.Items[0]["id"].(string)
	assertPublicUsersStatus(t, publicUsersRequest(t, app, "GET", "/api/v1/user/collections/notes/records/"+published, nil, owner.Token, nil, nil), 200)
	assertPublicUsersStatus(t, publicUsersRequest(t, app, "PATCH", "/api/v1/user/collections/notes/records/"+published, map[string]any{"title": "stolen"}, owner.Token, nil, nil), 404)
	var before, after int
	if err := app.app.pool.QueryRow(context.Background(), `SELECT count(*) FROM pgbase_meta.record_outbox`).Scan(&before); err != nil {
		t.Fatal(err)
	}
	assertPublicUsersStatus(t, publicUsersRequest(t, app, "POST", "/api/v1/user/collections/notes/records", map[string]any{"title": "forbidden"}, owner.Token, nil, nil), 403)
	if err := app.app.pool.QueryRow(context.Background(), `SELECT count(*) FROM pgbase_meta.record_outbox`).Scan(&after); err != nil || after != before {
		t.Fatalf("denied create published artifact %d/%d %v", before, after, err)
	}
	rules.List = accessrule.Rule{Op: "deny"}
	set()
	assertPublicUsersStatus(t, publicUsersRequest(t, app, http.MethodGet, "/api/v1/user/collections/notes/records?limit=1&cursor="+listed.NextCursor, nil, owner.Token, nil, nil), 400)
	page = publicUsersRequest(t, app, "GET", "/api/v1/user/collections/notes/records", nil, owner.Token, nil, nil)
	assertPublicUsersStatus(t, page, 200)
	decodePublicUsersJSON(t, page, &listed)
	if len(listed.Items) != 0 {
		t.Fatal("deny list rule ignored")
	}
}

func TestNativeExpressionRulesSaveAtomicallyAndFilterHTTPRecords(t *testing.T) {
	app := newPublicUsersApp(t, true)
	admin, csrf := setupPublicUsersAdministrator(t, app)
	createPublicUsersCollection(t, app, admin, csrf, "native_rules")
	headers := map[string]string{"X-PgBase-CSRF": csrf}
	path := "/api/admin/collections/native_rules/rules"
	expression := "title = 'published'"
	rules := accessrule.OwnerRules()
	rules.List = accessrule.Rule{Op: "expression", Expression: &expression}
	saved := publicUsersRequest(t, app, "PUT", path, map[string]any{"rules": rules}, "", admin, headers)
	assertPublicUsersStatus(t, saved, http.StatusOK)
	var before, after struct {
		Rules    accessrule.Rules `json:"rules"`
		Revision int64            `json:"revision"`
	}
	decodePublicUsersJSON(t, saved, &before)
	if before.Rules.List.Op != "eq" || before.Rules.List.Expression == nil || before.Rules.Update.Op != "owner" {
		t.Fatalf("mixed policy not saved: %s", saved.Body.String())
	}
	// Syntax is valid, but catalog validation must reject the entire policy.
	invalid := "missing_column = true"
	rules.Update = accessrule.Rule{Op: "expression", Expression: &invalid}
	assertPublicUsersStatus(t, publicUsersRequest(t, app, "PUT", path, map[string]any{"rules": rules}, "", admin, headers), http.StatusBadRequest)
	fresh := publicUsersRequest(t, app, "GET", path, nil, "", admin, nil)
	assertPublicUsersStatus(t, fresh, http.StatusOK)
	decodePublicUsersJSON(t, fresh, &after)
	if after.Revision != before.Revision || after.Rules.Update.Op != "owner" || after.Rules.List.Expression == nil || *after.Rules.List.Expression != expression {
		t.Fatalf("rejected policy changed stored rules: %s", fresh.Body.String())
	}
	assertPublicUsersStatus(t, publicUsersRequest(t, app, "PUT", "/api/admin/collections/native_rules/user-access", map[string]any{"enabled": true}, "", admin, headers), http.StatusOK)
	owner := registerPublicUser(t, app, "native-rules@example.test", publicUserOnePass)
	for _, title := range []string{"private", "published"} {
		assertPublicUsersStatus(t, publicUsersRequest(t, app, "POST", "/api/v1/user/collections/native_rules/records", map[string]any{"title": title}, owner.Token, nil, nil), http.StatusCreated)
	}
	page := publicUsersRequest(t, app, "GET", "/api/v1/user/collections/native_rules/records", nil, owner.Token, nil, nil)
	assertPublicUsersStatus(t, page, http.StatusOK)
	var listed struct {
		Items []map[string]any `json:"items"`
	}
	decodePublicUsersJSON(t, page, &listed)
	if len(listed.Items) != 1 || listed.Items[0]["title"] != "published" {
		t.Fatalf("native expression did not filter records: %s", page.Body.String())
	}
}

func TestUnsavedRulePreviewUsesActualSubjectAndLeavesPolicyUnchanged(t *testing.T) {
	app := newPublicUsersApp(t, true)
	admin, csrf := setupPublicUsersAdministrator(t, app)
	createPublicUsersCollection(t, app, admin, csrf, "preview_notes")
	headers := map[string]string{"X-PgBase-CSRF": csrf}
	assertPublicUsersStatus(t, publicUsersRequest(t, app, "PUT", "/api/admin/collections/preview_notes/user-access", map[string]any{"enabled": true}, "", admin, headers), 200)
	owner := registerPublicUser(t, app, "preview-owner@example.test", publicUserOnePass)
	other := registerPublicUser(t, app, "preview-other@example.test", publicUserTwoPass)
	created := publicUsersRequest(t, app, "POST", "/api/v1/user/collections/preview_notes/records", map[string]any{"title": "published"}, owner.Token, nil, nil)
	assertPublicUsersStatus(t, created, 201)
	var record map[string]any
	decodePublicUsersJSON(t, created, &record)
	preview := func(rule accessrule.Rule, user any, want bool) {
		t.Helper()
		response := publicUsersRequest(t, app, "POST", "/api/admin/collections/preview_notes/rules/preview", map[string]any{"rule": rule, "action": "view", "userId": user, "recordIds": []any{record["id"]}}, "", admin, headers)
		assertPublicUsersStatus(t, response, 200)
		var result struct {
			Items []struct {
				Allowed bool `json:"allowed"`
			} `json:"items"`
		}
		decodePublicUsersJSON(t, response, &result)
		if len(result.Items) != 1 || result.Items[0].Allowed != want {
			t.Fatalf("preview=%s", response.Body.String())
		}
	}
	preview(accessrule.Rule{Op: "owner"}, owner.User.ID, true)
	preview(accessrule.Rule{Op: "owner"}, other.User.ID, false)
	preview(accessrule.Rule{Op: "eq", Field: "title", Value: []byte(`"published"`)}, other.User.ID, true)
	// The unsaved preview grants no permission to the actual user route.
	assertPublicUsersStatus(t, publicUsersRequest(t, app, "GET", "/api/v1/user/collections/preview_notes/records/"+record["id"].(string), nil, other.Token, nil, nil), 404)
	assertPublicUsersStatus(t, publicUsersRequest(t, app, "PUT", "/api/admin/collections/preview_notes/user-access", map[string]any{"enabled": false}, "", admin, headers), 200)
	preview(accessrule.Rule{Op: "allow"}, owner.User.ID, false)
}

func TestRulePreviewHTTPPreservesPublicTextIdentities(t *testing.T) {
	app := newPublicUsersApp(t, true)
	admin, csrf := setupPublicUsersAdministrator(t, app)
	headers := map[string]string{"X-PgBase-CSRF": csrf}
	change := map[string]any{
		"operation": "create", "name": "preview_text_records",
		"recordId": map[string]any{"kind": "text", "minLength": 1, "maxLength": 32, "pattern": "^[A-Za-z0-9]+$"},
		"fields":   []map[string]any{{"name": "title", "type": "text"}},
	}
	plan := publicUsersRequest(t, app, "POST", "/api/admin/collections/plan", change, "", admin, headers)
	assertPublicUsersStatus(t, plan, 200)
	var planned struct {
		BaseVersion int64
		Checksum    string
	}
	decodePublicUsersJSON(t, plan, &planned)
	change["baseVersion"], change["checksum"] = planned.BaseVersion, planned.Checksum
	assertPublicUsersStatus(t, publicUsersRequest(t, app, "POST", "/api/admin/collections/apply", change, "", admin, headers), 201)
	assertPublicUsersStatus(t, publicUsersRequest(t, app, "PUT", "/api/admin/collections/preview_text_records/user-access", map[string]any{"enabled": true}, "", admin, headers), 200)
	assertPublicUsersStatus(t, publicUsersRequest(t, app, "POST", "/api/v1/collections/preview_text_records/records", map[string]any{"id": "PublicRecord001", "title": "visible"}, "", admin, headers), 201)
	response := publicUsersRequest(t, app, "POST", "/api/admin/collections/preview_text_records/rules/preview", map[string]any{
		"rule": accessrule.Rule{Op: "allow"}, "action": "view", "recordIds": []string{"PublicRecord001", "MissingRecord"},
	}, "", admin, headers)
	assertPublicUsersStatus(t, response, 200)
	var result struct {
		Items []struct {
			RecordID string `json:"recordId"`
			Allowed  bool   `json:"allowed"`
		} `json:"items"`
	}
	decodePublicUsersJSON(t, response, &result)
	if len(result.Items) != 2 || result.Items[0].RecordID != "PublicRecord001" || !result.Items[0].Allowed || result.Items[1].RecordID != "MissingRecord" || result.Items[1].Allowed {
		t.Fatalf("public text preview = %s", response.Body.String())
	}
}
