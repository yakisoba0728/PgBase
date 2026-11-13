//go:build integration

package api

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/yakisoba0728/PgBase/internal/database"
	"github.com/yakisoba0728/PgBase/internal/migrate"
	"github.com/yakisoba0728/PgBase/internal/recordaccess"
	"github.com/yakisoba0728/PgBase/internal/schema"
	"github.com/yakisoba0728/PgBase/internal/testutil"
)

func TestInheritedViewPolicyHTTPRejectsIgnoredWrites(t *testing.T) {
	ctx := context.Background()
	pool, err := database.Open(ctx, testutil.NewDatabase(t), 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	if err := migrate.New(pool, nil).Up(ctx); err != nil {
		t.Fatal(err)
	}
	actor := uuid.New()
	if _, err := pool.Exec(ctx, `INSERT INTO pgbase_meta.admins(id,email,password_hash,active,created_at,updated_at) VALUES($1,'view-http@example.test','x',true,now(),now())`, actor); err != nil {
		t.Fatal(err)
	}
	schemas := schema.NewService(pool, nil, nil)
	for _, input := range []schema.CreateCollectionInput{{Name: "policy_http_source", Fields: []schema.FieldInput{{Name: "title", Type: schema.TypeText}}}, {Name: "policy_http_view", View: &schema.ViewInput{Source: "policy_http_source", Projection: []string{"title"}}}, {Name: "policy_http_sql", View: &schema.ViewInput{SQL: `SELECT id,title FROM app.policy_http_source`}}} {
		change := schema.NewCreateChange(input)
		plan, err := schemas.Preview(ctx, change)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := schemas.Apply(ctx, schema.LegacyApply(change, plan, actor)); err != nil {
			t.Fatal(err)
		}
	}
	dependencies := Dependencies{RecordRules: recordaccess.NewRuleService(pool, schemas), RecordAccess: recordaccess.NewService(pool)}
	var ruleHandler http.Handler
	for _, route := range ruleRoutes(dependencies) {
		if route.method == http.MethodPut {
			ruleHandler = route.handler
		}
	}
	gateHandler := http.HandlerFunc(userEndpoints{dependencies: dependencies}.access)
	for _, name := range []string{"policy_http_view", "policy_http_source", "policy_http_sql"} {
		for _, tc := range []struct {
			path, body string
			handler    http.Handler
		}{{"rules", `{"rules":{"list":{"op":"deny"},"view":{"op":"deny"},"create":{"op":"deny"},"update":{"op":"deny"},"delete":{"op":"deny"}}}`, ruleHandler}, {"user-access", `{"enabled":false}`, gateHandler}} {
			request := httptest.NewRequest(http.MethodPut, "/api/admin/collections/"+name+"/"+tc.path, strings.NewReader(tc.body))
			request.Header.Set("Content-Type", "application/json")
			request.SetPathValue("collection", name)
			response := httptest.NewRecorder()
			tc.handler.ServeHTTP(response, request)
			want := http.StatusOK
			if name == "policy_http_view" {
				want = http.StatusForbidden
			}
			if response.Code != want {
				t.Fatalf("%s/%s status=%d body=%s", name, tc.path, response.Code, response.Body.String())
			}
			if want == http.StatusForbidden && !strings.Contains(response.Body.String(), `"code":"access_denied"`) {
				t.Fatalf("wrong error: %s", response.Body.String())
			}
		}
	}
	var writes int
	if err := pool.QueryRow(ctx, `SELECT (SELECT count(*) FROM pgbase_meta.collection_rules WHERE collection_id=c.id)+(SELECT count(*) FROM pgbase_meta.collection_access WHERE collection_id=c.id) FROM pgbase_meta.collections c WHERE name='policy_http_view'`).Scan(&writes); err != nil || writes != 0 {
		t.Fatalf("view rows=%d err=%v", writes, err)
	}
}
