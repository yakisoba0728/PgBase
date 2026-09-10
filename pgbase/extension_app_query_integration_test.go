//go:build integration

package pgbase

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/yakisoba0728/PgBase/internal/identity"
	"github.com/yakisoba0728/PgBase/internal/recordops"
)

func TestExtensionAppRecordCountAndPrivilegedLogDeletionUseLiveDatabase(t *testing.T) {
	fixture := newPublicUsersApp(t, false)
	adminCookie, csrf := setupPublicUsersAdministrator(t, fixture)
	createPublicUsersCollection(t, fixture, adminCookie, csrf, "query_notes")
	principal := identity.Subject{Kind: identity.Administrator, ID: uuid.New()}
	for index, title := range []string{"first", "second", "third"} {
		_, err := fixture.app.extensions.records.Create(t.Context(), recordops.CreateRequest{Principal: principal, RequestID: "ext-query-create-" + string(rune('a'+index)), Collection: "query_notes", Input: map[string]json.RawMessage{"title": json.RawMessage(`"` + title + `"`)}})
		if err != nil {
			t.Fatal(err)
		}
	}
	host := &extensionHost{bridge: fixture.app.extensions, principal: principal, requestID: "extension-query-test", valid: true}
	count, err := host.callAppRecords(t.Context(), "app.record.count", json.RawMessage(`{"collection":"query_notes","filter":"title ~ 'i'"}`))
	if err != nil || string(count) != "2" {
		t.Fatalf("count=%s err=%v", count, err)
	}
	old := time.Now().Add(-48 * time.Hour).UTC()
	if _, err := fixture.app.pool.Exec(t.Context(), `INSERT INTO pgbase_meta.request_logs(occurred_at,method,route,status,duration_ms) VALUES($1,'GET','/old',200,1),(clock_timestamp(),'GET','/new',200,1)`, old); err != nil {
		t.Fatal(err)
	}
	cutoff, _ := json.Marshal(map[string]any{"to": time.Now().Add(-24 * time.Hour)})
	deleted, err := host.callAppQueries(t.Context(), "app.log.deleteOld", cutoff)
	if err != nil || string(deleted) != `{"deleted":1}` {
		t.Fatalf("deleted=%s err=%v", deleted, err)
	}
	denied := &extensionHost{bridge: fixture.app.extensions, principal: identity.Subject{Kind: identity.User, ID: uuid.New()}, requestID: "denied-query", valid: true}
	if _, err := denied.callAppQueries(t.Context(), "app.log.stats", json.RawMessage(`{}`)); err == nil {
		t.Fatal("user accessed logs")
	}
}
