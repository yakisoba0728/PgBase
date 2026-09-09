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

  if (request.url === "/api/v1/auth/login") {
    response.setHeader("content-type", "application/json");
    response.end(JSON.stringify({ token: "first-token", user: { id: "first", email: "first@example.test", createdAt: "2026-01-01T00:00:00Z" }, expiresAt: "2026-01-02T00:00:00Z" }));
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
  if (request.url === "/api/v1/auth/members/profile") {
	response.setHeader("content-type", "application/json"); response.end(JSON.stringify({ attributes: request.method === "PATCH" ? JSON.parse(body) : { nickname: "realm" } })); return;
  }
  if (request.url === "/api/v1/auth/members/request-password-reset") { response.statusCode = 204; response.end(); return; }
  if (request.url === "/api/v1/auth/members/oauth/start" || request.url === "/api/v1/auth/oauth/link") { response.setHeader("content-type", "application/json"); response.end(JSON.stringify({ authorizationUrl: "https://provider.test/authorize", expiresAt: "2026-01-02T00:00:00Z" })); return; }
  if (request.url === "/api/v1/auth/oauth/complete") { response.setHeader("content-type", "application/json"); response.end(JSON.stringify({ token: "oauth-token", user: { id: "oauth", email: "oauth@example.test", createdAt: "" }, expiresAt: "" })); return; }
  if (request.url.endsWith("/otp/request")) { response.setHeader("content-type", "application/json"); response.end(JSON.stringify({ otpId: "otp-id", expiresAt: "2026-01-02T00:00:00Z" })); return; }
  if (request.url.endsWith("/otp/complete")) { response.setHeader("content-type", "application/json"); response.end(JSON.stringify({ token: "otp-token", user: { id: "otp", email: "otp@example.test", createdAt: "" }, expiresAt: "" })); return; }
  if (request.url === "/api/v1/user/collections/batch") {
    response.setHeader("content-type", "application/json");
    const input=body.startsWith("{")?JSON.parse(body):{};
    if(input.requests){response.end(JSON.stringify({results:[{eventId:"00000000-0000-0000-0000-000000000005",status:200,headers:{ETag:'"pgb1.upsert"'},body:{id:"note-upsert",title:"upserted"},etag:'"pgb1.upsert"',version:1}]}));return;}
    response.end(JSON.stringify({ results: [
      { eventId: "00000000-0000-0000-0000-000000000003", record: { id: "note-3", title: "batched" }, etag: '"pgb1.batch"', version: 1 },
      { eventId: "00000000-0000-0000-0000-000000000004" },
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
  if (request.url?.startsWith("/api/v1/user/collections/notes/records") && request.method === "GET") {
    response.setHeader("content-type", "application/json");
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
  const session = await client.login("first@example.test", "correct horse battery staple");
  assert.equal(session.user.id, "first");
  assert.equal(client.auth.token, "first-token");

  const realm = client.realm("members");
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
  assert.equal(requests.at(-1).path, "/api/v1/auth/members/oauth/start");
  assert.deepEqual(JSON.parse(requests.at(-1).body), { provider: "github", redirectUri: "https://app.test/callback" });
  await realm.oauthLink("password", "github", "https://app.test/callback");
  assert.equal(requests.at(-1).headers.authorization, "Bearer realm-token");
  await realm.oauthComplete("github", "state", "code");
  assert.equal(client.auth.token, "oauth-token");
  await realm.oauthCompleteFromURL("github", "https://app.test/callback?code=browser-code&state=browser-state");
  assert.deepEqual(JSON.parse(requests.at(-1).body), { provider: "github", state: "browser-state", code: "browser-code" });
  assert.throws(() => realm.oauthCompleteFromURL("github", "https://app.test/callback?error=access_denied&error_description=Cancelled"), (error) => error.name === "OAuthCallbackError" && error.code === "access_denied" && error.message === "Cancelled");
  assert.throws(() => realm.oauthCompleteFromURL("github", "https://app.test/callback?state=only"), (error) => error.code === "invalid_callback");
  client.auth.save("first-token");

  const page = await client.records("notes").list({ filter: 'title = "private"', sort: "-createdAt", expand: "author", cursor: "current", limit: 20 });
  assert.equal(page.items[0].id, "note-1");
  const listRequest = requests.at(-1);
  assert.equal(listRequest.headers.authorization, "Bearer first-token");
  assert.match(listRequest.path, /filter=title/);
  assert.match(listRequest.path, /sort=-createdAt/);
  assert.match(listRequest.path, /expand=author/);
  assert.match(listRequest.path, /cursor=current/);

  const iterated = [];
  for await (const record of client.iterate("notes")) iterated.push(record.id);
  assert.deepEqual(iterated, ["note-1", "note-2"]);

  const batch = await client.batch([
    { operation: "create", collection: "notes", input: { title: "batched" }, idempotencyKey: "batch-create-0001" },
    { operation: "delete", collection: "notes", id: "note-2", ifMatch: '"pgb1.updated"' },
  ]);
  assert.equal(batch.results[0].etag, '"pgb1.batch"');
  assert.equal(batch.results[1].record, undefined, "delete results intentionally omit a record");
  assert.equal(JSON.parse(requests.at(-1).body).operations[1].operation, "delete");

  const generic=await client.batchRequests([{method:"PUT",url:"/api/v1/collections/notes/records/00000000-0000-4000-8000-000000000099",body:{title:"upserted"}}]);
  assert.equal(generic.results[0].status,200);assert.equal(generic.results[0].body.title,"upserted");

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
