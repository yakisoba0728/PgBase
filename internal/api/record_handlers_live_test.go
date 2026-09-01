//go:build integration

package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/webisnoteasy/PgBase/internal/database"
	"github.com/webisnoteasy/PgBase/internal/migrate"
	"github.com/webisnoteasy/PgBase/internal/recordops"
	"github.com/webisnoteasy/PgBase/internal/schema"
	"github.com/webisnoteasy/PgBase/internal/testutil"
)

func TestRecordRoutesPersistNullableValuesThroughLiveService(t *testing.T) {
	ctx := context.Background()
	pool, err := database.Open(ctx, testutil.NewDatabase(t), 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	if err := migrate.New(pool, nil).Up(ctx); err != nil {
		t.Fatal(err)
	}
	administrator := validSession().Administrator
	if _, err := pool.Exec(ctx, `insert into pgbase_meta.admins
		(id, email, password_hash, active, created_at, updated_at)
		values ($1, $2, 'test', true, current_timestamp, current_timestamp)`, administrator.ID, administrator.Email); err != nil {
		t.Fatal(err)
	}

	schemas := schema.NewService(pool, &schema.Cache{}, schema.Planner{})
	definition := schema.CreateCollectionInput{Name: "nullable_api_records", Fields: []schema.FieldInput{
		{Name: "title", Type: schema.TypeText, Required: true},
		{Name: "active", Type: schema.TypeBoolean},
		{Name: "note", Type: schema.TypeText},
		{Name: "metadata", Type: schema.TypeJSONB, Required: true},
	}}
	plan, err := schemas.PlanCreate(ctx, definition)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := schemas.ApplyCreate(ctx, definition, plan.Checksum, plan.BaseVersion, administrator.ID); err != nil {
		t.Fatal(err)
	}

	records := recordops.NewService(pool, schemas, []byte("0123456789abcdef0123456789abcdef"))
	handler := NewRouter(recordTestDependencies(&fakeAdminAuth{authenticateAdmin: administrator}, records))
	create := authenticatedRecordRequest(http.MethodPost, "/api/v1/collections/nullable_api_records/records",
		`{"title":"kept","active":false,"note":"","metadata":null}`, true)
	createdResponse := httptest.NewRecorder()
	handler.ServeHTTP(createdResponse, create)
	if createdResponse.Code != http.StatusCreated {
		t.Fatalf("create status = %d body = %s", createdResponse.Code, createdResponse.Body.String())
	}
	createdETag := createdResponse.Header().Get("ETag")
	if createdETag == "" || createdResponse.Header().Get("Idempotency-Replayed") != "false" ||
		createdResponse.Header().Get("PgBase-Compatibility") != string(recordops.CompatibilityMissingIdempotencyKey) ||
		createdResponse.Header().Get("Deprecation") != "" {
		t.Fatalf("create headers = %v", createdResponse.Header())
	}
	var created map[string]json.RawMessage
	if err := json.Unmarshal(createdResponse.Body.Bytes(), &created); err != nil {
		t.Fatal(err)
	}
	if string(created["active"]) != "false" || string(created["note"]) != `""` || string(created["metadata"]) != "null" {
		t.Fatalf("created value response = %s", createdResponse.Body.String())
	}

	var id string
	if err := json.Unmarshal(created["id"], &id); err != nil {
		t.Fatal(err)
	}
	if createdResponse.Header().Get("Location") != "/api/v1/collections/nullable_api_records/records/"+id {
		t.Fatalf("create Location = %q", createdResponse.Header().Get("Location"))
	}
	nullableUpdate := authenticatedRecordRequest(http.MethodPatch, "/api/v1/collections/nullable_api_records/records/"+id,
		`{"active":null,"note":null}`, true)
	nullableUpdate.Header.Set("If-Match", createdETag)
	nullableResponse := httptest.NewRecorder()
	handler.ServeHTTP(nullableResponse, nullableUpdate)
	if nullableResponse.Code != http.StatusOK {
		t.Fatalf("nullable update status = %d body = %s", nullableResponse.Code, nullableResponse.Body.String())
	}
	currentETag := nullableResponse.Header().Get("ETag")
	if currentETag == "" || currentETag == createdETag || nullableResponse.Header().Get("PgBase-Compatibility") != "" {
		t.Fatalf("nullable update headers = %v", nullableResponse.Header())
	}
	var nulled map[string]json.RawMessage
	if err := json.Unmarshal(nullableResponse.Body.Bytes(), &nulled); err != nil {
		t.Fatal(err)
	}
	if string(nulled["active"]) != "null" || string(nulled["note"]) != "null" || string(nulled["title"]) != `"kept"` {
		t.Fatalf("nullable update response = %s", nullableResponse.Body.String())
	}

	stale := authenticatedRecordRequest(http.MethodPatch, "/api/v1/collections/nullable_api_records/records/"+id,
		`{"title":"must-not-win"}`, true)
	stale.Header.Set("If-Match", createdETag)
	staleResponse := httptest.NewRecorder()
	handler.ServeHTTP(staleResponse, stale)
	if staleResponse.Code != http.StatusPreconditionFailed || staleResponse.Header().Get("ETag") != currentETag {
		t.Fatalf("stale update status = %d ETag = %q body = %s", staleResponse.Code, staleResponse.Header().Get("ETag"), staleResponse.Body.String())
	}
	assertErrorCode(t, staleResponse.Body.Bytes(), "precondition_failed")

	staleInvalid := authenticatedRecordRequest(http.MethodPatch, "/api/v1/collections/nullable_api_records/records/"+id,
		`{"unknown":"value"}`, true)
	staleInvalid.Header.Set("If-Match", createdETag)
	staleInvalidResponse := httptest.NewRecorder()
	handler.ServeHTTP(staleInvalidResponse, staleInvalid)
	if staleInvalidResponse.Code != http.StatusUnprocessableEntity || staleInvalidResponse.Header().Get("ETag") != "" {
		t.Fatalf("stale invalid update status = %d ETag = %q body = %s",
			staleInvalidResponse.Code, staleInvalidResponse.Header().Get("ETag"), staleInvalidResponse.Body.String())
	}
	var staleInvalidFailure struct {
		Error struct {
			Code    string              `json:"code"`
			Details map[string][]string `json:"details"`
		} `json:"error"`
	}
	if err := json.Unmarshal(staleInvalidResponse.Body.Bytes(), &staleInvalidFailure); err != nil {
		t.Fatal(err)
	}
	if staleInvalidFailure.Error.Code != "validation_failed" || len(staleInvalidFailure.Error.Details["unknown"]) != 1 ||
		staleInvalidFailure.Error.Details["unknown"][0] != "unknown_field" {
		t.Fatalf("stale invalid update failure = %#v", staleInvalidFailure.Error)
	}

	missing := authenticatedRecordRequest(http.MethodPatch,
		"/api/v1/collections/nullable_api_records/records/019551d0-e9c0-7000-8000-000000000404",
		`{"title":"absent"}`, true)
	missing.Header.Set("If-Match", createdETag)
	missingResponse := httptest.NewRecorder()
	handler.ServeHTTP(missingResponse, missing)
	if missingResponse.Code != http.StatusNotFound || missingResponse.Header().Get("ETag") != "" {
		t.Fatalf("missing update status = %d ETag = %q body = %s",
			missingResponse.Code, missingResponse.Header().Get("ETag"), missingResponse.Body.String())
	}
	assertErrorCode(t, missingResponse.Body.Bytes(), "not_found")

	for _, method := range []string{http.MethodPatch, http.MethodDelete} {
		body := ""
		if method == http.MethodPatch {
			body = `{"title":"absent"}`
		}
		missingWithMalformedPrecondition := authenticatedRecordRequest(method,
			"/api/v1/collections/nullable_api_records/records/019551d0-e9c0-7000-8000-000000000404",
			body, true)
		missingWithMalformedPrecondition.Header.Set("If-Match", "not-an-entity-tag")
		malformedResponse := httptest.NewRecorder()
		handler.ServeHTTP(malformedResponse, missingWithMalformedPrecondition)
		if malformedResponse.Code != http.StatusNotFound || malformedResponse.Header().Get("ETag") != "" {
			t.Fatalf("missing %s with malformed precondition status = %d ETag = %q body = %s",
				method, malformedResponse.Code, malformedResponse.Header().Get("ETag"), malformedResponse.Body.String())
		}
		assertErrorCode(t, malformedResponse.Body.Bytes(), "not_found")
	}

	update := authenticatedRecordRequest(http.MethodPatch, "/api/v1/collections/nullable_api_records/records/"+id,
		`{"title":null}`, true)
	update.Header.Set("If-Match", currentETag)
	updatedResponse := httptest.NewRecorder()
	handler.ServeHTTP(updatedResponse, update)
	if updatedResponse.Code != http.StatusUnprocessableEntity {
		t.Fatalf("required-null status = %d body = %s", updatedResponse.Code, updatedResponse.Body.String())
	}
	var failure struct {
		Error struct {
			Code    string              `json:"code"`
			Details map[string][]string `json:"details"`
		} `json:"error"`
	}
	if err := json.Unmarshal(updatedResponse.Body.Bytes(), &failure); err != nil {
		t.Fatal(err)
	}
	if failure.Error.Code != "validation_failed" || len(failure.Error.Details["title"]) != 1 || failure.Error.Details["title"][0] != "required" {
		t.Fatalf("required-null failure = %#v", failure.Error)
	}

	const idempotencyKey = "nullable-create-key-2026"
	createIdempotent := func(body string) *httptest.ResponseRecorder {
		t.Helper()
		request := authenticatedRecordRequest(http.MethodPost, "/api/v1/collections/nullable_api_records/records", body, true)
		request.Header.Set("Idempotency-Key", idempotencyKey)
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		return response
	}
	first := createIdempotent(`{"title":"replay","metadata":{}}`)
	replay := createIdempotent(`{"title":"replay","metadata":{}}`)
	if first.Code != http.StatusCreated || replay.Code != http.StatusCreated ||
		first.Header().Get("Idempotency-Replayed") != "false" || replay.Header().Get("Idempotency-Replayed") != "true" ||
		first.Header().Get("ETag") == "" || replay.Header().Get("ETag") != first.Header().Get("ETag") ||
		replay.Header().Get("Location") != first.Header().Get("Location") || replay.Body.String() != first.Body.String() ||
		first.Header().Get("PgBase-Compatibility") != "" || replay.Header().Get("PgBase-Compatibility") != "" {
		t.Fatalf("idempotent create first=%d/%v/%s replay=%d/%v/%s",
			first.Code, first.Header(), first.Body.String(), replay.Code, replay.Header(), replay.Body.String())
	}
	conflict := createIdempotent(`{"title":"different","metadata":{}}`)
	if conflict.Code != http.StatusConflict {
		t.Fatalf("idempotency conflict status = %d body = %s", conflict.Code, conflict.Body.String())
	}
	assertErrorCode(t, conflict.Body.Bytes(), "idempotency_conflict")
	var replayCount int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM app.nullable_api_records WHERE title = 'replay'`).Scan(&replayCount); err != nil {
		t.Fatal(err)
	}
	if replayCount != 1 {
		t.Fatalf("idempotent create persisted %d replay records, want 1", replayCount)
	}
}

func TestRecordRoutesForwardExpandIsFlatOrderedAndRootValidated(t *testing.T) {
	ctx := context.Background()
	pool, err := database.Open(ctx, testutil.NewDatabase(t), 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	if err := migrate.New(pool, nil).Up(ctx); err != nil {
		t.Fatal(err)
	}
	administrator := validSession().Administrator
	if _, err := pool.Exec(ctx, `insert into pgbase_meta.admins
		(id, email, password_hash, active, created_at, updated_at)
		values ($1, $2, 'test', true, current_timestamp, current_timestamp)`, administrator.ID, administrator.Email); err != nil {
		t.Fatal(err)
	}

	schemas := schema.NewService(pool, &schema.Cache{}, schema.Planner{})
	apply := func(input schema.CreateCollectionInput) {
		t.Helper()
		plan, err := schemas.PlanCreate(ctx, input)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := schemas.ApplyCreate(ctx, input, plan.Checksum, plan.BaseVersion, administrator.ID); err != nil {
			t.Fatal(err)
		}
	}
	apply(schema.CreateCollectionInput{Name: "api_expand_people", Fields: []schema.FieldInput{
		{Name: "name", Type: schema.TypeText, Required: true, Indexed: true},
	}})
	maximumTags := 4
	apply(schema.CreateCollectionInput{Name: "api_expand_articles", Fields: []schema.FieldInput{
		{Name: "title", Type: schema.TypeText, Required: true},
		{Name: "owner", Type: schema.TypeRelation, Relation: &schema.RelationInput{
			TargetCollection: "api_expand_people", Cardinality: schema.RelationSingle, OnDelete: schema.RelationRestrict,
		}},
		{Name: "tags", Type: schema.TypeRelation, Relation: &schema.RelationInput{
			TargetCollection: "api_expand_people", Cardinality: schema.RelationMultiple,
			MaxSelect: &maximumTags, OnDelete: schema.RelationRestrict,
		}},
	}})

	const cursorSecret = "0123456789abcdef0123456789abcdef"
	records := recordops.NewService(pool, schemas, []byte(cursorSecret))
	principal := recordops.Principal{Kind: recordops.PrincipalAdministrator, ID: administrator.ID}
	createPerson := func(requestID, name string) recordops.MutationResult {
		t.Helper()
		created, err := records.Create(ctx, recordops.CreateRequest{
			Principal: principal, RequestID: requestID, Collection: "api_expand_people",
			Input: map[string]json.RawMessage{"name": json.RawMessage(`"` + name + `"`)},
		})
		if err != nil {
			t.Fatal(err)
		}
		return created
	}
	first := createPerson("api-expand-person-first", "first")
	second := createPerson("api-expand-person-second", "second")
	firstID := first.Record.Data["id"].(string)
	secondID := second.Record.Data["id"].(string)
	ownerJSON, _ := json.Marshal(firstID)
	tagsJSON, _ := json.Marshal([]string{secondID, firstID})
	var articleIDs []string
	for index, title := range []string{"one", "two"} {
		created, err := records.Create(ctx, recordops.CreateRequest{
			Principal: principal, RequestID: "api-expand-article-" + title, Collection: "api_expand_articles",
			Input: map[string]json.RawMessage{
				"title": json.RawMessage(`"` + title + `"`), "owner": ownerJSON, "tags": tagsJSON,
			},
		})
		if err != nil {
			t.Fatalf("create article %d: %v", index, err)
		}
		articleIDs = append(articleIDs, created.Record.Data["id"].(string))
	}

	handler := NewRouter(recordTestDependencies(&fakeAdminAuth{authenticateAdmin: administrator}, records))
	serve := func(path string) *httptest.ResponseRecorder {
		t.Helper()
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, authenticatedRecordRequest(http.MethodGet, path, "", true))
		return response
	}
	raw := serve("/api/v1/collections/api_expand_articles/records/" + articleIDs[0])
	expanded := serve("/api/v1/collections/api_expand_articles/records/" + articleIDs[0] + "?expand=owner%2Ctags")
	if raw.Code != http.StatusOK || expanded.Code != http.StatusOK || raw.Header().Get("ETag") == "" ||
		expanded.Header().Get("ETag") != raw.Header().Get("ETag") {
		t.Fatalf("raw=%d/%v expanded=%d/%v body=%s", raw.Code, raw.Header(), expanded.Code, expanded.Header(), expanded.Body.String())
	}
	var body map[string]any
	if err := json.Unmarshal(expanded.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if _, wrapped := body["data"]; wrapped || body["owner"] != firstID {
		t.Fatalf("expanded response was wrapped or lost raw owner: %#v", body)
	}
	tags, ok := body["tags"].([]any)
	if !ok || len(tags) != 2 || tags[0] != secondID || tags[1] != firstID {
		t.Fatalf("raw relation order = %#v", body["tags"])
	}
	expand, ok := body["expand"].(map[string]any)
	if !ok || expand["owner"].(map[string]any)["name"] != "first" {
		t.Fatalf("expanded owner = %#v", body["expand"])
	}
	expandedTags, ok := expand["tags"].([]any)
	if !ok || len(expandedTags) != 2 || expandedTags[0].(map[string]any)["id"] != secondID ||
		expandedTags[1].(map[string]any)["id"] != firstID {
		t.Fatalf("expanded tag order = %#v", expand["tags"])
	}

	if _, err := records.Update(ctx, recordops.UpdateRequest{
		Principal: principal, RequestID: "api-expand-target-update", Collection: "api_expand_people",
		ID: uuid.MustParse(firstID), IfMatch: recordops.EntityTagSet{Present: true, Values: []string{first.Record.ETag}},
		Input: map[string]json.RawMessage{"name": json.RawMessage(`"changed"`)},
	}); err != nil {
		t.Fatal(err)
	}
	afterTargetUpdate := serve("/api/v1/collections/api_expand_articles/records/" + articleIDs[0] + "?expand=owner")
	var updatedBody map[string]any
	if afterTargetUpdate.Code != http.StatusOK || afterTargetUpdate.Header().Get("ETag") != raw.Header().Get("ETag") {
		t.Fatalf("target-only update changed root validator: %d/%v", afterTargetUpdate.Code, afterTargetUpdate.Header())
	}
	if err := json.Unmarshal(afterTargetUpdate.Body.Bytes(), &updatedBody); err != nil {
		t.Fatal(err)
	}
	if updatedBody["expand"].(map[string]any)["owner"].(map[string]any)["name"] != "changed" {
		t.Fatalf("expanded target did not refresh: %#v", updatedBody["expand"])
	}

	firstPage := serve("/api/v1/collections/api_expand_articles/records?limit=1&expand=owner%2Ctags")
	var page struct {
		Items      []map[string]any `json:"items"`
		NextCursor string           `json:"nextCursor"`
	}
	if firstPage.Code != http.StatusOK {
		t.Fatalf("expanded list status=%d body=%s", firstPage.Code, firstPage.Body.String())
	}
	if err := json.Unmarshal(firstPage.Body.Bytes(), &page); err != nil {
		t.Fatal(err)
	}
	if len(page.Items) != 1 || page.NextCursor == "" || page.Items[0]["expand"] == nil {
		t.Fatalf("expanded first page = %#v", page)
	}
	secondPage := serve("/api/v1/collections/api_expand_articles/records?limit=1&expand=owner%2Ctags&cursor=" + url.QueryEscape(page.NextCursor))
	var continuation struct {
		Items      []map[string]any `json:"items"`
		NextCursor string           `json:"nextCursor"`
	}
	if secondPage.Code != http.StatusOK {
		t.Fatalf("expanded continuation status=%d body=%s", secondPage.Code, secondPage.Body.String())
	}
	if err := json.Unmarshal(secondPage.Body.Bytes(), &continuation); err != nil {
		t.Fatal(err)
	}
	if len(continuation.Items) != 1 || continuation.NextCursor != "" ||
		continuation.Items[0]["id"] == page.Items[0]["id"] || continuation.Items[0]["expand"] == nil {
		t.Fatalf("expanded continuation = %#v", continuation)
	}
	thirdOwnerJSON, _ := json.Marshal(secondID)
	thirdTagsJSON, _ := json.Marshal([]string{firstID})
	third, err := records.Create(ctx, recordops.CreateRequest{
		Principal: principal, RequestID: "api-relation-query-article", Collection: "api_expand_articles",
		Input: map[string]json.RawMessage{
			"title": json.RawMessage(`"relation-query"`), "owner": thirdOwnerJSON, "tags": thirdTagsJSON,
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	relationFilter := "tags = '" + firstID + "' && owner = '" + secondID + "'"
	relationQuery := serve("/api/v1/collections/api_expand_articles/records?limit=10&filter=" +
		url.QueryEscape(relationFilter) + "&sort=owner.name&expand=owner")
	var relationPage struct {
		Items []map[string]any `json:"items"`
	}
	if relationQuery.Code != http.StatusOK {
		t.Fatalf("relation filter/sort status=%d body=%s", relationQuery.Code, relationQuery.Body.String())
	}
	if err := json.Unmarshal(relationQuery.Body.Bytes(), &relationPage); err != nil {
		t.Fatal(err)
	}
	if len(relationPage.Items) != 1 || relationPage.Items[0]["id"] != third.Record.Data["id"] ||
		relationPage.Items[0]["expand"].(map[string]any)["owner"].(map[string]any)["id"] != secondID {
		t.Fatalf("relation filter/sort page = %#v", relationPage)
	}
	invalid := serve("/api/v1/collections/api_expand_articles/records/" + articleIDs[0] + "?expand=owner.missing")
	if invalid.Code != http.StatusUnprocessableEntity {
		t.Fatalf("nested forward expand status=%d body=%s", invalid.Code, invalid.Body.String())
	}
	assertErrorCode(t, invalid.Body.Bytes(), "validation_failed")
}
