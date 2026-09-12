//go:build integration

package pgbase

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/yakisoba0728/PgBase/internal/identity"
	"github.com/yakisoba0728/PgBase/internal/jsvm"
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
	directory := t.TempDir()
	for name, contents := range map[string]string{
		"pgbase.scripts.json": `{"format":"pgbase.scripts/v1","routes":[{"method":"GET","pattern":"/query","access":"admin","handler":{"module":"route.js","export":"run"},"capabilities":["records","app-control"]}]}`,
		"route.js":            `exports.run=()=>({count:$app.countRecords("query_notes","title != ''"),first:$app.findFirstRecordByData("query_notes","title","first").getString("title"),stats:$app.logsStats().total})`,
	} {
		if err := os.WriteFile(filepath.Join(directory, name), []byte(contents), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	bundle, err := jsvm.LoadBundle(directory)
	if err != nil {
		t.Fatal(err)
	}
	runtime, err := jsvm.NewRuntime(bundle, jsvm.Limits{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = runtime.Close(t.Context()) })
	output, err := runtime.Invoke(t.Context(), jsvm.Invocation{Handler: bundle.Manifest().Routes[0].Handler, Capabilities: []string{"records", "app-control"}, Host: host})
	if err != nil || !json.Valid(output) || !containsJSONKey(output, "count") || !containsJSONKey(output, "first") {
		t.Fatalf("js output=%s err=%v", output, err)
	}
}

func TestExtensionAppRecordFindAllHonorsAggregateOutputBudget(t *testing.T) {
	fixture := newPublicUsersApp(t, false)
	adminCookie, csrf := setupPublicUsersAdministrator(t, fixture)
	createPublicUsersCollection(t, fixture, adminCookie, csrf, "find_all_budget")
	principal := identity.Subject{Kind: identity.Administrator, ID: uuid.New()}
	for index := 0; index < 8; index++ {
		_, err := fixture.app.extensions.records.Create(t.Context(), recordops.CreateRequest{Principal: principal, RequestID: fmt.Sprintf("find-all-%d", index), Collection: "find_all_budget", Input: map[string]json.RawMessage{"title": json.RawMessage(fmt.Sprintf("%q", strings.Repeat("x", 80)))}})
		if err != nil {
			t.Fatal(err)
		}
	}
	fixture.app.extensions.maximumOutputBytes = 256
	host := &extensionHost{bridge: fixture.app.extensions, principal: principal, requestID: "find-all-budget", valid: true}
	if _, err := host.callAppRecords(t.Context(), "app.record.findAll", json.RawMessage(`{"collection":"find_all_budget"}`)); err == nil {
		t.Fatal("findAll returned a partial result past its aggregate budget")
	} else {
		var coded interface{ CodeValue() string }
		if !errors.As(err, &coded) || coded.CodeValue() != "record_result_limit" {
			t.Fatalf("findAll error=%v", err)
		}
	}
	fixture.app.extensions.maximumOutputBytes = 1 << 20
	result, err := host.callAppRecords(t.Context(), "app.record.findAll", json.RawMessage(`{"collection":"find_all_budget","filter":"title != ''"}`))
	if err != nil {
		t.Fatal(err)
	}
	var page extensionRecordResult
	if err := json.Unmarshal(result, &page); err != nil || len(page.Items) != 8 {
		t.Fatalf("findAll items=%d err=%v", len(page.Items), err)
	}
}
