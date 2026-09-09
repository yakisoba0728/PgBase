//go:build integration

package pgbase

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

func TestAdvancedEditorFiveMiBHTTPRoundTripAndBatch(t *testing.T) {
	fixture := newPublicUsersApp(t, true)
	administrator, csrf := setupPublicUsersAdministrator(t, fixture)
	headers := map[string]string{"X-PgBase-CSRF": csrf}
	change := map[string]any{"operation": "create", "name": "large_editor", "fields": []map[string]any{
		{"name": "body", "type": "editor", "required": true},
		{"name": "location", "type": "geoPoint", "indexed": true},
	}}
	preview := publicUsersRequest(t, fixture, http.MethodPost, "/api/admin/collections/plan", change, "", administrator, headers)
	assertPublicUsersStatus(t, preview, http.StatusOK)
	var plan struct {
		BaseVersion int64  `json:"baseVersion"`
		Checksum    string `json:"checksum"`
	}
	decodePublicUsersJSON(t, preview, &plan)
	change["baseVersion"], change["checksum"] = plan.BaseVersion, plan.Checksum
	assertPublicUsersStatus(t, publicUsersRequest(t, fixture, http.MethodPost, "/api/admin/collections/apply", change, "", administrator, headers), http.StatusCreated)
	// '<' becomes six bytes in Go JSON. A default-size editor must survive the
	// actual request parser, canonical record limit, outbox and response buffers.
	body := strings.Repeat("<", 5<<20)
	input := map[string]any{"body": body, "location": map[string]any{"lon": 127.5, "lat": 37.5}}
	createHeaders := map[string]string{"X-PgBase-CSRF": csrf, "Idempotency-Key": "large-editor-five-mib-0001"}
	created := publicUsersRequest(t, fixture, http.MethodPost, "/api/v1/collections/large_editor/records", input, "", administrator, createHeaders)
	if created.Code != http.StatusCreated {
		t.Fatalf("create status=%d body=%.500s", created.Code, created.Body.String())
	}
	var record struct {
		ID   string `json:"id"`
		Body string `json:"body"`
	}
	decodePublicUsersJSON(t, created, &record)
	if record.Body != body {
		t.Fatal("created editor body changed")
	}
	got := publicUsersRequest(t, fixture, http.MethodGet, "/api/v1/collections/large_editor/records/"+record.ID, nil, "", administrator, nil)
	if got.Code != http.StatusOK {
		t.Fatalf("get status=%d body=%.500s", got.Code, got.Body.String())
	}
	decodePublicUsersJSON(t, got, &record)
	if record.Body != body {
		t.Fatal("stored editor body changed")
	}
	listed := publicUsersRequest(t, fixture, http.MethodGet, "/api/v1/collections/large_editor/records?limit=1", nil, "", administrator, nil)
	if listed.Code != http.StatusOK {
		t.Fatalf("list status=%d body=%.500s", listed.Code, listed.Body.String())
	}
	var page struct {
		Items []struct {
			Body string `json:"body"`
		} `json:"items"`
	}
	decodePublicUsersJSON(t, listed, &page)
	if len(page.Items) != 1 || page.Items[0].Body != body {
		t.Fatal("page lost editor content")
	}
	oversized := publicUsersRequest(t, fixture, http.MethodPatch, "/api/v1/collections/large_editor/records/"+record.ID, map[string]any{"body": body + "x"}, "", administrator, headers)
	if oversized.Code != http.StatusUnprocessableEntity {
		t.Fatalf("oversized status=%d", oversized.Code)
	}
	batch := map[string]any{"requests": []map[string]any{{"method": "POST", "url": "/api/v1/collections/large_editor/records", "headers": map[string]string{"Idempotency-Key": "large-editor-five-mib-batch"}, "body": input}}}
	batched := publicUsersRequest(t, fixture, http.MethodPost, "/api/v1/collections/batch", batch, "", administrator, headers)
	if batched.Code != http.StatusOK {
		t.Fatalf("batch status=%d body=%.500s", batched.Code, batched.Body.String())
	}
	var decoded any
	if err := json.Unmarshal(batched.Body.Bytes(), &decoded); err != nil {
		t.Fatal(err)
	}
	// The complete result must contain the exact editor, not a truncated receipt.
	var find func(any) bool
	find = func(value any) bool {
		switch v := value.(type) {
		case string:
			return v == body
		case []any:
			for _, item := range v {
				if find(item) {
					return true
				}
			}
		case map[string]any:
			for _, item := range v {
				if find(item) {
					return true
				}
			}
		}
		return false
	}
	if !find(decoded) {
		t.Fatal("batch result omitted editor content")
	}
}
