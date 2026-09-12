//go:build integration

package pgbase

import (
	"encoding/json"
	"testing"
)

func TestDatabaseDryRunViewPropagatesMidStreamError(t *testing.T) {
	fixture := newPublicUsersApp(t, false)
	request, err := json.Marshal(map[string]any{
		"Name":       "app.audit_preview",
		"Query":      "select i as id, case when i=3 then pg_cancel_backend(pg_backend_pid()) else true end as continued from generate_series(1,20) i",
		"SampleSize": 20,
	})
	if err != nil {
		t.Fatal(err)
	}
	output, err := fixture.app.extensions.appControl.callDatabaseMetadata(t.Context(), "app-control.databaseDryRunView", request)
	if err == nil {
		t.Fatalf("mid-stream PostgreSQL cancellation returned success: %s", output)
	}
}
