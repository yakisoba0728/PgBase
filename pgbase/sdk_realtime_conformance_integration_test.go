//go:build integration && sdkconformance

package pgbase

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

func TestSDKRealtimeHTTPConformance(t *testing.T) {
	for _, executable := range []string{"node", "npm", "dart"} {
		if _, err := exec.LookPath(executable); err != nil {
			t.Fatalf("%s is required for SDK realtime conformance: %v", executable, err)
		}
	}

	app := newPublicUsersApp(t, true)
	admin, csrf := setupPublicUsersAdministrator(t, app)
	change := map[string]any{
		"operation": "create",
		"name":      "sdk_realtime_notes",
		"fields": []map[string]any{
			{"name": "title", "type": "text", "required": true},
			{"name": "ignored", "type": "text"},
		},
	}
	plan := publicUsersRequest(t, app, "POST", "/api/admin/collections/plan", change, "", admin, map[string]string{"X-PgBase-CSRF": csrf})
	assertPublicUsersStatus(t, plan, 200)
	var planned struct {
		BaseVersion int64  `json:"baseVersion"`
		Checksum    string `json:"checksum"`
	}
	decodePublicUsersJSON(t, plan, &planned)
	change["baseVersion"], change["checksum"] = planned.BaseVersion, planned.Checksum
	assertPublicUsersStatus(t, publicUsersRequest(t, app, "POST", "/api/admin/collections/apply", change, "", admin, map[string]string{"X-PgBase-CSRF": csrf}), 201)
	assertPublicUsersStatus(t, publicUsersRequest(t, app, "PUT", "/api/admin/collections/sdk_realtime_notes/user-access", map[string]any{"enabled": true}, "", admin, map[string]string{"X-PgBase-CSRF": csrf}), 200)
	owner := registerPublicUser(t, app, "sdk-realtime@example.test", publicUserOnePass)

	repository, err := filepath.Abs("..")
	if err != nil {
		t.Fatal(err)
	}
	typeScriptDirectory := filepath.Join(repository, "sdk", "typescript")
	dartDirectory := filepath.Join(repository, "sdk", "dart")
	runSDKCommand(t, repository, nil, "npm", "--prefix", typeScriptDirectory, "ci", "--ignore-scripts")
	runSDKCommand(t, repository, nil, "npm", "--prefix", typeScriptDirectory, "run", "build")
	runSDKCommand(t, dartDirectory, nil, "dart", "pub", "get")
	environment := map[string]string{
		"PGBASE_RT_URL":        app.server.URL,
		"PGBASE_RT_TOKEN":      owner.Token,
		"PGBASE_RT_COLLECTION": "sdk_realtime_notes",
	}
	t.Run("typescript", func(t *testing.T) {
		runSDKCommand(t, repository, environment, "node", filepath.Join("sdk", "typescript", "test", "realtime-http-conformance.mjs"))
	})
	t.Run("dart", func(t *testing.T) {
		runSDKCommand(t, dartDirectory, environment, "dart", "run", "tool/realtime_http_conformance.dart")
	})
}

func runSDKCommand(t *testing.T, directory string, environment map[string]string, name string, arguments ...string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, name, arguments...)
	command.Dir = directory
	command.Env = os.Environ()
	for key, value := range environment {
		command.Env = append(command.Env, key+"="+value)
	}
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("%s %v: %v\n%s", name, arguments, err, output)
	}
}
