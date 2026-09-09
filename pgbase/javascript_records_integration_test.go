//go:build integration

package pgbase

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"testing"
)

func TestJavaScriptAndGoFacadesShareRecordAuthorizationAndHooks(t *testing.T) {
	directory := t.TempDir()
	manifest := `{
 "format":"pgbase.scripts/v1",
 "routes":[{"method":"POST","pattern":"/extensions/records","access":"user","handler":{"module":"route.js","export":"create"},"capabilities":["records"]}],
 "hooks":[{"event":"record.create","priority":5,"before":{"module":"hook.js","export":"before"}}]
}`
	for name, source := range map[string]string{
		"pgbase.scripts.json": manifest,
		"route.js": `const pg=require("pgbase"); exports.create=function(request){
 const result=pg.records.create({collection:"notes",input:request.json,idempotencyKey:request.headers["Idempotency-Key"]});
 return {status:201,json:result};
};`,
		"hook.js": `exports.before=function(){return {patch:{input:{title:"hooked by nested JS"}}};};`,
	} {
		if err := os.WriteFile(filepath.Join(directory, name), []byte(source), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	var appRef *App
	fixture := newPublicUsersAppOptions(t, true, func(options *Options) {
		options.JavaScript = &JavaScriptOptions{Directory: directory, Capabilities: map[string]bool{"records": true}, Concurrency: 1, Queue: 1}
		options.CustomRoutes = []CustomRoute{{Method: http.MethodGet, Pattern: "/extensions/go-records", Access: RouteUser,
			Handler: http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
				extensions, err := appRef.ExtensionsForRequest(request.Context())
				if err != nil {
					http.Error(w, "extension context unavailable", http.StatusInternalServerError)
					return
				}
				page, err := extensions.Records.List(request.Context(), "notes", ExtensionListOptions{Limit: 50})
				if err != nil {
					http.Error(w, "record list failed", http.StatusInternalServerError)
					return
				}
				w.Header().Set("Content-Type", "application/json")
				_ = json.NewEncoder(w).Encode(page)
			}),
		}}
	})
	appRef = fixture.app
	if _, err := fixture.app.ExtensionsForRequest(context.Background()); err == nil {
		t.Fatal("plain context acquired extension authority")
	}
	adminCookie, csrf := setupPublicUsersAdministrator(t, fixture)
	createPublicUsersCollection(t, fixture, adminCookie, csrf, "notes")
	enable := publicUsersRequest(t, fixture, http.MethodPut, "/api/admin/collections/notes/user-access",
		map[string]any{"enabled": true}, "", adminCookie, map[string]string{"X-PgBase-CSRF": csrf})
	assertPublicUsersStatus(t, enable, http.StatusOK)
	first := registerPublicUser(t, fixture, "js-first@example.test", publicUserOnePass)
	second := registerPublicUser(t, fixture, "js-second@example.test", publicUserTwoPass)

	for attempt := range 2 {
		created := publicUsersRequest(t, fixture, http.MethodPost, "/extensions/records", map[string]any{"title": "original"}, first.Token, nil,
			map[string]string{"Idempotency-Key": "javascript-record-create-0001"})
		assertPublicUsersStatus(t, created, http.StatusCreated)
		var result struct {
			Data     map[string]any `json:"data"`
			Replayed bool           `json:"replayed"`
		}
		decodePublicUsersJSON(t, created, &result)
		if result.Data["title"] != "hooked by nested JS" || result.Replayed != (attempt == 1) {
			t.Fatalf("attempt %d result = %#v", attempt, result)
		}
	}
	owned := publicUsersRequest(t, fixture, http.MethodGet, "/extensions/go-records", nil, first.Token, nil, nil)
	assertPublicUsersStatus(t, owned, http.StatusOK)
	var firstPage struct {
		Items []json.RawMessage
	}
	decodePublicUsersJSON(t, owned, &firstPage)
	if len(firstPage.Items) != 1 {
		t.Fatalf("owner facade items = %d", len(firstPage.Items))
	}
	foreign := publicUsersRequest(t, fixture, http.MethodGet, "/extensions/go-records", nil, second.Token, nil, nil)
	assertPublicUsersStatus(t, foreign, http.StatusOK)
	var secondPage struct {
		Items []json.RawMessage
	}
	decodePublicUsersJSON(t, foreign, &secondPage)
	if len(secondPage.Items) != 0 {
		t.Fatalf("foreign facade exposed %d items", len(secondPage.Items))
	}
}
