import assert from "node:assert/strict";
import { test } from "node:test";
import { PgBaseClient, PgBaseError } from "../dist/index.js";

test("PB_ERROR preserves terminal cursor failure codes before readiness", async () => {
  for (const code of ["cursor_expired", "invalid_cursor"]) {
    let connections = 0;
    const client = new PgBaseClient("http://pgbase.test", { fetch: async () => {
      connections++;
      return new Response(`event: PB_ERROR\ndata: {"code":"${code}"}\n\n`);
    } });
    await assert.rejects(client.realtime.subscribe("notes/*", () => {}, { maxReconnects: 0 }),
      (error) => error instanceof PgBaseError && error.code === code);
    assert.equal(connections, 1);
  }
});

test("PB_ERROR after connection stops retries while numeric checkpoints and overlapping topics remain valid", async () => {
  let connections = 0;
  const events = [];
  const client = new PgBaseClient("http://pgbase.test", { fetch: async (_url, init) => {
    if (init.method === "POST") return new Response(null, { status: 204 });
    connections++;
    return new Response('event: PB_CONNECT\ndata: {"clientId":"session"}\n\n' +
      'id: 9007199254740993\nevent: cursor\ndata: {}\n\n' +
      'id: 9007199254740993\nevent: notes/*\ndata: {"action":"create","record":{"id":"one"}}\n\n' +
      'id: 9007199254740993\nevent: notes/*\ndata: {"action":"update","record":{"id":"one"}}\n\n' +
      'event: PB_ERROR\ndata: {"code":"cursor_expired"}\n\n');
  } });
  try {
    await client.realtime.subscribe("notes/*", (event) => events.push(event), { maxReconnects: 1, reconnectDelayMs: 1 });
    await new Promise((resolve) => setTimeout(resolve, 20));
    assert.equal(connections, 1);
    assert.deepEqual(events.map((event) => event.action), ["create", "update"]);
  } finally { await client.realtime.unsubscribe(); }
});

test("a rejected added subscription does not poison subsequent topic synchronization", async () => {
  const syncs = [];
  const client = new PgBaseClient("http://pgbase.test", { fetch: async (_url, init) => {
    if (init.method === "POST") {
      const topics = JSON.parse(init.body).subscriptions;
      syncs.push(topics);
      return topics.includes("invalid/topic/extra")
        ? Response.json({ error: { code: "invalid_realtime_topic" } }, { status: 400 })
        : new Response(null, { status: 204 });
    }
    return new Response(new ReadableStream({ start(controller) {
      controller.enqueue(new TextEncoder().encode('event: PB_CONNECT\ndata: {"clientId":"session"}\n\n'));
      init.signal.addEventListener("abort", () => controller.close(), { once: true });
    } }));
  } });
  try {
    await client.realtime.subscribe("notes/*", () => {});
    await assert.rejects(client.realtime.subscribe("invalid/topic/extra", () => {}), { code: "invalid_realtime_topic" });
    await client.realtime.subscribe("notes/one", () => {});
    assert.deepEqual(syncs.at(-1), ["notes/*", "notes/one"]);
  } finally { await client.realtime.unsubscribe(); }
});

test("shared realtime stops the old user stream when auth changes", async () => {
  let connectionSignal;
  const events = [];
  const client = new PgBaseClient("http://pgbase.test", { fetch: async (_url, init) => {
    if (init.method === "POST") return new Response(null, { status: 204 });
    connectionSignal = init.signal;
    return new Response(new ReadableStream({ start(controller) {
      controller.enqueue(new TextEncoder().encode('event: PB_CONNECT\ndata: {"clientId":"session"}\n\n'));
      init.signal.addEventListener("abort", () => controller.close(), { once: true });
    } }));
  } });
  client.auth.save("user-a");
  await client.realtime.subscribe("notes/*", (event) => events.push(event));
  client.auth.save("user-b");
  assert.equal(connectionSignal.aborted, true);
  assert.equal(client.realtime.status.state, "stopped");
  assert.deepEqual(events, []);
});


test("auth changes in one realtime listener stop the remaining copied listeners", async () => {
  let write;
  let connectionSignal;
  const first = [];
  const second = [];
  const client = new PgBaseClient("http://pgbase.test", { fetch: async (_url, init) => {
    if (init.method === "POST") return new Response(null, { status: 204 });
    connectionSignal = init.signal;
    return new Response(new ReadableStream({ start(controller) {
      write = (frame) => controller.enqueue(new TextEncoder().encode(frame));
      write('event: PB_CONNECT\ndata: {"clientId":"session"}\n\n');
      init.signal.addEventListener("abort", () => controller.close(), { once: true });
    } }));
  } });
  try {
    client.auth.save("user-a");
    await client.realtime.subscribe("notes/*", (event) => { first.push(event); client.auth.save("user-b"); });
    await client.realtime.subscribe("notes/*", (event) => second.push(event));
    write('event: notes/*\ndata: {"action":"create","record":{"id":"a-private"}}\n\n');
    await new Promise((resolve) => setTimeout(resolve, 0));
    assert.equal(first.length, 1);
    assert.equal(connectionSignal.aborted, true);
    assert.equal(client.auth.token, "user-b");
    assert.deepEqual(second, []);
  } finally { await client.realtime.unsubscribe(); }
});
