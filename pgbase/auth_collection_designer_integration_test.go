//go:build integration

package pgbase

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/uuid"
)

// The schema designer's single-collection plan/apply keeps an auth collection's
// realm in step: added, renamed and dropped fields reach user profiles, and the
// collection is renamed and deleted together with its realm.
func TestAuthCollectionDesignerChangesReachTheRealm(t *testing.T) {
	app := newPublicUsersApp(t, true)
	cookie, csrf := setupPublicUsersAdministrator(t, app)
	admin := map[string]string{"X-PgBase-CSRF": csrf}
	send := func(method, path string, body any, want int) *httptest.ResponseRecorder {
		t.Helper()
		response := publicUsersRequest(t, app, method, path, body, "", cookie, admin)
		assertPublicUsersStatus(t, response, want)
		return response
	}

	var scaffolds struct {
		Auth struct {
			Collection     map[string]any `json:"collection"`
			AuthCollection map[string]any `json:"authCollection"`
		} `json:"auth"`
	}
	decodePublicUsersJSON(t, send(http.MethodGet, "/api/admin/collections/scaffolds", nil, http.StatusOK), &scaffolds)
	scaffolds.Auth.Collection["name"], scaffolds.Auth.AuthCollection["name"] = "crew", "crew"
	var preview struct {
		Document    json.RawMessage `json:"document"`
		BaseVersion int64           `json:"baseVersion"`
		Checksum    string          `json:"checksum"`
	}
	document := map[string]any{"format": "pgbase.schema/v1", "collections": []any{scaffolds.Auth.Collection}, "authCollections": []any{scaffolds.Auth.AuthCollection}}
	decodePublicUsersJSON(t, send(http.MethodPost, "/api/admin/schema/import/preview", document, http.StatusOK), &preview)
	assertPublicUsersStatus(t, publicUsersRequest(t, app, http.MethodPost, "/api/admin/schema/import/apply", map[string]any{"document": preview.Document, "baseVersion": preview.BaseVersion, "checksum": preview.Checksum}, "", cookie,
		map[string]string{"X-PgBase-CSRF": csrf, "PgBase-Operation-ID": uuid.NewString(), "Idempotency-Key": "designer-auth-import-0001"}), http.StatusOK)

	apply := func(change map[string]any) {
		t.Helper()
		var plan struct {
			BaseVersion int64  `json:"baseVersion"`
			Checksum    string `json:"checksum"`
		}
		decodePublicUsersJSON(t, send(http.MethodPost, "/api/admin/collections/plan", change, http.StatusOK), &plan)
		change["baseVersion"], change["checksum"] = plan.BaseVersion, plan.Checksum
		send(http.MethodPost, "/api/admin/collections/apply", change, http.StatusOK)
	}
	// update replaces the field list: the stored fields (with ids) plus edit's changes.
	update := func(name string, edit func([]map[string]any) []map[string]any) {
		t.Helper()
		var collection struct {
			ID     uuid.UUID        `json:"id"`
			Fields []map[string]any `json:"fields"`
		}
		decodePublicUsersJSON(t, send(http.MethodGet, "/api/admin/collections/"+name, nil, http.StatusOK), &collection)
		fields := []map[string]any{}
		for _, field := range collection.Fields {
			input := map[string]any{}
			for _, key := range []string{"id", "name", "type", "required", "unique", "indexed", "default", "options", "hidden", "presentable", "help", "system"} {
				if value, ok := field[key]; ok {
					input[key] = value
				}
			}
			fields = append(fields, input)
		}
		apply(map[string]any{"operation": "update", "name": name, "collectionId": collection.ID, "fields": edit(fields)})
	}
	realm := func(name string) (id string, fields []string) {
		t.Helper()
		var list struct {
			Items []struct {
				ID     string `json:"id"`
				Name   string `json:"name"`
				Fields []struct {
					Name   string `json:"name"`
					System bool   `json:"system"`
				} `json:"fields"`
			} `json:"items"`
		}
		decodePublicUsersJSON(t, send(http.MethodGet, "/api/admin/auth-collections", nil, http.StatusOK), &list)
		for _, item := range list.Items {
			if item.Name == name {
				for _, field := range item.Fields {
					if !field.System {
						fields = append(fields, field.Name)
					}
				}
				return item.ID, fields
			}
		}
		return "", nil
	}

	update("crew", func(fields []map[string]any) []map[string]any {
		return append(fields, map[string]any{"name": "nickname", "type": "text"})
	})
	if _, fields := realm("crew"); len(fields) != 1 || fields[0] != "nickname" {
		t.Fatalf("realm fields after add = %v", fields)
	}
	id, _ := realm("crew")
	send(http.MethodPost, "/api/admin/auth-collections/"+id+"/records", map[string]any{"email": "nick@example.test", "password": publicUserTwoPass, "fields": map[string]any{"nickname": "Nick"}}, http.StatusCreated)

	update("crew", func(fields []map[string]any) []map[string]any {
		for _, field := range fields {
			if field["name"] == "nickname" {
				field["name"] = "alias"
			}
		}
		return fields
	})
	var records struct {
		Items []struct {
			Email  string         `json:"email"`
			Fields map[string]any `json:"fields"`
		} `json:"items"`
	}
	decodePublicUsersJSON(t, send(http.MethodGet, "/api/admin/auth-collections/"+id+"/records", nil, http.StatusOK), &records)
	if _, fields := realm("crew"); len(fields) != 1 || fields[0] != "alias" || len(records.Items) == 0 || records.Items[len(records.Items)-1].Fields["alias"] != "Nick" {
		t.Fatalf("after field rename fields=%v records=%+v", fields, records.Items)
	}

	update("crew", func(fields []map[string]any) []map[string]any {
		kept := []map[string]any{}
		for _, field := range fields {
			if field["name"] != "alias" {
				kept = append(kept, field)
			}
		}
		return kept
	})
	if _, fields := realm("crew"); len(fields) != 0 {
		t.Fatalf("realm fields after drop = %v", fields)
	}

	var catalog struct {
		ID uuid.UUID `json:"id"`
	}
	decodePublicUsersJSON(t, send(http.MethodGet, "/api/admin/collections/crew", nil, http.StatusOK), &catalog)
	apply(map[string]any{"operation": "update", "name": "staff", "collectionId": catalog.ID})
	if renamed, _ := realm("staff"); renamed != id {
		t.Fatalf("renamed realm id = %q, want %q", renamed, id)
	}
	send(http.MethodPost, "/api/admin/collections/plan", map[string]any{"operation": "delete", "name": "staff"}, http.StatusUnprocessableEntity)
	send(http.MethodDelete, "/api/admin/auth-collections/"+id, nil, http.StatusConflict)
}
