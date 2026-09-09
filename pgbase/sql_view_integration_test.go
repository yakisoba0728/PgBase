//go:build integration

package pgbase

import (
	"net/url"
	"testing"

	"github.com/webisnoteasy/PgBase/internal/accessrule"
)

func TestSQLViewHTTPIndependentRulesCursorAndContentETag(t *testing.T) {
	app := newPublicUsersApp(t, true)
	cookie, csrf := setupPublicUsersAdministrator(t, app)
	headers := map[string]string{"X-PgBase-CSRF": csrf}
	apply := func(change map[string]any) {
		t.Helper()
		plan := publicUsersRequest(t, app, "POST", "/api/admin/collections/plan", change, "", cookie, headers)
		assertPublicUsersStatus(t, plan, 200)
		var value struct {
			BaseVersion int64  `json:"baseVersion"`
			Checksum    string `json:"checksum"`
		}
		decodePublicUsersJSON(t, plan, &value)
		change["baseVersion"] = value.BaseVersion
		change["checksum"] = value.Checksum
		assertPublicUsersStatus(t, publicUsersRequest(t, app, "POST", "/api/admin/collections/apply", change, "", cookie, headers), 201)
	}
	apply(map[string]any{"operation": "create", "name": "view_sales", "fields": []map[string]any{{"name": "category", "type": "uuid"}, {"name": "amount", "type": "integer"}}})
	add := func(category string, amount int) {
		t.Helper()
		assertPublicUsersStatus(t, publicUsersRequest(t, app, "POST", "/api/v1/collections/view_sales/records", map[string]any{"category": category, "amount": amount}, "", cookie, headers), 201)
	}
	first := "019551d0-e9c0-7000-8000-000000000001"
	second := "019551d0-e9c0-7000-8000-000000000002"
	add(first, 4)
	add(first, 6)
	add(second, 3)
	apply(map[string]any{"operation": "create", "name": "view_totals", "view": map[string]any{"sql": "SELECT category AS id, sum(amount)::bigint AS total FROM app.view_sales GROUP BY category"}})
	page := publicUsersRequest(t, app, "GET", "/api/v1/collections/view_totals/records?limit=1&sort=-total", nil, "", cookie, nil)
	assertPublicUsersStatus(t, page, 200)
	var listed struct {
		Items      []map[string]any `json:"items"`
		NextCursor string           `json:"nextCursor"`
	}
	decodePublicUsersJSON(t, page, &listed)
	if len(listed.Items) != 1 || listed.Items[0]["id"] != first || listed.NextCursor == "" {
		t.Fatalf("page=%s", page.Body.String())
	}
	next := publicUsersRequest(t, app, "GET", "/api/v1/collections/view_totals/records?limit=1&sort=-total&cursor="+url.QueryEscape(listed.NextCursor), nil, "", cookie, nil)
	assertPublicUsersStatus(t, next, 200)
	listed.NextCursor = ""
	decodePublicUsersJSON(t, next, &listed)
	if len(listed.Items) != 1 || listed.Items[0]["id"] != second || listed.NextCursor != "" {
		t.Fatalf("next=%+v", listed)
	}
	path := "/api/v1/collections/view_totals/records/" + first
	before := publicUsersRequest(t, app, "GET", path, nil, "", cookie, nil)
	assertPublicUsersStatus(t, before, 200)
	add(first, 1)
	after := publicUsersRequest(t, app, "GET", path, nil, "", cookie, nil)
	assertPublicUsersStatus(t, after, 200)
	if before.Header().Get("ETag") == "" || before.Header().Get("ETag") == after.Header().Get("ETag") {
		t.Fatal("computed content change did not change ETag")
	}
	denied := publicUsersRequest(t, app, "POST", "/api/v1/collections/view_totals/records", map[string]any{"total": 7}, "", cookie, headers)
	if denied.Code < 400 {
		t.Fatal("SQL view writable")
	}
	user := registerPublicUser(t, app, "view-reader@example.test", publicUserOnePass)
	publicPath := "/api/v1/user/collections/view_totals/records"
	denied = publicUsersRequest(t, app, "GET", publicPath, nil, user.Token, nil, nil)
	if denied.Code < 400 {
		t.Fatal("SQL view public without opt-in")
	}
	assertPublicUsersStatus(t, publicUsersRequest(t, app, "PUT", "/api/admin/collections/view_totals/user-access", map[string]any{"enabled": true}, "", cookie, headers), 200)
	rules := accessrule.OwnerRules()
	rules.List = accessrule.Rule{Op: "allow"}
	rules.View = accessrule.Rule{Op: "allow"}
	assertPublicUsersStatus(t, publicUsersRequest(t, app, "PUT", "/api/admin/collections/view_totals/rules", map[string]any{"rules": rules}, "", cookie, headers), 200)
	allowed := publicUsersRequest(t, app, "GET", publicPath, nil, user.Token, nil, nil)
	assertPublicUsersStatus(t, allowed, 200)
	decodePublicUsersJSON(t, allowed, &listed)
	if len(listed.Items) != 2 {
		t.Fatalf("own view access policy ignored: %s", allowed.Body.String())
	}
	assertPublicUsersStatus(t, publicUsersRequest(t, app, "GET", publicPath+"/"+first, nil, user.Token, nil, nil), 200)
	// The base collection remains private; the administrator independently chose
	// which aggregate fields this view exposes.
	if response := publicUsersRequest(t, app, "GET", "/api/v1/user/collections/view_sales/records", nil, user.Token, nil, nil); response.Code < 400 {
		t.Fatal("view permission leaked to source")
	}
	rules.List = accessrule.Rule{Op: "deny"}
	assertPublicUsersStatus(t, publicUsersRequest(t, app, "PUT", "/api/admin/collections/view_totals/rules", map[string]any{"rules": rules}, "", cookie, headers), 200)
}
