import assert from "node:assert/strict";
import { createServer } from "node:http";
import { once } from "node:events";
import { AuthStore, MFAChallengeError, PgBaseClient, PgBaseError, browserTokenStorage } from "../dist/index.js";

const requests = [];
let eventConnections = 0;
const server = createServer(async (request, response) => {
  const body = await new Promise((resolve) => {
    let value = "";
    request.on("data", (chunk) => { value += chunk; });
    request.on("end", () => resolve(value));
  });
  requests.push({ method: request.method, path: request.url, headers: request.headers, body });

  if (request.url?.startsWith("/api/v1/auth/login")) {
    response.setHeader("content-type", "application/json");
    response.end(JSON.stringify({ token: "first-token", user: { id: "first", email: "first@example.test", createdAt: "2026-01-01T00:00:00Z" }, expiresAt: "2026-01-02T00:00:00Z" }));
    return;
  }
  if (request.url === "/api/realtime" && request.method === "GET") {
    response.setHeader("content-type", "text/event-stream");
    response.write('event: PB_CONNECT\ndata: {"clientId":"application-test"}\n\n');
    setTimeout(() => response.write('event: @app/Y3VzdG9tLXRvcGlj\ndata: {"ok":true,"nested":[1,2]}\n\n'), 5);
    return;
  }
  if (request.url === "/api/realtime" && request.method === "POST") {
    response.statusCode = 204;
    response.end();
    return;
  }
  if (request.url === "/api/v1/auth/members/login") {
    response.statusCode = 401; response.setHeader("content-type", "application/json");
    response.end(JSON.stringify({ error: { code: "mfa_required", details: { challengeToken: ["realm-challenge"], expiresAt: ["2026-01-02T00:00:00Z"] } } })); return;
  }
  if (request.url === "/api/v1/auth/members/register") {
    response.statusCode = 201; response.setHeader("content-type", "application/json");
    response.end(JSON.stringify({ token: "realm-token", user: { id: "realm", email: "realm@example.test", verified: true, authCollection: "members", createdAt: "" }, expiresAt: "" })); return;
  }
  if (request.url === "/api/v1/auth/members/methods") {
    response.setHeader("content-type", "application/json");
    response.end(JSON.stringify({ passwordEnabled: true, otpEnabled: true, mfaEnabled: false, oauthProviders: ["github"] })); return;
  }
  if (request.url === "/api/v1/auth/members/refresh") {
    response.setHeader("content-type", "application/json");
    response.end(JSON.stringify({ token: "rotated-token", user: { id: "realm", email: "realm@example.test", createdAt: "" }, expiresAt: "2026-01-02T00:00:00Z" })); return;
  }
  if (request.url === "/api/v1/auth/invalid/refresh") {
    response.setHeader("content-type", "application/json");
    response.end(JSON.stringify({ token: "must-not-save" })); return;
  }
  if (request.url === "/api/v1/auth/members/profile") {
	response.setHeader("content-type", "application/json"); response.end(JSON.stringify({ attributes: request.method === "PATCH" ? JSON.parse(body) : { nickname: "realm" } })); return;
  }
  if (request.url === "/api/v1/auth/members/request-password-reset") { response.statusCode = 204; response.end(); return; }
  if (request.url === "/api/v1/auth/members/oauth/start" || request.url === "/api/v1/auth/oauth/link") { response.setHeader("content-type", "application/json"); response.end(JSON.stringify({ authorizationUrl: "https://provider.test/authorize?state=expected-state", expiresAt: "2026-01-02T00:00:00Z" })); return; }
  if (request.url === "/api/v1/auth/oauth/complete") { response.setHeader("content-type", "application/json"); response.end(JSON.stringify({ token: "oauth-token", user: { id: "oauth", email: "oauth@example.test", createdAt: "" }, expiresAt: "" })); return; }
  if (request.url.endsWith("/otp/request")) { response.setHeader("content-type", "application/json"); response.end(JSON.stringify({ otpId: "otp-id", expiresAt: "2026-01-02T00:00:00Z" })); return; }
  if (request.url.endsWith("/otp/complete")) { response.setHeader("content-type", "application/json"); response.end(JSON.stringify({ token: "otp-token", user: { id: "otp", email: "otp@example.test", createdAt: "" }, expiresAt: "" })); return; }
  if (request.url === "/api/v1/user/collections/batch") {
    response.setHeader("content-type", "application/json");
    const input=body.startsWith("{")?JSON.parse(body):{};
    if(input.requests){response.end(JSON.stringify({results:[{eventId:"00000000-0000-0000-0000-000000000005",status:200,headers:{ETag:'"pgb1.upsert"'},body:{id:"note-upsert",title:"upserted"},etag:'"pgb1.upsert"',version:1}]}));return;}
    response.end(JSON.stringify({ results: [
      { eventId: "00000000-0000-0000-0000-000000000003", record: { id: "note-3", title: "batched" }, etag: '"pgb1.batch"', version: 1 },
      { eventId: "00000000-0000-0000-0000-000000000004", record: { id: "00000000-0000-4000-8000-000000000099", title: "upserted" }, etag: '"pgb1.upsert"', version: 2 },
      { eventId: "00000000-0000-0000-0000-000000000005" },
    ] }));
    return;
  }
  if (request.url === "/api/v1/auth/change-password") {
    response.statusCode = 204;
    response.end();
    return;
  }
  if ([
    "/api/v1/auth/request-password-reset",
    "/api/v1/auth/confirm-password-reset",
    "/api/v1/auth/request-verification",
    "/api/v1/auth/confirm-verification",
    "/api/v1/auth/request-email-change",
    "/api/v1/auth/confirm-email-change",
  ].includes(request.url)) {
    response.statusCode = 204;
    response.end();
    return;
  }
  if (request.url === "/api/v1/user/collections/notes/events") {
    eventConnections += 1;
    response.setHeader("content-type", "text/event-stream");
    if (eventConnections === 1) {
      response.end('event: cursor\nid: 0\ndata: {}\n\nevent: record\nid: 1\ndata: {"eventId":"00000000-0000-0000-0000-000000000001","collection":"notes","recordId":"note-1","operation":"create","version":1}\n\n');
      return;
    }
	if (eventConnections === 3) {
	  response.end('event: record\nid: 9007199254740993\ndata: {"eventId":"00000000-0000-0000-0000-000000000005","collection":"notes","recordId":"note-5","operation":"update","version":5}\n\n');
	  return;
	}
    response.end('event: record\nid: 1\ndata: {"eventId":"00000000-0000-0000-0000-000000000001","collection":"notes","recordId":"note-1","operation":"create","version":1}\n\nevent: cursor\nid: 1\ndata: {}\n\nevent: record\nid: 2\ndata: {"eventId":"00000000-0000-0000-0000-000000000002","collection":"notes","recordId":"note-2","operation":"update","version":2}\n\n');
    return;
  }
  if (request.url?.startsWith("/api/v1/user/collections/notes/records/note-1") && request.method === "GET") {
    response.setHeader("content-type", "application/json");
    response.end(JSON.stringify({ id: "note-1", title: "private" }));
    return;
  }
  if (request.url?.startsWith("/api/v1/user/collections/notes/records") && request.method === "GET") {
    response.setHeader("content-type", "application/json");
    if (new URL(request.url, "http://test").searchParams.get("compat") === "pocketbase") {
      response.end(JSON.stringify({ page: 2, perPage: 25, totalItems: -1, totalPages: -1, items: [{ id: "note-pb", title: "paged" }] }));
      return;
    }
    const cursor = new URL(request.url, "http://test").searchParams.get("cursor");
    response.end(JSON.stringify(cursor === "next" ? { items: [{ id: "note-2", title: "second" }] } : { items: [{ id: "note-1", title: "private" }], nextCursor: "next" }));
    return;
  }
  if (request.url === "/api/v1/user/collections/notes/records" && request.method === "POST") {
    response.setHeader("content-type", "application/json");
	response.setHeader("ETag", '"pgb1.created"');
    response.end(JSON.stringify({ id: "note-2", title: "created" }));
    return;
  }
  if (request.url === "/api/v1/user/collections/notes/records/note-2" && request.method === "PATCH") {
    response.setHeader("content-type", "application/json");
	response.setHeader("ETag", '"pgb1.updated"');
    response.end(JSON.stringify({ id: "note-2", title: "updated" }));
    return;
  }
  if (request.url === "/api/v1/user/collections/notes/records/note-2" && request.method === "DELETE") {
    response.statusCode = 204;
    response.end();
    return;
  }
  if (request.url === "/failure") {
    response.statusCode = 422;
    response.setHeader("content-type", "application/json");
    response.end(JSON.stringify({ error: { code: "invalid_record", details: { title: ["required"] }, message: "sensitive server text" } }));
    return;
  }
  response.statusCode = 404;
  response.end();
});

server.listen(0, "127.0.0.1");
await once(server, "listening");
const { port } = server.address();
const client = new PgBaseClient(`http://127.0.0.1:${port}`);

try {
  const repeated = client.filter('title = {:title} || summary = {:title}', { title: 'quoted "value"\\next\nline' });
  assert.equal(repeated, 'title = "quoted \\"value\\"\\\\next\\nline" || summary = "quoted \\"value\\"\\\\next\\nline"');
  assert.equal(client.filter('created >= {:when}', { when: new Date('2026-01-02T03:04:05.000Z') }), 'created >= "2026-01-02 03:04:05.000Z"');
  assert.equal(client.filter('flags = {:value}', { value: [true, 'two'] }), 'flags = "[true,\\"two\\"]"');
  assert.equal(client.filter('meta = {:value}', { value: { flag: true } }), 'meta = "{\\"flag\\":true}"');
  assert.equal(client.filter('unset = {:value}', { value: undefined }), 'unset = null');
  assert.equal(client.filter('number = {:value}', { value: Number.NaN }), 'number = NaN');
  assert.equal(client.filter('number = {:value}', { value: Infinity }), 'number = Infinity');
  assert.equal(client.filter('known = {:known} && missing = {:missing}', { known: false }), 'known = false && missing = {:missing}');
  assert.equal(client.filter('value = {:text}', { text: '{:text} || title != "safe"' }), 'value = "{:text} || title != \\"safe\\""', 'substitution is a single pass');
  const applicationEvent = new Promise((resolve) => {
    client.realtime.subscribeApplication("custom-topic", async (event) => resolve(event));
  });
  assert.deepEqual(await applicationEvent, { topic: "custom-topic", data: { ok: true, nested: [1, 2] } });
  await client.realtime.unsubscribe();
  await assert.rejects(() => client.realtime.subscribeApplication("", () => {}), (error) => error instanceof PgBaseError && error.code === "invalid_realtime_topic");

  const session = await client.login("first@example.test", "correct horse battery staple");
  assert.equal(session.user.id, "first");
  assert.equal(client.auth.token, "first-token");
  assert.equal(client.auth.record?.id, "first");
	await client.login("first@example.test", "correct horse battery staple", { fields: "token,user.id" });
	assert.equal(requests.at(-1).path, "/api/v1/auth/login?fields=token%2Cuser.id");
  assert.equal(client.auth.isValid, true);
  const authCookie = client.auth.exportToCookie({ secure: true, sameSite: "Lax" });
  const restoredAuth = new AuthStore();
  restoredAuth.loadFromCookie(authCookie);
  assert.equal(restoredAuth.token, "first-token");
  assert.equal(restoredAuth.record?.email, "first@example.test");
  assert.throws(() => client.auth.exportToCookie({ path: "/\r\nInjected: value" }), PgBaseError);
  client.auth.save("different-token");
  assert.equal(client.auth.record, null, "a token without a record cannot retain another user's record");

  let aborted = 0;
  const keyed = new PgBaseClient("http://unused.test", { fetch: (_url, init) => new Promise((resolve, reject) => {
    if (init.signal?.aborted) { aborted += 1; reject(new DOMException("aborted", "AbortError")); return; }
    init.signal?.addEventListener("abort", () => { aborted += 1; reject(new DOMException("aborted", "AbortError")); }, { once: true });
    setTimeout(() => resolve(new Response(JSON.stringify({ ok: true }), { headers: { "content-type": "application/json" } })), 10);
  }) });
  const firstKeyed = keyed.send("/one", {}, { requestKey: "same" });
  const secondKeyed = keyed.send("/two", {}, { requestKey: "same" });
  await assert.rejects(firstKeyed, { name: "AbortError" });
  await secondKeyed;
  assert.equal(aborted, 1);
  const alreadyAborted = new AbortController(); alreadyAborted.abort();
  await assert.rejects(keyed.send("/three", {}, { requestKey: "abort", signal: alreadyAborted.signal }), { name: "AbortError" });
  keyed.autoCancellation = false;
  const unkeyedOne = keyed.send("/four", {}, { requestKey: "off" });
  const unkeyedTwo = keyed.send("/five", {}, { requestKey: "off" });
  await Promise.all([unkeyedOne, unkeyedTwo]);
  assert.equal(aborted, 2, "disabling auto cancellation never registers or aborts keyed requests");

  const realm = client.realm("members");
	const authMethods = await realm.methods();
	assert.deepEqual(authMethods, { passwordEnabled: true, otpEnabled: true, mfaEnabled: false, oauthProviders: ["github"], oauth2: { enabled: false, providers: [] } });
	assert.equal(authMethods.oauth2.enabled, false);
	assert.equal(requests.at(-1).path, "/api/v1/auth/members/methods");
	client.auth.save("refresh-source-token");
	const refreshed = await realm.refresh();
	assert.equal(refreshed.token, "rotated-token");
	assert.equal(client.auth.token, "rotated-token");
	assert.equal(requests.at(-1).headers.authorization, "Bearer refresh-source-token");
	client.auth.save("still-valid-locally");
	await assert.rejects(() => client.realm("invalid").refresh(), (error) => error instanceof PgBaseError && error.code === "invalid_session_response");
	assert.equal(client.auth.token, "still-valid-locally", "invalid refresh responses do not overwrite stored tokens");
	assert.equal((await client.requestOTP("first@example.test")).otpId, "otp-id");
	await client.completeOTP("otp-id", "123456");
	assert.equal(client.auth.token, "otp-token");
	client.auth.save("first-token");
  const realmSession = await realm.register("realm@example.test", "password");
  assert.equal(realmSession.user.verified, true);
  assert.equal(realmSession.user.authCollection, "members");
  assert.equal(requests.at(-1).path, "/api/v1/auth/members/register");
  await assert.rejects(() => realm.login("realm@example.test", "password"), (error) => error instanceof MFAChallengeError && error.challenge.challengeToken === "realm-challenge");
  assert.deepEqual(await realm.profile(), { nickname: "realm" });
  await realm.updateProfile({ nickname: "updated" });
  assert.deepEqual(JSON.parse(requests.at(-1).body), { nickname: "updated" });
  await realm.requestPasswordReset("realm@example.test");
  assert.equal(requests.at(-1).path, "/api/v1/auth/members/request-password-reset");
  assert.equal((await realm.requestOTP("realm@example.test")).otpId, "otp-id");
  assert.deepEqual(JSON.parse(requests.at(-1).body), { email: "realm@example.test" });
  await realm.completeOTP("otp-id", "123456");
  assert.equal(client.auth.token, "otp-token");
  client.auth.save("realm-token");
  await realm.oauthStart("github", "https://app.test/callback");
  let openedOAuthURL = "";
  await realm.oauthStartAndOpen("github", "https://app.test/callback", { open: (url) => { openedOAuthURL = url; } });
  assert.equal(openedOAuthURL, "https://provider.test/authorize?state=expected-state");
  let closedOAuth = 0;
  const browserSession = await realm.oauthWithBrowser("github", "https://app.test/callback", {
    open: () => {}, waitForCallback: async () => "https://app.test/callback?state=expected-state&code=browser-code", close: () => { closedOAuth += 1; },
  });
  assert.equal(browserSession.token, "oauth-token");
  assert.equal(closedOAuth, 1);
  await assert.rejects(() => realm.oauthWithBrowser("github", "https://app.test/callback", {
    open: () => {}, waitForCallback: async () => "https://app.test/callback?state=wrong&code=browser-code", close: () => { closedOAuth += 1; },
  }), (error) => error.code === "invalid_state");
  assert.equal(closedOAuth, 2);
  const abortOAuth = new AbortController();
  const pendingOAuth = realm.oauthWithBrowser("github", "https://app.test/callback", {
    open: () => {}, waitForCallback: () => new Promise(() => {}), close: () => { closedOAuth += 1; },
  }, abortOAuth.signal);
  abortOAuth.abort();
  await assert.rejects(pendingOAuth, { name: "AbortError" });
  assert.equal(closedOAuth, 3);
  assert.equal(requests.at(-1).path, "/api/v1/auth/members/oauth/start");
  assert.deepEqual(JSON.parse(requests.at(-1).body), { provider: "github", redirectUri: "https://app.test/callback" });
  await realm.oauthLink("password", "github", "https://app.test/callback");
  assert.equal(requests.at(-1).headers.authorization, "Bearer oauth-token");
  await realm.oauthComplete("github", "state", "code");
  assert.equal(client.auth.token, "oauth-token");
  await realm.oauthCompleteFromURL("github", "https://app.test/callback?code=browser-code&state=browser-state");
  assert.deepEqual(JSON.parse(requests.at(-1).body), { provider: "github", state: "browser-state", code: "browser-code" });
  assert.throws(() => realm.oauthCompleteFromURL("github", "https://app.test/callback?error=access_denied&error_description=Cancelled"), (error) => error.name === "OAuthCallbackError" && error.code === "access_denied" && error.message === "Cancelled");
  assert.throws(() => realm.oauthCompleteFromURL("github", "https://app.test/callback?state=only"), (error) => error.code === "invalid_callback");
  client.auth.save("first-token");

  const page = await client.records("notes").list({ filter: 'title = "private"', sort: "-createdAt", expand: "author", fields: "id,title", cursor: "current", limit: 20 });
  assert.equal(page.items[0].id, "note-1");
  const listRequest = requests.at(-1);
  assert.equal(listRequest.headers.authorization, "Bearer first-token");
  assert.match(listRequest.path, /filter=title/);
  assert.match(listRequest.path, /sort=-createdAt/);
  assert.match(listRequest.path, /expand=author/);
  assert.match(listRequest.path, /fields=id%2Ctitle/);
  assert.match(listRequest.path, /cursor=current/);

  const pocketBasePage = await client.records("notes").getList(2, 25, { filter: client.filter('title = {:title}', { title: "paged" }), sort: "@random", skipTotal: true });
  assert.deepEqual(pocketBasePage, { page: 2, perPage: 25, totalItems: -1, totalPages: -1, items: [{ id: "note-pb", title: "paged" }] });
  assert.match(requests.at(-1).path, /compat=pocketbase&page=2&perPage=25/);
  assert.match(requests.at(-1).path, /skipTotal=true/);

  const projected = await client.records("notes").get("note-1", { fields: "id,title", expand: "author" });
  assert.equal(projected.id, "note-1");
  const getRequest = requests.at(-1);
  assert.equal(getRequest.method, "GET");
  assert.match(getRequest.path, /records\/note-1\?expand=author&fields=id%2Ctitle$/);

  const iterated = [];
  for await (const record of client.iterate("notes")) iterated.push(record.id);
  assert.deepEqual(iterated, ["note-1", "note-2"]);

  const batch = await client.batch([
    { operation: "create", collection: "notes", input: { title: "batched" }, idempotencyKey: "batch-create-0001" },
    { operation: "upsert", collection: "notes", id: "00000000-0000-4000-8000-000000000099", input: { title: "upserted" }, ifMatch: '"pgb1.current"' },
    { operation: "delete", collection: "notes", id: "note-2", ifMatch: '"pgb1.updated"' },
  ]);
  assert.equal(batch.results[0].etag, '"pgb1.batch"');
  assert.equal(batch.results[1].record.title, "upserted");
  assert.equal(batch.results[2].record, undefined, "delete results intentionally omit a record");
  const operationRequest = requests.at(-1);
  assert.equal(operationRequest.method, "POST");
  assert.equal(JSON.parse(operationRequest.body).operations[1].operation, "upsert");

  const generic=await client.batchRequests([{method:"PUT",url:"/api/v1/collections/notes/records/00000000-0000-4000-8000-000000000099",body:{title:"upserted"}}]);
  assert.equal(generic.results[0].status,200);assert.equal(generic.results[0].body.title,"upserted");
  const genericRequest = requests.at(-1);
  assert.equal(genericRequest.method, "POST");
  assert.deepEqual(JSON.parse(genericRequest.body), { requests: [{ method: "PUT", url: "/api/v1/collections/notes/records/00000000-0000-4000-8000-000000000099", body: { title: "upserted" } }] });

	const created = await client.records("notes").createWithETag({ title: "created" }, "create-note-0001");
	assert.equal(created.etag, '"pgb1.created"');
	assert.equal(created.record.title, "created");
  assert.equal(requests.at(-1).headers["idempotency-key"], "create-note-0001");
	const updated = await client.records("notes").updateWithETag("note-2", { title: "updated" }, '"pgb1.current"');
	assert.equal(updated.etag, '"pgb1.updated"');
  assert.equal(requests.at(-1).headers["if-match"], '"pgb1.current"');
  await client.records("notes").remove("note-2", '"pgb1.updated"');
  assert.equal(requests.at(-1).headers["if-match"], '"pgb1.updated"');

  await assert.rejects(
    () => client._request("/failure"),
    (error) => error instanceof PgBaseError && error.status === 422 && error.code === "invalid_record" && error.details.title[0] === "required" && !error.message.includes("sensitive"),
  );

  await client.changePassword("old password", "new password");
  assert.equal(client.auth.token, null, "password changes revoke the local token");
  assert.equal(JSON.parse(requests.at(-1).body).newPassword, "new password");

  client.auth.save("email-token");
  await client.requestPasswordReset("first@example.test");
  assert.deepEqual(JSON.parse(requests.at(-1).body), { email: "first@example.test" });
  await client.confirmPasswordReset("reset-token", "replacement password");
  assert.deepEqual(JSON.parse(requests.at(-1).body), { token: "reset-token", newPassword: "replacement password" });
  await client.requestVerification();
  assert.equal(requests.at(-1).headers.authorization, "Bearer email-token");
  await client.confirmVerification("verify-token");
  assert.deepEqual(JSON.parse(requests.at(-1).body), { token: "verify-token" });
  await client.requestEmailChange("replacement password", "new@example.test");
  assert.deepEqual(JSON.parse(requests.at(-1).body), { currentPassword: "replacement password", newEmail: "new@example.test" });
  await client.confirmEmailChange("email-token");
  assert.equal(client.auth.token, null, "email confirmation clears the local token");

  client.auth.save("event-token");
  const stream = client.records("notes").events({ reconnectDelayMs: 1 });
	assert.equal((await stream.next()).value.deliveryId, "1");
	assert.equal((await stream.next()).value.deliveryId, "2", "a resumed stream suppresses duplicate delivery");
  await stream.return();
  const resumedRequest = requests.findLast((entry) => entry.path === "/api/v1/user/collections/notes/events");
  assert.equal(resumedRequest.headers["last-event-id"], "1");

	const largeCursorStream = client.records("notes").events({ lastEventId: "9007199254740992", maxReconnects: 0 });
	assert.equal((await largeCursorStream.next()).value.deliveryId, "9007199254740993", "int64 delivery cursors remain exact strings");
	await largeCursorStream.return();
	assert.equal(requests.at(-1).headers["last-event-id"], "9007199254740992");
	await assert.rejects(
	  () => client.records("notes").events({ lastEventId: Number.MAX_SAFE_INTEGER + 1 }).next(),
	  (error) => error instanceof PgBaseError && error.code === "invalid_cursor",
	);

  const storageValues = new Map();
  const storage = browserTokenStorage({
    getItem: (key) => storageValues.get(key) ?? null,
    setItem: (key, value) => storageValues.set(key, value),
    removeItem: (key) => storageValues.delete(key),
    clear: () => storageValues.clear(),
    key: () => null,
    length: 0,
  });
  const browserStore = new AuthStore(storage);
  browserStore.save("browser-token");
  assert.equal(new AuthStore(storage).token, "browser-token", "explicit browser storage restores a token");
  browserStore.clear();
  assert.equal(storageValues.size, 0);

  const pending = new Promise((resolve) => {
    setTimeout(() => {
      resolve(new Response(JSON.stringify({
        token: "stale",
        user: { id: "stale", email: "stale@example.test", createdAt: "" },
        expiresAt: "",
      }), { headers: { "content-type": "application/json" } }));
    }, 10);
  });
  const fenced = new PgBaseClient("http://example.test", { fetch: () => pending });
  const staleLogin = fenced.login("stale@example.test", "password");
  fenced.auth.save("fresh");
  await staleLogin;
  assert.equal(fenced.auth.token, "fresh", "a late authentication response cannot replace a newer token");

	for (const [name, invoke] of [
	  ["realm authentication", (sdk) => sdk.realm("members").register("late@example.test", "password")],
	  ["OAuth completion", (sdk) => sdk.realm("members").oauthComplete("github", "state", "code")],
	  ["MFA completion", (sdk) => sdk.completeMFA("challenge", "123456")],
	]) {
	  const delayed = new Promise((resolve) => setTimeout(() => resolve(new Response(JSON.stringify({
		token: "late-token", user: { id: "late", email: "late@example.test", createdAt: "" }, expiresAt: "",
	  }), { headers: { "content-type": "application/json" } })), 10));
	  const guarded = new PgBaseClient("http://example.test", { fetch: () => delayed });
	  const request = invoke(guarded);
	  guarded.auth.save("newer-token");
	  await request;
	  assert.equal(guarded.auth.token, "newer-token", `${name} response cannot replace newer authentication`);
	}
} finally {
  server.close();
}
