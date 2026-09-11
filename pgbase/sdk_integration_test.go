//go:build integration && sdkconformance

package pgbase

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/google/uuid"
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
	applyCollection := func(change map[string]any) {
		t.Helper()
		plan := publicUsersRequest(t, app, "POST", "/api/admin/collections/plan", change, "", adminCookie, map[string]string{"X-PgBase-CSRF": csrf})
		assertPublicUsersStatus(t, plan, 200)
		var planned struct {
			BaseVersion int64
			Checksum    string
		}
		decodePublicUsersJSON(t, plan, &planned)
		change["baseVersion"], change["checksum"] = planned.BaseVersion, planned.Checksum
		assertPublicUsersStatus(t, publicUsersRequest(t, app, "POST", "/api/admin/collections/apply", change, "", adminCookie, map[string]string{"X-PgBase-CSRF": csrf}), 201)
		assertPublicUsersStatus(t, publicUsersRequest(t, app, "PUT", "/api/admin/collections/"+change["name"].(string)+"/user-access", map[string]any{"enabled": true}, "", adminCookie, map[string]string{"X-PgBase-CSRF": csrf}), 200)
	}
	applyCollection(map[string]any{"operation": "create", "name": "modifier_tags", "fields": []map[string]any{{"name": "name", "type": "text"}}})
	applyCollection(map[string]any{"operation": "create", "name": "notes", "fields": []map[string]any{{"name": "title", "type": "text"}, {"name": "attachment", "type": "file"}}})
	applyCollection(map[string]any{"operation": "create", "name": "modifier_notes", "fields": []map[string]any{
		{"name": "title", "type": "text"}, {"name": "count", "type": "integer"},
		{"name": "roles", "type": "jsonb", "options": map[string]any{"allowedValues": []string{"a", "b", "c", "d"}, "maxSelect": 4}},
		{"name": "tags", "type": "relation", "relation": map[string]any{"targetCollection": "modifier_tags", "cardinality": "multiple", "minSelect": 0, "maxSelect": 4, "onDelete": "restrict"}},
	}})
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
const modifierRecords = client.records("modifier_notes");
const tags = client.records("modifier_tags");
const firstTag = await tags.create({name:"first"}, "sdk-real-tag-first-0001");
const secondTag = await tags.create({name:"second"}, "sdk-real-tag-second-0001");
const thirdTag = await tags.create({name:"third"}, "sdk-real-tag-third-0001");
const created = await records.create({title:"from sdk"}, "sdk-real-record-create-0001");
if (!created.id) throw new Error("create returned no id");
const modifierCreated = await modifierRecords.create({title:"modifier", count:10, roles:["b","d"], tags:[firstTag.id,secondTag.id]}, "sdk-real-modifier-create-0001");
let modifierRead = await fetch(process.argv[1] + "/api/v1/user/collections/modifier_notes/records/" + modifierCreated.id, {headers:{Authorization:"Bearer " + s.token}});
let modifierETag = modifierRead.headers.get("etag"); if (!modifierETag) throw new Error("modifier read did not return ETag");
let modified = await modifierRecords.update(modifierCreated.id, {"count+": 5}, modifierETag);
if (modified.count !== 15) throw new Error("numeric append modifier did not increment");
modifierRead = await fetch(process.argv[1] + "/api/v1/user/collections/modifier_notes/records/" + modifierCreated.id, {headers:{Authorization:"Bearer " + s.token}});
modifierETag = modifierRead.headers.get("etag"); if (!modifierETag) throw new Error("modifier decrement read did not return ETag");
modified = await modifierRecords.update(modifierCreated.id, {"count-": 3}, modifierETag);
if (modified.count !== 12) throw new Error("numeric remove modifier did not decrement");
modifierRead = await fetch(process.argv[1] + "/api/v1/user/collections/modifier_notes/records/" + modifierCreated.id, {headers:{Authorization:"Bearer " + s.token}});
modifierETag = modifierRead.headers.get("etag"); if (!modifierETag) throw new Error("mixed modifier read did not return ETag");
modified = await modifierRecords.update(modifierCreated.id, {count:100,"count-":50,"+roles":["a","b"],"roles+":["c","a"],"roles-":"b","+tags":[thirdTag.id],"tags-":[secondTag.id]}, modifierETag);
if (modified.count !== 50 || JSON.stringify(modified.roles) !== JSON.stringify(["a","d","c"]) || JSON.stringify(modified.tags) !== JSON.stringify([thirdTag.id,firstTag.id])) throw new Error("mixed select/relation modifier final values mismatch");
modifierRead = await fetch(process.argv[1] + "/api/v1/user/collections/modifier_notes/records/" + modifierCreated.id, {headers:{Authorization:"Bearer " + s.token}});
modifierETag = modifierRead.headers.get("etag"); if (!modifierETag) throw new Error("rollback modifier read did not return ETag");
let rejected = false; try { await modifierRecords.update(modifierCreated.id, {"count+":20,"tags+":["not-a-uuid"]}, modifierETag); } catch { rejected = true; }
if (!rejected) throw new Error("invalid modifier unexpectedly succeeded");
const afterRejected = await modifierRecords.get(modifierCreated.id); if (afterRejected.count !== 50 || JSON.stringify(afterRejected.tags) !== JSON.stringify([thirdTag.id,firstTag.id])) throw new Error("invalid modifier did not roll back atomically");
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

// TestSDKRelationFileNativeListRegression exercises a public SDK list over a
// source collection that carries both a file and a multiple relation. Creating
// the record succeeds; its immediate native list must also succeed.
func TestSDKRelationFileNativeListRegression(t *testing.T) {
	if _, err := exec.LookPath("node"); err != nil {
		t.Fatalf("node is required for SDK conformance: %v", err)
	}
	app := newPublicUsersAppOptions(t, true, func(options *Options) { options.Files = FileOptions{Driver: "local", LocalDirectory: t.TempDir()} })
	adminCookie, csrf := setupPublicUsersAdministrator(t, app)
	applyCollection := func(change map[string]any) {
		t.Helper()
		plan := publicUsersRequest(t, app, "POST", "/api/admin/collections/plan", change, "", adminCookie, map[string]string{"X-PgBase-CSRF": csrf})
		assertPublicUsersStatus(t, plan, 200)
		var planned struct {
			BaseVersion int64
			Checksum    string
		}
		decodePublicUsersJSON(t, plan, &planned)
		change["baseVersion"], change["checksum"] = planned.BaseVersion, planned.Checksum
		assertPublicUsersStatus(t, publicUsersRequest(t, app, "POST", "/api/admin/collections/apply", change, "", adminCookie, map[string]string{"X-PgBase-CSRF": csrf}), 201)
		assertPublicUsersStatus(t, publicUsersRequest(t, app, "PUT", "/api/admin/collections/"+change["name"].(string)+"/user-access", map[string]any{"enabled": true}, "", adminCookie, map[string]string{"X-PgBase-CSRF": csrf}), 200)
	}
	applyCollection(map[string]any{"operation": "create", "name": "relation_file_tags", "fields": []map[string]any{{"name": "name", "type": "text"}}})
	applyCollection(map[string]any{"operation": "create", "name": "relation_file_notes", "fields": []map[string]any{
		{"name": "attachment", "type": "file"},
		{"name": "tags", "type": "relation", "relation": map[string]any{"targetCollection": "relation_file_tags", "cardinality": "multiple", "minSelect": 0, "maxSelect": 4, "onDelete": "restrict"}},
	}})
	repo := filepath.Clean("..")
	build := exec.Command("npm", "--prefix", filepath.Join(repo, "sdk/typescript"), "run", "build")
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build SDK: %v\n%s", err, output)
	}
	script := `import { PgBaseClient } from "./sdk/typescript/dist/index.js";
const client = new PgBaseClient(process.argv[1]);
const session = await client.register("sdk-relation-file@example.test", "sdk-relation-file-password-0123456789");
const tags = client.records("relation_file_tags");
const records = client.records("relation_file_notes");
const first = await tags.create({name:"first"}, "sdk-relation-file-tag-first-0001");
const second = await tags.create({name:"second"}, "sdk-relation-file-tag-second-0001");
const created = await records.create({tags:[first.id,second.id]}, "sdk-relation-file-record-create-0001");
const page = await records.list();
if (page.items.length !== 1 || page.items[0].id !== created.id) throw new Error("relation-plus-file native list did not return the created record");
await client.realm("users").logout();`
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, "node", "--input-type=module", "--eval", script, app.server.URL)
	command.Dir = repo
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("relation-plus-file native list: %v\n%s", err, output)
	}
}

// TestSDKTextRecordIdentityHTTPConformance exercises text record identities
// through the packaged TypeScript and Dart clients against live HTTP routes.
func TestSDKTextRecordIdentityHTTPConformance(t *testing.T) {
	if _, err := exec.LookPath("node"); err != nil {
		t.Fatalf("node is required for SDK conformance: %v", err)
	}
	app := newPublicUsersAppOptions(t, true, func(options *Options) { options.Files = FileOptions{Driver: "local", LocalDirectory: t.TempDir()} })
	adminCookie, csrf := setupPublicUsersAdministrator(t, app)
	basePolicy := map[string]any{"kind": "text", "minLength": 1, "maxLength": 32, "pattern": "^[A-Za-z0-9]+$"}
	authPolicy := map[string]any{"kind": "text", "minLength": 1, "maxLength": 36, "pattern": "^[A-Za-z0-9-]+$", "autogeneratePattern": "[a-z0-9]{15}"}
	applyCollection := func(change map[string]any) {
		t.Helper()
		plan := publicUsersRequest(t, app, "POST", "/api/admin/collections/plan", change, "", adminCookie, map[string]string{"X-PgBase-CSRF": csrf})
		assertPublicUsersStatus(t, plan, 200)
		var planned struct {
			BaseVersion int64
			Checksum    string
		}
		decodePublicUsersJSON(t, plan, &planned)
		change["baseVersion"], change["checksum"] = planned.BaseVersion, planned.Checksum
		assertPublicUsersStatus(t, publicUsersRequest(t, app, "POST", "/api/admin/collections/apply", change, "", adminCookie, map[string]string{"X-PgBase-CSRF": csrf}), 201)
		assertPublicUsersStatus(t, publicUsersRequest(t, app, "PUT", "/api/admin/collections/"+change["name"].(string)+"/user-access", map[string]any{"enabled": true}, "", adminCookie, map[string]string{"X-PgBase-CSRF": csrf}), 200)
	}
	applyCollection(map[string]any{"operation": "create", "name": "text_tags", "recordId": basePolicy, "fields": []map[string]any{{"name": "label", "type": "text"}}})
	applyCollection(map[string]any{"operation": "create", "name": "text_notes", "recordId": basePolicy, "fields": []map[string]any{
		{"name": "title", "type": "text"}, {"name": "attachment", "type": "file"},
		{"name": "tags", "type": "relation", "relation": map[string]any{"targetCollection": "text_tags", "cardinality": "multiple", "minSelect": 0, "maxSelect": 4, "onDelete": "restrict"}},
	}})
	applyCollection(map[string]any{"operation": "create", "name": "uuid_notes", "fields": []map[string]any{{"name": "title", "type": "text"}}})
	authRealmID := uuid.New()
	authRealm := "sdk_text_auth"
	assertPublicUsersStatus(t, publicUsersRequest(t, app, "POST", "/api/admin/auth-collections", map[string]any{
		"id": authRealmID.String(), "name": authRealm, "registrationEnabled": true, "recordId": authPolicy,
		"fields": []map[string]any{{"name": "can_manage", "type": "boolean"}},
		"rules":  map[string]any{"list": "true", "view": "true", "create": "true", "update": "true", "delete": "false", "auth": "true", "manage": "@request.auth.can_manage = true && id != @request.auth.id"},
	}, "", adminCookie, map[string]string{"X-PgBase-CSRF": csrf}), 201)
	assertPublicUsersStatus(t, publicUsersRequest(t, app, "POST", "/api/admin/auth-collections/"+authRealmID.String()+"/records", map[string]any{
		"id": "ManagerCase01", "email": "sdk-text-manager@example.test", "password": publicUserOnePass, "active": true, "fields": map[string]any{"can_manage": true},
	}, "", adminCookie, map[string]string{"X-PgBase-CSRF": csrf}), 201)
	repo := filepath.Clean("..")
	build := exec.Command("npm", "--prefix", filepath.Join(repo, "sdk/typescript"), "run", "build")
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build SDK: %v\n%s", err, output)
	}
	script := `import { PgBaseClient } from "./sdk/typescript/dist/index.js";
const baseURL = process.argv[1];
const client = new PgBaseClient(baseURL);
await client.register("sdk-text-records@example.test", "sdk text records password 0123456789");
const tags = client.records("text_tags");
const records = client.records("text_notes");
const tag = await tags.create({id:"TaGCase01",label:"first"}, "sdk-text-tag-create-0001");
if (tag.id !== "TaGCase01") throw new Error("text relation target id lost on create");
const created = await records.create({id:"MiXeDNote01",title:"created",tags:[tag.id]}, "sdk-text-note-create-0001");
if (created.id !== "MiXeDNote01" || JSON.stringify(created.tags) !== JSON.stringify([tag.id])) throw new Error("text record id or relation did not round trip on create");
const loaded = await records.get("MiXeDNote01");
if (loaded.id !== "MiXeDNote01" || JSON.stringify(loaded.tags) !== JSON.stringify([tag.id])) throw new Error("text record get lost exact id or relation");
let duplicate = false; try { await records.create({id:"MiXeDNote01",title:"duplicate"}, "sdk-text-note-duplicate-0001"); } catch { duplicate = true; }
if (!duplicate || (await records.get("MiXeDNote01")).title !== "created") throw new Error("text id collision was not atomic");
let current = await records.getWithETag("MiXeDNote01");
current = await records.updateWithETag("MiXeDNote01", {title:"first update"}, current.etag);
let stale = false; try { await records.update("MiXeDNote01", {title:"stale"}, "\"stale\""); } catch { stale = true; }
if (!stale || (await records.get("MiXeDNote01")).title !== "first update") throw new Error("text id update conflict changed the record");
current = await records.getWithETag("MiXeDNote01");
const retried = await records.updateWithETag("MiXeDNote01", {title:"retried"}, current.etag);
if (retried.record.id !== "MiXeDNote01" || retried.record.title !== "retried") throw new Error("text id update retry failed");
const alpha = await records.create({id:"AlphaNote01",title:"alpha",tags:[tag.id]}, "sdk-text-note-alpha-0001");
const filtered = await records.getList(1, 30, {filter:client.filter("id = {:id}", {id:"MiXeDNote01"}), sort:"id"});
if (filtered.page !== 1 || filtered.perPage !== 30 || filtered.totalItems !== 1 || filtered.totalPages !== 1 || filtered.items.length !== 1 || filtered.items[0].id !== "MiXeDNote01") throw new Error("text id PocketBase filter/page envelope mismatch");
const sorted = await records.getList(1, 30, {sort:"id"});
if (sorted.items.map((item)=>item.id).join(",") !== [alpha.id,created.id].sort().join(",")) throw new Error("text id sort did not use public ids");
const files = client.files("text_notes", "MiXeDNote01");
const uploaded = await files.upload("attachment", new Blob(["text identity file"], {type:"text/plain"}), "text-id.txt");
const token = await files.mintDownloadToken(uploaded.id);
if (!(await fetch(files.downloadURL(uploaded.id, token.token), {headers:{Authorization:"Bearer " + client.auth.token}})).ok) throw new Error("text id file URL did not resolve");
const events = records.events({lastEventId:0,maxReconnects:0,signal:AbortSignal.timeout(10000)});
const event = await events.next(); if (event.done || event.value.recordId !== "MiXeDNote01") throw new Error("text id record subscription lost public id");
const native = client.records("uuid_notes"); const uuidRecord = await native.create({title:"uuid"}, "sdk-native-uuid-create-0001");
if (!/^[0-9a-f]{8}-[0-9a-f]{4}-[1-8][0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$/i.test(uuidRecord.id) || (await native.get(uuidRecord.id)).id !== uuidRecord.id) throw new Error("native UUID collection regressed");
const targetClient = new PgBaseClient(baseURL); const targetRealm = targetClient.realm("sdk_text_auth");
const target = await targetRealm.register("sdk-text-target@example.test", "sdk text target password 0123456789");
if (!/^[a-z0-9]{15}$/.test(target.user.id) || target.user.emailVisibility !== false) throw new Error("text auth registration lost generated id or visibility");
const targetLogin = await targetRealm.login("sdk-text-target@example.test", "sdk text target password 0123456789");
if (targetLogin.user.id !== target.user.id || targetLogin.user.emailVisibility !== false) throw new Error("text auth login lost generated id or visibility");
if ((await targetRealm.session()).user.emailVisibility !== false) throw new Error("text auth session lost emailVisibility");
if ((await targetRealm.refresh()).user.emailVisibility !== false) throw new Error("text auth refresh lost emailVisibility");
const managerClient = new PgBaseClient(baseURL); const manager = await managerClient.realm("sdk_text_auth").login("sdk-text-manager@example.test", "user-one-fixture-passphrase-7281");
if (manager.user.id !== "ManagerCase01") throw new Error("mixed-case manager auth id lost in SDK session");
const managed = await fetch(baseURL + "/api/v1/auth/sdk_text_auth/records/" + encodeURIComponent(target.user.id), {headers:{Authorization:"Bearer " + manager.token}}).then(async (response)=>({status:response.status,body:await response.json()}));
if (managed.status !== 200 || managed.body.id !== target.user.id || managed.body.email !== "sdk-text-target@example.test" || managed.body.emailVisibility !== false) throw new Error("manager auth projection lost text id or email visibility");
const madeVisible = await fetch(baseURL + "/api/v1/auth/sdk_text_auth/records/" + encodeURIComponent(target.user.id), {method:"PATCH",headers:{Authorization:"Bearer " + manager.token,"content-type":"application/json"},body:JSON.stringify({emailVisibility:true})}).then(async (response)=>({status:response.status,body:await response.json()}));
if (madeVisible.status !== 200 || madeVisible.body.emailVisibility !== true) throw new Error("manager did not persist email visibility");
const visibleLogin = await targetRealm.login("sdk-text-target@example.test", "sdk text target password 0123456789");
if (visibleLogin.user.emailVisibility !== true || (await targetRealm.session()).user.emailVisibility !== true || (await targetRealm.refresh()).user.emailVisibility !== true) throw new Error("persisted email visibility was lost by auth session responses");
await native.remove(uuidRecord.id, (await native.getWithETag(uuidRecord.id)).etag);
await records.remove("MiXeDNote01", (await records.getWithETag("MiXeDNote01")).etag);
let deleted = false; try { await records.get("MiXeDNote01"); } catch { deleted = true; } if (!deleted) throw new Error("text id delete did not remove record");
await targetRealm.logout(); await managerClient.realm("sdk_text_auth").logout();`
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, "node", "--input-type=module", "--eval", script, app.server.URL)
	command.Dir = repo
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("TypeScript text identity HTTP conformance: %v\n%s", err, output)
	}
	dart := exec.CommandContext(ctx, "dart", "run", "tool/text_identity_http_conformance.dart")
	dart.Dir = filepath.Join(repo, "sdk/dart")
	dart.Env = append(os.Environ(), "PGBASE_SDK_URL="+app.server.URL, "PGBASE_TEXT_AUTH_REALM="+authRealm)
	if output, err := dart.CombinedOutput(); err != nil {
		t.Fatalf("Dart text identity HTTP conformance: %v\n%s", err, output)
	}
}
