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
