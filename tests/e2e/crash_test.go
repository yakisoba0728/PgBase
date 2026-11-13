//go:build e2e

package e2e

import (
	"context"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/yakisoba0728/PgBase/tests/testsupport"
)

func TestAbruptApplicationAndDatabaseTerminationPreservesCommittedState(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	client := authenticatedClient(t, ctx, suite.baseURL)
	collection := uniqueCollection("crash_items")
	applyCollection(t, ctx, client, map[string]any{
		"name":   collection,
		"fields": []map[string]any{{"name": "value", "type": "text", "required": true}},
	})
	path := "/api/v1/collections/" + collection + "/records"
	input := map[string]any{"value": "acknowledged"}
	headers := http.Header{"Idempotency-Key": []string{collection + "-create"}}
	var created map[string]any
	if _, err := client.requestWithHeaders(ctx, http.MethodPost, path, input, headers, http.StatusCreated, &created); err != nil {
		t.Fatal(err)
	}
	id := stringValue(t, created, "id")
	appID := suite.composeServiceContainer(t, ctx, "pgbase")
	databaseID := suite.composeServiceContainer(t, ctx, "postgres")
	// Prevent an automatic restart from hiding the killed state before inspection.
	if output, err := suite.docker(ctx, "update", "--restart=no", appID, databaseID); err != nil {
		t.Fatalf("disable automatic restart: %v\n%s", err, output)
	}
	t.Cleanup(func() {
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 2*time.Minute)
		defer cleanupCancel()
		if output, err := suite.docker(cleanupCtx, "update", "--restart=unless-stopped", appID, databaseID); err != nil {
			t.Errorf("restore restart policy: %v\n%s", err, output)
		}
		if output, err := suite.compose(cleanupCtx, "up", "-d", "--wait", "--no-build"); err != nil {
			t.Errorf("recover compose services: %v\n%s", err, output)
		}
	})
	// The uncommitted write must disappear while the acknowledged create and its
	// idempotency receipt survive PostgreSQL recovery on the same volume.
	application := collection + "_uncommitted"
	sql := "BEGIN; UPDATE app." + collection + " SET value='uncommitted'; SELECT pg_sleep(300);"
	if output, err := suite.docker(ctx, "exec", "-d", "-e", "PGAPPNAME="+application, databaseID,
		"psql", "-U", "pgbase", "-d", "pgbase", "-X", "-v", "ON_ERROR_STOP=1", "-c", sql); err != nil {
		t.Fatalf("start uncommitted write: %v\n%s", err, output)
	}
	query := "SELECT count(*) FROM pg_stat_activity WHERE application_name='" + application + "' AND wait_event='PgSleep'"
	if err := testsupport.Poll(ctx, 15*time.Second, 50*time.Millisecond, 2*time.Second, func(attemptCtx context.Context) (bool, error) {
		output, err := suite.compose(attemptCtx, "exec", "-T", "postgres", "psql", "-U", "pgbase", "-d", "pgbase", "-XAt", "-c", query)
		return strings.TrimSpace(output) == "1", err
	}); err != nil {
		t.Fatalf("write did not reach the uncommitted boundary: %v", err)
	}
	if output, err := suite.compose(ctx, "kill", "--signal", "SIGKILL", "pgbase", "postgres"); err != nil {
		t.Fatalf("kill application and database: %v\n%s", err, output)
	}
	for _, container := range []string{appID, databaseID} {
		if code := suite.containerExitCode(t, ctx, container); code != 137 {
			t.Fatalf("container %s exit code=%d, want SIGKILL (137)", container, code)
		}
	}
	if output, err := suite.compose(ctx, "up", "-d", "--wait", "--no-build"); err != nil {
		t.Fatalf("restart after SIGKILL: %v\n%s", err, output)
	}
	var fetched map[string]any
	if err := client.request(ctx, http.MethodGet, path+"/"+id, nil, http.StatusOK, &fetched); err != nil {
		t.Fatal(err)
	}
	if fetched["value"] != "acknowledged" {
		t.Fatalf("recovered value=%v, want acknowledged", fetched["value"])
	}
	var replayed map[string]any
	response, err := client.requestWithHeaders(ctx, http.MethodPost, path, input, headers, http.StatusCreated, &replayed)
	if err != nil {
		t.Fatal(err)
	}
	if response.Get("Idempotency-Replayed") != "true" || replayed["id"] != id {
		t.Fatalf("crash lost idempotency receipt: headers=%v record=%v", response, replayed)
	}
}
