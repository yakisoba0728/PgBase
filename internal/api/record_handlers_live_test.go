package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/webisnoteasy/PgBase/internal/database"
	"github.com/webisnoteasy/PgBase/internal/migrate"
	"github.com/webisnoteasy/PgBase/internal/query"
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

	records := query.NewService(pool, schemas, []byte("0123456789abcdef0123456789abcdef"))
	handler := NewRouter(recordTestDependencies(&fakeAdminAuth{authenticateAdmin: administrator}, records))
	create := authenticatedRecordRequest(http.MethodPost, "/api/v1/collections/nullable_api_records/records",
		`{"title":"kept","active":false,"note":"","metadata":null}`, true)
	createdResponse := httptest.NewRecorder()
	handler.ServeHTTP(createdResponse, create)
	if createdResponse.Code != http.StatusCreated {
		t.Fatalf("create status = %d body = %s", createdResponse.Code, createdResponse.Body.String())
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
	nullableUpdate := authenticatedRecordRequest(http.MethodPatch, "/api/v1/collections/nullable_api_records/records/"+id,
		`{"active":null,"note":null}`, true)
	nullableResponse := httptest.NewRecorder()
	handler.ServeHTTP(nullableResponse, nullableUpdate)
	if nullableResponse.Code != http.StatusOK {
		t.Fatalf("nullable update status = %d body = %s", nullableResponse.Code, nullableResponse.Body.String())
	}
	var nulled map[string]json.RawMessage
	if err := json.Unmarshal(nullableResponse.Body.Bytes(), &nulled); err != nil {
		t.Fatal(err)
	}
	if string(nulled["active"]) != "null" || string(nulled["note"]) != "null" || string(nulled["title"]) != `"kept"` {
		t.Fatalf("nullable update response = %s", nullableResponse.Body.String())
	}

	update := authenticatedRecordRequest(http.MethodPatch, "/api/v1/collections/nullable_api_records/records/"+id,
		`{"title":null}`, true)
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
}
