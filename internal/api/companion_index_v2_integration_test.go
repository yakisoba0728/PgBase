//go:build integration

package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/webisnoteasy/PgBase/internal/database"
	"github.com/webisnoteasy/PgBase/internal/migrate"
	"github.com/webisnoteasy/PgBase/internal/recordops"
	"github.com/webisnoteasy/PgBase/internal/schema"
	"github.com/webisnoteasy/PgBase/internal/testutil"
)

// SPEC-2 through the surface an operator actually watches. Before the fix, a
// collection created after the companion rollout was activated dropped the
// capability straight back to pending and left its sort index unbuilt, so
// serving the collection meant another activate/build cycle by hand. Here the
// create happens while the capability is ready and the authenticated list works
// immediately, with no operator step between the two.
func TestReadyCapabilityServesANewCollectionWithNoOperatorStep(t *testing.T) {
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
		values ($1, $2, 'test', true, current_timestamp, current_timestamp)`,
		administrator.ID, administrator.Email); err != nil {
		t.Fatal(err)
	}

	schemas := schema.NewService(pool, &schema.Cache{}, nil)
	// The first collection exists only so the rollout has something to build and
	// so this process holds a schema-writer lease to witness.
	applyCompanionCollection(t, schemas, "companion_api_seed", administrator.ID)

	leases, err := schemas.SchemaWriterFleet(ctx)
	if err != nil {
		t.Fatal(err)
	}
	proof := schema.SchemaWriterFleetProof{}
	for _, lease := range leases {
		proof.Instances = append(proof.Instances, lease.InstanceID)
	}
	if _, err := schemas.ActivateCompanionIndexes(ctx, proof); err != nil {
		t.Fatalf("activate companion index rollout: %v", err)
	}
	if _, err := schemas.MaintainCompanionIndexes(ctx); err != nil {
		t.Fatalf("build companion indexes: %v", err)
	}

	// The collection the finding is about: created while the capability is ready.
	created := applyCompanionCollection(t, schemas, "companion_api_notes", administrator.ID)
	status, err := schemas.CompanionIndexStatus(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if !status.Ready() {
		t.Fatalf("creating a collection dropped the capability: %#v", status)
	}
	companionName := schema.NullableDescCompanionIndexName(created.Name, "nickname")
	var present bool
	if err := pool.QueryRow(ctx, `select exists(select 1 from pg_indexes
		where schemaname = 'app' and tablename = $1 and indexname = $2)`,
		created.TableName, companionName).Scan(&present); err != nil {
		t.Fatal(err)
	}
	if !present {
		t.Fatalf("companion index %s was not built with its collection", companionName)
	}

	records := recordops.NewService(pool, schemas, testTokenSecret, nil, nil, nil)
	handler := mustRouter(t, recordTestDependencies(
		&fakeAdminAuth{authenticateAdmin: administrator}, records))
	for _, body := range []string{
		`{"title":"first","nickname":"beta"}`,
		`{"title":"second","nickname":null}`,
		`{"title":"third","nickname":"alpha"}`,
	} {
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, authenticatedRecordRequest(http.MethodPost,
			"/api/v1/collections/companion_api_notes/records", body, true))
		if response.Code != http.StatusCreated {
			t.Fatalf("create status = %d body = %s", response.Code, response.Body.String())
		}
	}

	response := httptest.NewRecorder()
	handler.ServeHTTP(response, authenticatedRecordRequest(http.MethodGet,
		"/api/v1/collections/companion_api_notes/records?sort=-nickname", "", false))
	if response.Code != http.StatusOK {
		t.Fatalf("list status = %d body = %s", response.Code, response.Body.String())
	}
	var page struct {
		Items []map[string]any `json:"items"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &page); err != nil {
		t.Fatal(err)
	}
	// The public contract for a nullable descending sort is NULLS LAST, which is
	// the ordering the companion index exists to serve.
	var titles []any
	for _, item := range page.Items {
		titles = append(titles, item["title"])
	}
	if len(page.Items) != 3 || titles[0] != "first" || titles[1] != "third" || titles[2] != "second" {
		t.Fatalf("descending nullable page = %v", titles)
	}
}

func applyCompanionCollection(
	t *testing.T, schemas *schema.Service, name string, administratorID uuid.UUID,
) schema.Collection {
	t.Helper()
	ctx := context.Background()
	change := schema.NewCreateChange(schema.CreateCollectionInput{Name: name, Fields: []schema.FieldInput{
		{Name: "title", Type: schema.TypeText, Required: true, Indexed: true},
		{Name: "nickname", Type: schema.TypeText, Indexed: true},
	}})
	plan, err := schemas.Preview(ctx, change)
	if err != nil {
		t.Fatalf("preview %s: %v", name, err)
	}
	result, err := schemas.Apply(ctx, schema.LegacyApply(change, plan, administratorID))
	if err != nil {
		t.Fatalf("apply %s: %v", name, err)
	}
	return result.Collection
}
