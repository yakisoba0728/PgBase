import assert from "node:assert/strict";
import { once } from "node:events";
import { createServer } from "node:http";
import { test } from "node:test";
import { PgBaseClient, PgBaseError } from "../dist/index.js";

const recordJSON = '{"id":"CaseSensitive01","payload":{"large":9007199254740993,"decimal":0.12345678901234567890123456789,"nested":[1.23000000000000000000000000000001,1e-400],"literal":"9007199254740993"}}';
const pageJSON = `{"items":[${recordJSON}],"nextCursor":"ExactCursor01"}`;

test("raw record reads and writes preserve JSONB numbers across HTTP while parsed methods stay compatible", async (t) => {
  const calls = [];
  const server = createServer(async (request, response) => {
    let body = "";
    for await (const chunk of request) body += chunk;
    calls.push({ url: new URL(request.url, "http://localhost"), method: request.method, headers: request.headers, body });
    response.setHeader("Content-Type", "application/json");
    response.setHeader("ETag", '"pgb1.exact"');
    response.end(request.method === "GET" && calls.at(-1).url.pathname.endsWith("/records") ? pageJSON : recordJSON);
  });
  server.listen(0, "127.0.0.1");
  await once(server, "listening");
  t.after(() => new Promise((resolve) => { server.close(resolve); server.closeAllConnections(); }));
  const client = new PgBaseClient(`http://127.0.0.1:${server.address().port}`);
  client.auth.save("record-token");
  const records = client.records("precise data");

  const parsed = await records.get("CaseSensitive01");
  assert.equal(String(parsed.payload.large), "9007199254740992");
  assert.equal(String(parsed.payload.decimal), "0.12345678901234568");
  assert.equal(parsed.payload.nested[1], 0);
  assert.equal((await records.list()).items[0].payload.large, parsed.payload.large);

  const read = await records.getJSON("CaseSensitive01", { fields: "id,payload", expand: "owner" });
  assert.deepEqual(read, { recordJSON, etag: '"pgb1.exact"' });
  assert.equal(calls.at(-1).url.pathname, "/api/v1/user/collections/precise%20data/records/CaseSensitive01");
  assert.equal(calls.at(-1).url.searchParams.get("fields"), "id,payload");
  assert.equal(calls.at(-1).url.searchParams.get("expand"), "owner");
  const query = { fields: "id,payload", expand: "owner", filter: 'id = "CaseSensitive01"', sort: "id", cursor: "Case+Cursor/01", limit: 2 };
  assert.equal(await records.listJSON(query), pageJSON);
  assert.deepEqual(Object.fromEntries(calls.at(-1).url.searchParams), { ...query, limit: "2" });

  const created = await records.createJSON(recordJSON, "create-precise-01", { fields: "id,payload" });
  assert.deepEqual(created, read);
  const firstCreate = calls.at(-1);
  assert.equal(firstCreate.method, "POST");
  assert.equal(firstCreate.body, recordJSON);
  assert.equal(firstCreate.headers["idempotency-key"], "create-precise-01");
  assert.equal(firstCreate.url.searchParams.get("fields"), "id,payload");
  await records.createJSON(recordJSON, "create-precise-01", { fields: "id,payload" });
  assert.equal(calls.at(-1).body, firstCreate.body);
  assert.equal(calls.at(-1).headers["idempotency-key"], firstCreate.headers["idempotency-key"]);

  const patchJSON = '{"payload":{"large":9007199254740993,"decimal":0.12345678901234567890123456789}}';
  const updated = await records.updateJSON("CaseSensitive01", patchJSON, created.etag, { fields: "id,payload" });
  assert.deepEqual(updated, read);
  assert.equal(calls.at(-1).method, "PATCH");
  assert.equal(calls.at(-1).body, patchJSON);
  assert.equal(calls.at(-1).headers["if-match"], created.etag);
  assert.equal(calls.at(-1).url.searchParams.get("fields"), "id,payload");

  await records.create({ id: "AnotherCaseID", payload: { count: 3 } }, "create-text-id-01");
  assert.equal(JSON.parse(calls.at(-1).body).id, "AnotherCaseID");
  for (const call of calls) {
    assert.equal(call.headers.authorization, "Bearer record-token");
    assert.equal(call.headers.accept, "application/json");
    if (call.method !== "GET") assert.equal(call.headers["content-type"], "application/json");
  }
});

test("an uncertain raw create is not retried automatically and an explicit retry reuses exact input", async () => {
  const calls = [];
  const lostResponse = new TypeError("response lost after server commit");
  const client = new PgBaseClient("http://localhost", { fetch: async (_url, init) => {
    calls.push(init);
    if (calls.length === 1) throw lostResponse;
    return new Response(recordJSON, { headers: { ETag: '"pgb1.replayed"' } });
  } });
  const records = client.records("precise");
  await assert.rejects(records.createJSON(recordJSON, "stable-create-identity"), (error) => error === lostResponse);
  assert.equal(calls.length, 1);
  assert.deepEqual(await records.createJSON(recordJSON, "stable-create-identity"), { recordJSON, etag: '"pgb1.replayed"' });
  assert.equal(calls[0].body, recordJSON);
  assert.equal(calls[1].body, calls[0].body);
  assert.equal(calls[0].headers.get("Idempotency-Key"), calls[1].headers.get("Idempotency-Key"));
});

test("raw methods keep structured server errors and the current ETag", async () => {
  let requests = 0;
  const client = new PgBaseClient("http://localhost", { fetch: async () => {
    requests += 1;
    return Response.json({ error: { code: "etag_mismatch", details: { record: ["changed"] } } }, { status: 412, headers: { ETag: '"pgb1.current"' } });
  } });
  const records = client.records("precise");
  for (const request of [
    () => records.listJSON(),
    () => records.getJSON("CaseSensitive01"),
    () => records.createJSON(recordJSON, "create-failure-01"),
    () => records.updateJSON("CaseSensitive01", recordJSON, '"pgb1.stale"'),
  ]) {
    await assert.rejects(request(), (error) => error instanceof PgBaseError && error.status === 412 && error.code === "etag_mismatch" && error.etag === '"pgb1.current"' && error.details.record[0] === "changed");
  }
  assert.equal(requests, 4, "mutations and failed reads must not be silently replayed");
});

test("raw methods forward both signal argument forms and reject cancellation", async () => {
  const signals = [];
  const client = new PgBaseClient("http://localhost", { fetch: (_url, init) => new Promise((_resolve, reject) => {
    signals.push(init.signal);
    const abort = () => reject(new DOMException("aborted", "AbortError"));
    if (init.signal.aborted) abort();
    else init.signal.addEventListener("abort", abort, { once: true });
  }) });
  const records = client.records("precise");
  const requests = [
    (signal) => records.listJSON({}, signal),
    (signal) => records.getJSON("CaseSensitive01", signal),
    (signal) => records.getJSON("CaseSensitive01", { fields: "id,payload" }, signal),
    (signal) => records.createJSON(recordJSON, "create-cancel-01", signal),
    (signal) => records.createJSON(recordJSON, "create-cancel-01", { fields: "id,payload" }, signal),
    (signal) => records.updateJSON("CaseSensitive01", recordJSON, '"pgb1.exact"', signal),
    (signal) => records.updateJSON("CaseSensitive01", recordJSON, '"pgb1.exact"', { fields: "id,payload" }, signal),
  ];
  for (const request of requests) {
    const controller = new AbortController();
    const pending = request(controller.signal);
    controller.abort();
    assert.equal(signals.at(-1).aborted, true);
    await assert.rejects(pending, { name: "AbortError" });
    await assert.rejects(request(controller.signal), { name: "AbortError" });
  }
  assert.equal(signals.length, requests.length * 2);
});

test("a raw response body interrupted after headers rejects instead of returning partial JSON", async () => {
  let reading;
  const started = new Promise((resolve) => { reading = resolve; });
  const client = new PgBaseClient("http://localhost", { fetch: async (_url, init) => new Response(new ReadableStream({
    start(controller) {
      controller.enqueue(new TextEncoder().encode('{"payload":'));
      init.signal.addEventListener("abort", () => controller.error(new DOMException("aborted", "AbortError")), { once: true });
      reading();
    },
  })) });
  const controller = new AbortController();
  const pending = client.records("precise").getJSON("CaseSensitive01", controller.signal);
  await started;
  controller.abort();
  await assert.rejects(pending, { name: "AbortError" });
});
