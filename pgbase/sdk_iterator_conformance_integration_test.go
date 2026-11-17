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

func TestSDKIteratorSessionHTTPConformance(t *testing.T) {
	app := newPublicUsersAppOptions(t, true, nil)
	cookie, csrf := setupPublicUsersAdministrator(t, app)
	createPublicUsersCollection(t, app, cookie, csrf, "iterator_notes")
	assertPublicUsersStatus(t, publicUsersRequest(t, app, "PUT", "/api/admin/collections/iterator_notes/user-access",
		map[string]any{"enabled": true}, "", cookie, map[string]string{"X-PgBase-CSRF": csrf}), 200)
	owner := registerPublicUser(t, app, "iterator-owner@example.test", publicUserOnePass)
	other := registerPublicUser(t, app, "iterator-other@example.test", publicUserOnePass)
	for _, title := range []string{"private-first", "private-second"} {
		assertPublicUsersStatus(t, publicUsersRequest(t, app, "POST", "/api/v1/user/collections/iterator_notes/records",
			map[string]any{"title": title}, owner.Token, nil, map[string]string{"Idempotency-Key": "iterator-" + title}), 201)
	}
	repo := filepath.Clean("..")
	build := exec.Command("npm", "--prefix", filepath.Join(repo, "sdk/typescript"), "run", "build")
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build SDK: %v\n%s", err, output)
	}
	for _, sdk := range []struct {
		name, binary, directory string
		arguments               []string
	}{
		{"typescript", "node", repo, []string{"sdk/typescript/test/iterator-http-conformance.mjs"}},
		{"dart", "dart", filepath.Join(repo, "sdk/dart"), []string{"run", "tool/iterator_http_conformance.dart"}},
	} {
		t.Run(sdk.name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			command := exec.CommandContext(ctx, sdk.binary, sdk.arguments...)
			command.Dir = sdk.directory
			command.Env = append(os.Environ(), "PGBASE_SDK_URL="+app.server.URL,
				"PGBASE_SDK_TOKEN="+owner.Token, "PGBASE_SDK_OTHER_TOKEN="+other.Token)
			if output, err := command.CombinedOutput(); err != nil {
				t.Fatalf("iterator HTTP conformance: %v\n%s", err, output)
			}
		})
	}
}
