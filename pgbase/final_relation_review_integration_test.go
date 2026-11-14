//go:build integration

package pgbase

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

func TestNativeDeleteEnforcesAuthProfileRelations(t *testing.T) {
	for _, tc := range []struct {
		name, action, operation string
		child, denied           bool
	}{
		{name: "restrict", action: "restrict"},
		{name: "set_null", action: "set_null"},
		{name: "cascade_multihop", action: "cascade"},
		{name: "cascade_denied_rolls_back", action: "cascade", denied: true},
		{name: "native_cascade_child_restrict", action: "restrict", child: true},
		{name: "truncate_restrict", action: "restrict", operation: "truncate"},
		{name: "batch_restrict", action: "restrict", operation: "batch"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fixture := newPublicUsersApp(t, true)
			cookie, csrf := setupPublicUsersAdministrator(t, fixture)
			headers := map[string]string{"X-PgBase-CSRF": csrf}
			createPublicUsersCollection(t, fixture, cookie, csrf, "review_targets")
			createNative := func(collection string, body map[string]any) string {
				response := publicUsersRequest(t, fixture, http.MethodPost, "/api/v1/collections/"+collection+"/records", body, "", cookie, headers)
				assertPublicUsersStatus(t, response, http.StatusCreated)
				var record struct {
					ID string `json:"id"`
				}
				decodePublicUsersJSON(t, response, &record)
				return record.ID
			}
			rootID := createNative("review_targets", map[string]any{"title": "referenced"})
			targetID, targetCollection := rootID, "review_targets"
			relation := func(name, target, action string) map[string]any {
				return map[string]any{"name": name, "type": "relation", "relation": map[string]any{"targetCollection": target, "cardinality": "single", "minSelect": 0, "maxSelect": 1, "onDelete": action}}
			}
			if tc.child {
				change := map[string]any{"operation": "create", "name": "review_children", "fields": []any{relation("parent", "review_targets", "cascade")}}
				planAndApplySchemaChange(t, fixture, cookie, csrf, change, http.StatusCreated, nil)
				targetID, targetCollection = createNative("review_children", map[string]any{"parent": rootID}), "review_children"
			}
			realm := uuid.NewString()
			createdRealm := publicUsersRequest(t, fixture, http.MethodPost, "/api/admin/auth-collections", map[string]any{
				"id": realm, "name": "review_members", "registrationEnabled": true,
				"fields": []any{relation("target", targetCollection, tc.action), relation("parent", "review_members", "cascade")},
				"rules":  map[string]any{"delete": "false"},
			}, "", cookie, headers)
			assertPublicUsersStatus(t, createdRealm, http.StatusCreated)
			createProfile := func(email string, fields map[string]any) string {
				response := publicUsersRequest(t, fixture, http.MethodPost, "/api/admin/auth-collections/"+realm+"/records", map[string]any{"email": email, "password": publicUserOnePass, "fields": fields}, "", cookie, headers)
				assertPublicUsersStatus(t, response, http.StatusCreated)
				var id string
				if err := fixture.app.pool.QueryRow(t.Context(), `select id::text from pgbase_meta.users where email=$1`, email).Scan(&id); err != nil {
					t.Fatal(err)
				}
				return id
			}
			profileID := createProfile("reference@example.test", map[string]any{"target": targetID})
			if tc.action == "cascade" {
				createProfile("descendant@example.test", map[string]any{"parent": profileID})
			}
			path := "/api/v1/collections/review_targets/records/" + rootID
			var deletedStatus int
			if tc.denied {
				user := registerPublicUser(t, fixture, "actor@example.test", publicUserTwoPass)
				assertPublicUsersStatus(t, publicUsersRequest(t, fixture, http.MethodPut, "/api/admin/collections/review_targets/user-access", map[string]any{"enabled": true}, "", cookie, headers), http.StatusOK)
				rules := map[string]any{"list": map[string]any{"op": "eq", "field": "title", "value": "referenced"}, "view": map[string]any{"op": "eq", "field": "title", "value": "referenced"}, "delete": map[string]any{"op": "eq", "field": "title", "value": "referenced"}, "create": map[string]any{"op": "owner"}, "update": map[string]any{"op": "owner"}}
				assertPublicUsersStatus(t, publicUsersRequest(t, fixture, http.MethodPut, "/api/admin/collections/review_targets/rules", map[string]any{"rules": rules}, "", cookie, headers), http.StatusOK)
				assertPublicUsersStatus(t, publicUsersRequest(t, fixture, http.MethodGet, "/api/v1/user/collections/review_targets/records/"+rootID, nil, user.Token, nil, nil), http.StatusOK)
				response := publicUsersRequest(t, fixture, http.MethodDelete, "/api/v1/user/collections/review_targets/records/"+rootID, nil, user.Token, nil, nil)
				deletedStatus = response.Code
				if response.Code != http.StatusNotFound {
					t.Fatalf("denied cascade status=%d body=%s", response.Code, response.Body.String())
				}
			} else {
				switch tc.operation {
				case "truncate":
					response := publicUsersRequest(t, fixture, http.MethodPost, "/api/admin/collections/review_targets/truncate", map[string]any{"name": "review_targets"}, "", cookie, headers)
					deletedStatus = response.Code
				case "batch":
					response := publicUsersRequest(t, fixture, http.MethodPost, "/api/v1/collections/batch", map[string]any{"requests": []any{map[string]any{"method": "DELETE", "url": path}}}, "", cookie, headers)
					deletedStatus = response.Code
				default:
					response := publicUsersRequest(t, fixture, http.MethodDelete, path, nil, "", cookie, headers)
					deletedStatus = response.Code
				}
				want := http.StatusNoContent
				if tc.action == "restrict" {
					want = http.StatusConflict
				}
				if deletedStatus != want {
					t.Fatalf("delete status=%d want=%d", deletedStatus, want)
				}
			}
			var nativeExists bool
			var profileCount, referenceCount int
			if err := fixture.app.pool.QueryRow(t.Context(), `select exists(select 1 from pgbase_meta.record_revisions where record_id=$1::uuid)`, rootID).Scan(&nativeExists); err != nil {
				t.Fatal(err)
			}
			if err := fixture.app.pool.QueryRow(t.Context(), `select count(*),count(*) filter(where attributes->>'target'=$2) from pgbase_meta.user_auth_profiles p join pgbase_meta.users u on u.id=p.user_id where u.auth_collection_id=$1`, realm, targetID).Scan(&profileCount, &referenceCount); err != nil {
				t.Fatal(err)
			}
			retained := tc.action == "restrict" || tc.denied
			wantProfiles, wantReferences := 1, 0
			if retained {
				wantReferences = 1
			}
			if tc.action == "cascade" {
				wantProfiles = 0
				if tc.denied {
					wantProfiles = 2
				}
			}
			if nativeExists != retained || profileCount != wantProfiles || referenceCount != wantReferences {
				t.Fatalf("native exists=%v profiles=%d refs=%d; want %v/%d/%d", nativeExists, profileCount, referenceCount, retained, wantProfiles, wantReferences)
			}
		})
	}
}

func TestAuthRelationPublicationWaitsForNativeDelete(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	fixture := newPublicUsersApp(t, true)
	cookie, csrf := setupPublicUsersAdministrator(t, fixture)
	headers := map[string]string{"X-PgBase-CSRF": csrf}
	createPublicUsersCollection(t, fixture, cookie, csrf, "review_native_race")
	response := publicUsersRequest(t, fixture, http.MethodPost, "/api/v1/collections/review_native_race/records", map[string]any{"title": "target"}, "", cookie, headers)
	assertPublicUsersStatus(t, response, http.StatusCreated)
	var target struct {
		ID string `json:"id"`
	}
	decodePublicUsersJSON(t, response, &target)
	realm := uuid.NewString()
	relation := map[string]any{"name": "target", "type": "relation", "relation": map[string]any{"targetCollection": "review_native_race", "cardinality": "single", "minSelect": 0, "maxSelect": 1, "onDelete": "restrict"}}
	response = publicUsersRequest(t, fixture, http.MethodPost, "/api/admin/auth-collections", map[string]any{"id": realm, "name": "review_native_race_members", "fields": []any{relation}}, "", cookie, headers)
	assertPublicUsersStatus(t, response, http.StatusCreated)
	var table string
	if err := fixture.app.pool.QueryRow(ctx, `select table_name from pgbase_meta.collections where name='review_native_race'`).Scan(&table); err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.app.pool.Exec(ctx, `create function pgbase_meta.pause_native_delete() returns trigger language plpgsql as $$ begin perform pg_advisory_xact_lock(91386012); return old; end $$`); err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.app.pool.Exec(ctx, "create trigger pause_native_delete before delete on "+pgx.Identifier{"app", table}.Sanitize()+" for each row execute function pgbase_meta.pause_native_delete()"); err != nil {
		t.Fatal(err)
	}
	blocker, err := fixture.app.pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer blocker.Rollback(context.Background())
	if _, err := blocker.Exec(ctx, `select pg_advisory_xact_lock(91386012)`); err != nil {
		t.Fatal(err)
	}
	deleted := make(chan *httptest.ResponseRecorder, 1)
	go func() {
		deleted <- publicUsersRequest(t, fixture, http.MethodDelete, "/api/v1/collections/review_native_race/records/"+target.ID, nil, "", cookie, headers)
	}()
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	waitForLock := func(query string, completed <-chan *httptest.ResponseRecorder) {
		t.Helper()
		for {
			select {
			case response := <-completed:
				t.Fatalf("request escaped target lock: status=%d body=%s", response.Code, response.Body.String())
			default:
			}
			var waiting bool
			if err := fixture.app.pool.QueryRow(ctx, query).Scan(&waiting); err != nil {
				t.Fatal(err)
			}
			if waiting {
				return
			}
			select {
			case <-ctx.Done():
				t.Fatal(ctx.Err())
			case <-ticker.C:
			}
		}
	}
	waitForLock(`select exists(select 1 from pg_stat_activity where datname=current_database() and wait_event='advisory' and query like 'DELETE FROM %')`, deleted)
	created := make(chan *httptest.ResponseRecorder, 1)
	go func() {
		created <- publicUsersRequest(t, fixture, http.MethodPost, "/api/admin/auth-collections/"+realm+"/records", map[string]any{"email": "native-race-reference@example.test", "password": publicUserOnePass, "fields": map[string]any{"target": target.ID}}, "", cookie, headers)
	}()
	waitForLock(`select exists(select 1 from pg_stat_activity where datname=current_database() and wait_event_type='Lock' and query like 'select id from % for key share')`, created)
	if err := blocker.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	select {
	case response := <-deleted:
		assertPublicUsersStatus(t, response, http.StatusNoContent)
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	select {
	case response := <-created:
		assertPublicUsersStatus(t, response, http.StatusBadRequest)
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	var references int
	if err := fixture.app.pool.QueryRow(ctx, `select count(*) from pgbase_meta.user_auth_profiles where attributes->>'target'=$1`, target.ID).Scan(&references); err != nil {
		t.Fatal(err)
	}
	if references != 0 {
		t.Fatalf("deleted native target retained %d auth-profile references", references)
	}
}
