//go:build integration

package pgbase

import (
	"net/http"
	"os"
	"path/filepath"
	"testing"

	"github.com/yakisoba0728/PgBase/internal/observability"
)

func TestJavaScriptSkipSuccessActivityLogPreservesFailures(t *testing.T) {
	directory := t.TempDir()
	manifest := `{"format":"pgbase.scripts/v1","routes":[
 {"method":"GET","pattern":"/extensions/activity/skipped","access":"public","handler":{"module":"route.js","export":"skipped"}},
 {"method":"GET","pattern":"/extensions/activity/ordinary","access":"public","handler":{"module":"route.js","export":"ordinary"}},
 {"method":"GET","pattern":"/extensions/activity/failure","access":"public","handler":{"module":"route.js","export":"failure"}}
]}`
	source := `exports.skipped=$apis.skipSuccessActivityLog(()=>({status:200,body:"skipped"}));
exports.ordinary=()=>({status:200,body:"ordinary"});
exports.failure=$apis.skipSuccessActivityLog(()=>{throw new Error("expected route failure")});`
	if err := os.WriteFile(filepath.Join(directory, "pgbase.scripts.json"), []byte(manifest), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(directory, "route.js"), []byte(source), 0o600); err != nil {
		t.Fatal(err)
	}
	fixture := newPublicUsersAppOptions(t, false, func(options *Options) {
		options.JavaScript = &JavaScriptOptions{Directory: directory, Concurrency: 1, Queue: 1}
	})

	assertActivityLogDelta(t, fixture, "/extensions/activity/skipped", http.StatusOK, 0, func() {
		response := publicUsersRequest(t, fixture, http.MethodGet, "/extensions/activity/skipped", nil, "", nil, nil)
		assertPublicUsersStatus(t, response, http.StatusOK)
	})
	assertActivityLogDelta(t, fixture, "/extensions/activity/ordinary", http.StatusOK, 1, func() {
		response := publicUsersRequest(t, fixture, http.MethodGet, "/extensions/activity/ordinary", nil, "", nil, nil)
		assertPublicUsersStatus(t, response, http.StatusOK)
	})
	assertActivityLogDelta(t, fixture, "/extensions/activity/failure", http.StatusInternalServerError, 1, func() {
		response := publicUsersRequest(t, fixture, http.MethodGet, "/extensions/activity/failure", nil, "", nil, nil)
		assertPublicUsersStatus(t, response, http.StatusInternalServerError)
	})
}

func assertActivityLogDelta(t *testing.T, fixture *publicUsersFixture, route string, status, want int, request func()) {
	t.Helper()
	before := countActivityLogEntries(fixture.app.requestLog.Page(0, 0).Items, route, status)
	request()
	after := countActivityLogEntries(fixture.app.requestLog.Page(0, 0).Items, route, status)
	if got := after - before; got != want {
		t.Fatalf("request-log delta for %s status %d = %d, want %d; entries=%#v", route, status, got, want, fixture.app.requestLog.Page(0, 0).Items)
	}
}

func countActivityLogEntries(entries []observability.RequestEntry, route string, status int) int {
	count := 0
	for _, entry := range entries {
		if entry.Route == route && entry.Status == status {
			count++
		}
	}
	return count
}
