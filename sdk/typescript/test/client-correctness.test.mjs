import assert from "node:assert/strict";
import { test } from "node:test";
import { PgBaseAdminClient, PgBaseClient, PgBaseError } from "../dist/index.js";

test("auto cancellation retains body reads, leaves mutations independent and respects the global switch", async () => {
  const calls = [];
  const client = new PgBaseClient("http://pgbase.test", { fetch: async (_url, init) => new Response(new ReadableStream({
    start(controller) {
      calls.push({ signal: init.signal, finish: () => { controller.enqueue(new TextEncoder().encode('{"id":"one"}')); controller.close(); } });
      init.signal?.addEventListener("abort", () => controller.error(new DOMException("aborted", "AbortError")), { once: true });
    },
  })) });
  const records = client.records("notes");
  const older = records.get("one", { requestKey: "editor" });
  const olderFailed = assert.rejects(older, { name: "AbortError" });
  await Promise.resolve();
  const newer = records.getJSON("one", { requestKey: "editor" });
  const newerFailed = assert.rejects(newer, { name: "AbortError" });
  await olderFailed;
  client.cancelRequest("editor"); // older cleanup must not remove the newer registration.
  await newerFailed;
  const create = records.create({}, "create-0001");
  const another = records.create({}, "create-0002");
  client.autoCancellation = false;
  const first = records.get("one");
  const second = records.get("one");
  for (const call of calls.slice(2)) { assert.equal(call.signal?.aborted ?? false, false); call.finish(); }
  await Promise.all([create, another, first, second]);
});

test("mutations participate only with an explicit cancellation key", async () => {
  const client = new PgBaseClient("http://pgbase.test", { fetch: (_url, init) => new Promise((_resolve, reject) => {
    init.signal.addEventListener("abort", () => reject(new DOMException("aborted", "AbortError")), { once: true });
  }) });
  const result = client.records("notes").createJSON('{}', "create-0001", { requestKey: "write" });
  const failed = assert.rejects(result, { name: "AbortError" });
  client.cancelRequest("write");
  await failed;
});

test("parsed record reads reject a response body interruption instead of succeeding without a record", async () => {
  for (const method of ["get", "list"]) {
    for (const failure of [new DOMException("aborted", "AbortError"), new TypeError("connection lost while reading")]) {
      const client = new PgBaseClient("http://pgbase.test", { fetch: async () => new Response(new ReadableStream({
        start(controller) {
          controller.enqueue(new TextEncoder().encode('{"id":'));
          controller.error(failure);
        },
      })) });
      const records = client.records("notes");
      await assert.rejects(method === "get" ? records.get("one") : records.list(), (error) => error === failure);
    }
  }
});

test("record reads accept explicit cancellation keys and opt-out without putting keys in the URL", async () => {
  const calls = [];
  const client = new PgBaseClient("http://pgbase.test", { fetch: (url, init) => new Promise((resolve, reject) => {
    calls.push({ url: String(url), finish: () => resolve(Response.json({ id: "one", items: [] })) });
    init.signal?.addEventListener("abort", () => reject(new DOMException("aborted", "AbortError")), { once: true });
  }) });
  const keyed = client.records("notes").get("one", { requestKey: "editor" });
  const rejected = assert.rejects(keyed, { name: "AbortError" });
  client.cancelRequest("editor");
  const first = client.records("notes").list({ requestKey: null });
  const second = client.records("notes").list({ requestKey: null });
  calls[1].finish();
  calls[2].finish();
  assert.equal(calls.some((call) => call.url.includes("requestKey")), false);
  await Promise.all([rejected, first, second]);
});

test("public and administrator clients retain transport failures while reading HTTP error bodies", async () => {
  for (const status of [200, 400]) {
    const failure = new TypeError("connection lost while reading");
    const fetch = async () => new Response(new ReadableStream({ start(controller) { controller.error(failure); } }), { status });
    const clients = [new PgBaseClient("http://pgbase.test", { fetch }), new PgBaseAdminClient("http://pgbase.test", { fetch })];
    for (const client of clients) {
      await assert.rejects(client instanceof PgBaseClient ? client.send("/status") : client.catalog(), (error) => error === failure);
    }
  }
});

test("a malformed success body rejects with a protocol error while 204 remains an empty success", async () => {
  const broken = new PgBaseClient("http://pgbase.test", { fetch: async () => new Response("not json") });
  await assert.rejects(broken.records("notes").get("one"), (error) => error instanceof PgBaseError && error.code === "invalid_response_body");
  const empty = new PgBaseClient("http://pgbase.test", { fetch: async () => new Response(null, { status: 204 }) });
  assert.equal(await empty.records("notes").remove("one", '"pgb1.current"'), undefined);
});

test("duplicate service reads cancel the older full request while different queries remain independent", async () => {
  const pending = [];
  const client = new PgBaseClient("http://pgbase.test", { fetch: (_url, init) => new Promise((resolve, reject) => {
    const request = { complete: () => resolve(Response.json({ items: [], id: "one" })), aborted: false };
    pending.push(request);
    init.signal?.addEventListener("abort", () => { request.aborted = true; reject(new DOMException("aborted", "AbortError")); }, { once: true });
  }) });
  const first = client.records("notes").list({ filter: "score > 1" });
  const firstFailed = assert.rejects(first, { name: "AbortError" });
  const other = client.records("notes").list({ filter: "score > 2" });
  const latest = client.records("notes").list({ filter: "score > 1" });
  assert.equal(pending[0].aborted, true);
  assert.equal(pending[1].aborted, false);
  pending[1].complete();
  pending[2].complete();
  await Promise.all([firstFailed, other, latest]);
});

test("record events respect prior cancellation and close their connection on iterator return", async () => {
  let calls = 0;
  let connectionSignal;
  const client = new PgBaseClient("http://pgbase.test", { fetch: async (_url, init) => {
    calls += 1;
    connectionSignal = init.signal;
    return new Response(new ReadableStream({
      start(controller) {
        controller.enqueue(new TextEncoder().encode('id: 1\nevent: record\ndata: {"eventId":"one"}\n\n'));
        init.signal.addEventListener("abort", () => controller.error(new DOMException("aborted", "AbortError")), { once: true });
      },
    }));
  } });
  const cancelled = client.records("notes").events({ signal: AbortSignal.abort() });
  assert.deepEqual(await cancelled.next(), { value: undefined, done: true });
  assert.equal(calls, 0);

  const signal = new AbortController().signal;
  let listeners = 0;
  const add = signal.addEventListener.bind(signal);
  const remove = signal.removeEventListener.bind(signal);
  signal.addEventListener = (...args) => { listeners += 1; add(...args); };
  signal.removeEventListener = (...args) => { listeners -= 1; remove(...args); };
  const events = client.records("notes").events({ signal });
  assert.equal((await events.next()).value.deliveryId, "1");
  assert.equal(connectionSignal.aborted, false);
  await events.return();
  assert.equal(connectionSignal.aborted, true);
  assert.equal(listeners, 0);
});

test("invalid record event cursors do not register auth listeners", async () => {
  const client = new PgBaseClient("http://pgbase.test");
  let subscriptions = 0;
  client.auth._onChange = () => { subscriptions += 1; return () => {}; };
  await assert.rejects(client.records("notes").events({ lastEventId: "invalid" }).next(), { code: "invalid_cursor" });
  assert.equal(subscriptions, 0);
});

test("empty record event streams exhaust the configured reconnect budget", async () => {
  for (const maxReconnects of [0, 2]) {
    let calls = 0;
    const client = new PgBaseClient("http://pgbase.test", { fetch: async () => {
      calls += 1;
      return new Response("");
    } });
    await assert.rejects(client.records("notes").events({ maxReconnects, reconnectDelayMs: 0 }).next(), { code: "realtime_stream_ended" });
    assert.equal(calls, maxReconnects + 1);
  }
});

test("record event reconnect policies reject invalid values before connecting", async () => {
  let calls = 0;
  const client = new PgBaseClient("http://pgbase.test", { fetch: async () => { calls += 1; return new Response(""); } });
  for (const options of [
    ...[NaN, Infinity, -1, 0.5].map((maxReconnects) => ({ maxReconnects })),
    ...[NaN, Infinity, -1].map((reconnectDelayMs) => ({ reconnectDelayMs })),
  ]) {
    await assert.rejects(client.records("notes").events(options).next(), { code: "invalid_reconnect_options" });
  }
  assert.equal(calls, 0);
});

test("record event terminal HTTP errors do not reconnect", async () => {
  for (const [status, code] of [[400, "request_failed"], [401, "request_failed"], [403, "request_failed"], [410, "request_failed"], [500, "cursor_expired"]]) {
    let calls = 0;
    const client = new PgBaseClient("http://pgbase.test", { fetch: async () => {
      calls += 1;
      return Response.json({ error: { code } }, { status });
    } });
    await assert.rejects(client.records("notes").events({ maxReconnects: 3, reconnectDelayMs: 0 }).next(), { status, code });
    assert.equal(calls, 1);
  }
});

test("empty record IDs never request the collection list", async () => {
  const paths = [];
  const client = new PgBaseClient("http://pgbase.test", { fetch: async (url) => {
    paths.push(new URL(url).pathname);
    return Response.json({ error: { code: "not_found" } }, { status: 404 });
  } });
  const records = client.records("notes");
  for (const read of [() => records.get(""), () => records.getJSON("")]) await assert.rejects(read(), { code: "not_found" });
  assert.deepEqual(paths, ["/api/v1/user/collections/notes/records/", "/api/v1/user/collections/notes/records/"]);
});

test("stale auth revocation responses preserve a newer saved session", async () => {
  const actions = [
    (client) => client.logout(),
    (client) => client.realm("members").logout(),
    (client) => client.changePassword("old", "new"),
    (client) => client.confirmEmailChange("confirmation"),
  ];
  for (const action of actions) {
    for (const status of [204, 500]) {
      for (const replace of [false, true]) {
        let finish;
        const client = new PgBaseClient("http://pgbase.test", { fetch: () => new Promise((resolve) => { finish = resolve; }) });
        client.auth.save("old", { id: "old" });
        const pending = action(client);
        const settled = status === 500 ? assert.rejects(pending, { status }) : pending;
        if (replace) client.auth.save("new", { id: "new" });
        finish(status === 204 ? new Response(null, { status }) : Response.json({ error: { code: "failed" } }, { status }));
        await settled;
        assert.equal(client.auth.token, replace ? "new" : null);
        assert.deepEqual(client.auth.record, replace ? { id: "new" } : null);
      }
    }
  }
});

test("OAuth cancelled while opening never starts the callback adapter", async () => {
  const controller = new AbortController();
  let callbacks = 0;
  let closed = false;
  const client = new PgBaseClient("http://pgbase.test", { fetch: async () => Response.json({ authorizationUrl: "https://provider.test/auth?state=expected", expiresAt: "later" }) });
  await assert.rejects(client.realm("members").oauthWithBrowser("provider", "https://app.test/callback", {
    open() { controller.abort(); },
    waitForCallback() { callbacks += 1; return Promise.reject(new Error("adapter aborted")); },
    close() { closed = true; },
  }, controller.signal), { name: "AbortError" });
  assert.equal(callbacks, 0);
  assert.equal(closed, true);
});

test("OAuth removes cancellation listeners after a callback completes", async () => {
  const controller = new AbortController();
  const signal = controller.signal;
  let listeners = 0;
  const add = signal.addEventListener.bind(signal);
  const remove = signal.removeEventListener.bind(signal);
  signal.addEventListener = (...args) => { listeners += 1; add(...args); };
  signal.removeEventListener = (...args) => { listeners -= 1; remove(...args); };
  const client = new PgBaseClient("http://pgbase.test", { fetch: async (url) => Response.json(new URL(url).pathname.endsWith("/start")
    ? { authorizationUrl: "https://provider.test/auth?state=expected", expiresAt: "later" }
    : { token: "session", user: { id: "one" } }) });
  await client.realm("members").oauthWithBrowser("provider", "https://app.test/callback", {
    open() {}, waitForCallback: async () => "https://app.test/callback?state=expected&code=code",
  }, signal);
  assert.equal(listeners, 0);
});
