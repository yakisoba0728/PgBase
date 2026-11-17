import assert from "node:assert/strict";
import { readFile } from "node:fs/promises";
import { test } from "node:test";
import vm from "node:vm";

const source = await readFile(new URL("../public/app.js", import.meta.url), "utf8");

function page(fetch) {
  const elements = new Map([
    ["#run", { addEventListener() {} }],
    ["#result", { dataset: {} }],
    ["#email", { value: "member@example.test" }],
    ["#password", { value: "disposable-test-password" }],
    ["#record-id", { value: "" }],
    ["#file-id", { value: "" }],
  ]);
  const context = vm.createContext({
    document: { querySelector: (selector) => elements.get(selector) },
    location: new URL("https://pgbase.test/?api=https%3A%2F%2Fattacker.test"),
    URLSearchParams,
    AbortController,
    TextDecoder,
    fetch,
  });
  vm.runInContext(source, context);
  return context;
}

test("a page URL cannot redirect entered login credentials to another API", async () => {
  const calls = [];
  const context = page(async (url, init) => {
    calls.push({ url, init });
    return new Response("refused", { status: 401 });
  });
  await vm.runInContext("run()", context);
  assert.equal(calls.length, 1);
  assert.equal(calls[0].url, "/api/v1/auth/login");
  assert.equal(JSON.parse(calls[0].init.body).password, "disposable-test-password");
});

test("a page URL cannot redirect realtime bearer credentials", async () => {
  const calls = [];
  const context = page(async (url, init) => {
    calls.push({ url, init });
    return new Response('event: record\ndata: {"eventId":"one"}\n\n');
  });
  await vm.runInContext('nextRecordEvent("private-token", async () => {})', context);
  assert.equal(calls[0].url, "/api/v1/user/collections/n6_tasks/events");
  assert.equal(calls[0].init.headers.Authorization, "Bearer private-token");
  assert.equal(calls[0].init.signal.aborted, true);
});

test("a failed mutation closes the already-open realtime connection", async () => {
  let signal;
  let cancelled = false;
  const body = new ReadableStream({ cancel() { cancelled = true; } });
  const context = page(async (_url, init) => {
    signal = init.signal;
    return new Response(body);
  });
  await assert.rejects(
    vm.runInContext('nextRecordEvent("private-token", async () => { throw new Error("write failed"); })', context),
    { message: "write failed" },
  );
  assert.equal(signal.aborted, true);
  assert.equal(cancelled, true);
  assert.equal(body.locked, false);
});

test("realtime HTTP refusals abort the connection", async () => {
  let signal;
  const context = page(async (_url, init) => {
    signal = init.signal;
    return new Response("refused", { status: 403 });
  });
  await assert.rejects(vm.runInContext('nextRecordEvent("private-token", async () => {})', context), { message: "SSE: 403" });
  assert.equal(signal.aborted, true);
});
