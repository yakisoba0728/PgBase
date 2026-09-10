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

// TestSDKPublicRecordHTTPConformance runs the packaged SDK against a live
// httptest PgBase handler. It intentionally exercises the wire contract rather
// than replacing fetch with a mock.
func TestSDKPublicRecordHTTPConformance(t *testing.T) {
	if _, err := exec.LookPath("node"); err != nil {
		t.Fatalf("node is required for SDK conformance: %v", err)
	}
	app := newPublicUsersAppOptions(t, true, func(options *Options) { options.Files = FileOptions{Driver: "local", LocalDirectory: t.TempDir()} })
	adminCookie, csrf := setupPublicUsersAdministrator(t, app)
	change := map[string]any{"operation": "create", "name": "notes", "fields": []map[string]any{{"name": "title", "type": "text"}, {"name": "attachment", "type": "file"}}}
	plan := publicUsersRequest(t, app, "POST", "/api/admin/collections/plan", change, "", adminCookie, map[string]string{"X-PgBase-CSRF": csrf})
	assertPublicUsersStatus(t, plan, 200)
	var planned struct {
		BaseVersion int64
		Checksum    string
	}
	decodePublicUsersJSON(t, plan, &planned)
	change["baseVersion"], change["checksum"] = planned.BaseVersion, planned.Checksum
	assertPublicUsersStatus(t, publicUsersRequest(t, app, "POST", "/api/admin/collections/apply", change, "", adminCookie, map[string]string{"X-PgBase-CSRF": csrf}), 201)
	enable := publicUsersRequest(t, app, "PUT", "/api/admin/collections/notes/user-access", map[string]any{"enabled": true}, "", adminCookie, map[string]string{"X-PgBase-CSRF": csrf})
	assertPublicUsersStatus(t, enable, 200)
	dartOwner := registerPublicUser(t, app, "sdk-dart-page@example.test", publicUserOnePass)
	repo := filepath.Clean("..")
	build := exec.Command("npm", "--prefix", filepath.Join(repo, "sdk/typescript"), "run", "build")
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build SDK: %v\n%s", err, output)
	}
	script := `import { PgBaseClient } from "./sdk/typescript/dist/index.js";
const client = new PgBaseClient(process.argv[1]);
const s = await client.register("sdk-real@example.test", "sdk-real-password-0123456789");
if (!s.token) throw new Error("registration returned no token");
const records = client.records("notes");
const created = await records.create({title:"from sdk"}, "sdk-real-record-create-0001");
if (!created.id) throw new Error("create returned no id");
const page = await records.list(); if (page.items.length !== 1) throw new Error("owned list mismatch");
const bound = await records.list({filter: client.filter("title = {:title}", {title:"from sdk"}), compat:"pocketbase"});
if (bound.items.length !== 1 || bound.items[0].id !== created.id) throw new Error("safe filter did not bind the intended record");
const attack = 'from sdk" || title != "from sdk';
const escaped = client.filter("title = {:title}", {title: attack});
if (escaped.includes(' || title != "from sdk"')) throw new Error("filter literal was interpolated as filter syntax");
const noMatch = await records.list({filter: escaped, compat:"pocketbase"});
if (noMatch.items.length !== 0) throw new Error("filter-looking input widened the record query");
const numbered = await records.getList(1, 30, {filter: client.filter("title = {:title}", {title:"from sdk"}), sort:"@rowid"});
if (numbered.page !== 1 || numbered.perPage !== 30 || numbered.totalItems !== 1 || numbered.totalPages !== 1 || numbered.items.length !== 1 || numbered.items[0].id !== created.id) throw new Error("PocketBase numbered page did not bind the intended record");
const skipped = await records.getList(1, 30, {filter: client.filter("title = {:title}", {title:"from sdk"}), skipTotal:true});
if (skipped.totalItems !== -1 || skipped.totalPages !== -1 || skipped.items.length !== 1) throw new Error("PocketBase skipTotal envelope mismatch");
const read = await fetch(process.argv[1] + "/api/v1/user/collections/notes/records/" + created.id, {headers:{Authorization:"Bearer " + s.token}});
const etag = read.headers.get("etag"); if (!etag) throw new Error("read returned no etag");
await records.update(created.id, {title:"updated"}, etag);
let stale = false; try { await records.update(created.id, {title:"stale"}, etag); } catch { stale = true; }
if (!stale) throw new Error("stale etag was accepted");
const files = client.files("notes", created.id);
const file = await files.upload("attachment", new Blob(["SDK real file bytes"], {type:"text/plain"}), "sdk.txt");
const downloadToken = await files.mintDownloadToken(file.id);
const downloaded = await fetch(files.downloadURL(file.id, downloadToken.token), {headers:{Authorization:"Bearer " + client.auth.token}});
if (!downloaded.ok || await downloaded.text() !== "SDK real file bytes") throw new Error("SDK file bytes mismatch");
await files.delete(file.id);
if ((await files.list("attachment")).items.length !== 0) throw new Error("SDK file deletion failed");
const events = records.events({lastEventId:0,maxReconnects:0,signal:AbortSignal.timeout(10000)});
const event = await events.next(); if (event.done || event.value.recordId !== created.id) throw new Error("SDK SSE replay mismatch");
await client.realm("users").logout();
const ended = await events.next(); if (!ended.done) throw new Error("logout did not end SDK subscription");
const revoked = await fetch(process.argv[1] + "/api/v1/auth/session", {headers:{Authorization:"Bearer " + s.token}});
if (revoked.status !== 401) throw new Error("realm logout did not revoke session");`
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, "node", "--input-type=module", "--eval", script, app.server.URL)
	command.Dir = repo
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("SDK HTTP conformance: %v\n%s", err, output)
	}
	dart := exec.CommandContext(ctx, "dart", "run", "tool/pocketbase_http_conformance.dart")
	dart.Dir = filepath.Join(repo, "sdk/dart")
	dart.Env = append(os.Environ(), "PGBASE_SDK_URL="+app.server.URL, "PGBASE_SDK_TOKEN="+dartOwner.Token)
	if output, err := dart.CombinedOutput(); err != nil {
		t.Fatalf("Dart SDK HTTP conformance: %v\n%s", err, output)
	}
}
