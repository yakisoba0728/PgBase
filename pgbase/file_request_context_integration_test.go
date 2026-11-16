//go:build integration

package pgbase

import (
	"bytes"
	"net/http"
	"net/url"
	"testing"
	"time"

	"github.com/yakisoba0728/PgBase/internal/accessrule"
	"github.com/yakisoba0728/PgBase/internal/filestore"
	"github.com/yakisoba0728/PgBase/internal/identity"
	"github.com/yakisoba0728/PgBase/internal/query"
)

func TestProtectedFileHTTPRetainsRequestRuleContext(t *testing.T) {
	fixture := newPublicUsersAppOptions(t, true, func(options *Options) {
		options.Files = FileOptions{Driver: "local", LocalDirectory: t.TempDir(), MaximumBytes: 1024, DownloadTokenTTL: time.Minute, AllowedTypes: []string{"text/plain"}}
	})
	admin, csrf := setupPublicUsersAdministrator(t, fixture)
	applyFieldVisibilityCollection(t, fixture, admin, csrf, map[string]any{
		"operation": "create", "name": "request_files", "fields": []map[string]any{
			{"name": "title", "type": "text"},
			{"name": "attachment", "type": "file", "options": map[string]any{"maxSelect": 3}},
		},
	})
	adminHeaders := map[string]string{"X-PgBase-CSRF": csrf}
	assertPublicUsersStatus(t, publicUsersRequest(t, fixture, "PUT", "/api/admin/collections/request_files/user-access", map[string]any{"enabled": true}, "", admin, adminHeaders), http.StatusOK)
	owner := registerPublicUser(t, fixture, "file-context-owner@example.test", publicUserOnePass)
	other := registerPublicUser(t, fixture, "file-context-other@example.test", publicUserTwoPass)
	created := publicUsersRequest(t, fixture, "POST", "/api/v1/user/collections/request_files/records", map[string]any{"title": "request context"}, owner.Token, nil, nil)
	assertPublicUsersStatus(t, created, http.StatusCreated)
	var record struct{ ID string }
	decodePublicUsersJSON(t, created, &record)
	base := "/api/v1/user/collections/request_files/records/" + record.ID + "/files/attachment"
	content := []byte("request scoped bytes")
	upload := multipartFileRequest(t, fixture, base, owner.Token, nil, "context.txt", "text/plain", content, nil)
	assertPublicUsersStatus(t, upload, http.StatusCreated)
	var file struct{ ID string }
	decodePublicUsersJSON(t, upload, &file)
	fileURL := "/api/v1/user/files/" + file.ID

	rules := accessrule.OwnerRules()
	expression := "@request.headers.x_file_scope = 'allowed' && @request.auth.email = 'file-context-owner@example.test'"
	parsed, err := query.ParseAccessRule(expression)
	if err != nil {
		t.Fatal(err)
	}
	requestRule := accessrule.Rule{Op: "all", Args: []accessrule.Rule{{Op: "owner"}, parsed}}
	rules.View, rules.Update = requestRule, requestRule
	assertPublicUsersStatus(t, publicUsersRequest(t, fixture, "PUT", "/api/admin/collections/request_files/rules", map[string]any{"rules": rules}, "", admin, adminHeaders), http.StatusOK)
	allowed := map[string]string{"X-File-Scope": "allowed"}
	denied := map[string]string{"X-File-Scope": "denied"}
	// The ordinary record route proves the rule and authenticated fixture work.
	assertPublicUsersStatus(t, publicUsersRequest(t, fixture, "GET", "/api/v1/user/collections/request_files/records/"+record.ID, nil, owner.Token, nil, allowed), http.StatusOK)

	t.Run("list_and_owner_boundary", func(t *testing.T) {
		assertPublicUsersStatus(t, publicUsersRequest(t, fixture, "GET", base, nil, owner.Token, nil, allowed), http.StatusOK)
		assertPublicUsersStatus(t, publicUsersRequest(t, fixture, "GET", base, nil, owner.Token, nil, denied), http.StatusNotFound)
		assertPublicUsersStatus(t, publicUsersRequest(t, fixture, "GET", base, nil, other.Token, nil, allowed), http.StatusNotFound)
	})
	t.Run("token_download_and_range_reauthorize", func(t *testing.T) {
		assertPublicUsersStatus(t, publicUsersRequest(t, fixture, "POST", fileURL+"/token", map[string]any{"variant": "original"}, owner.Token, nil, denied), http.StatusNotFound)
		response := publicUsersRequest(t, fixture, "POST", fileURL+"/token", map[string]any{"variant": "original"}, owner.Token, nil, allowed)
		assertPublicUsersStatus(t, response, http.StatusOK)
		var token struct{ Token string }
		decodePublicUsersJSON(t, response, &token)
		downloadURL := fileURL + "?variant=original&token=" + url.QueryEscape(token.Token)
		assertPublicUsersStatus(t, publicUsersRequest(t, fixture, "GET", downloadURL, nil, owner.Token, nil, denied), http.StatusNotFound)
		full := publicUsersRequest(t, fixture, "GET", downloadURL, nil, owner.Token, nil, allowed)
		assertPublicUsersStatus(t, full, http.StatusOK)
		if !bytes.Equal(full.Body.Bytes(), content) {
			t.Fatal("authorized download changed bytes")
		}
		part := publicUsersRequest(t, fixture, "GET", downloadURL, nil, owner.Token, nil, map[string]string{"X-File-Scope": "allowed", "Range": "bytes=0-6"})
		assertPublicUsersStatus(t, part, http.StatusPartialContent)
		if !bytes.Equal(part.Body.Bytes(), content[:7]) {
			t.Fatal("authorized range changed bytes")
		}
		assertPublicUsersStatus(t, publicUsersRequest(t, fixture, "GET", downloadURL, nil, owner.Token, nil, map[string]string{"X-File-Scope": "denied", "Range": "bytes=0-6"}), http.StatusNotFound)
	})
	t.Run("upload", func(t *testing.T) {
		assertPublicUsersStatus(t, multipartFileRequest(t, fixture, base, owner.Token, nil, "denied.txt", "text/plain", content, denied), http.StatusNotFound)
		assertPublicUsersStatus(t, multipartFileRequest(t, fixture, base, owner.Token, nil, "allowed.txt", "text/plain", content, allowed), http.StatusCreated)
	})
	t.Run("embedded_call_does_not_inherit_http_authority", func(t *testing.T) {
		files := fixture.app.fileWorker.(*filestore.Service)
		collection, id, err := files.ResolveRecordID(t.Context(), "request_files", record.ID)
		if err != nil {
			t.Fatal(err)
		}
		if rows, err := files.List(t.Context(), identity.Subject{Kind: identity.User, ID: owner.User.ID}, collection, id, "attachment"); err == nil || len(rows) != 0 {
			t.Fatalf("request-free embedded call acquired HTTP authority: rows=%v err=%v", rows, err)
		}
	})
	t.Run("delete", func(t *testing.T) {
		assertPublicUsersStatus(t, publicUsersRequest(t, fixture, "DELETE", fileURL, nil, owner.Token, nil, denied), http.StatusNotFound)
		assertPublicUsersStatus(t, publicUsersRequest(t, fixture, "DELETE", fileURL, nil, owner.Token, nil, allowed), http.StatusNoContent)
	})
}
