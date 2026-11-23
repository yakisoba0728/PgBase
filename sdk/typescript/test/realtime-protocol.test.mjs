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

test("reconnect retains the checkpoint and rejects only newly denied topics", async () => {
  const streams = [];
  const syncs = [];
  let reconnecting;
  const began = new Promise((resolve) => { reconnecting = resolve; });
  const client = new PgBaseClient("http://pgbase.test", { fetch: async (_url, init) => {
    if (init.method === "POST") {
      const topics = JSON.parse(init.body).subscriptions;
      syncs.push(topics);
      return topics.includes("denied/*")
        ? Response.json({ error: { code: "access_denied" } }, { status: 403 })
        : new Response(null, { status: 204 });
    }
    return new Response(new ReadableStream({ start(controller) {
      streams.push({ controller, checkpoint: init.headers.get("Last-Event-ID") });
      controller.enqueue(new TextEncoder().encode(`event: PB_CONNECT\ndata: {"clientId":"session-${streams.length}"}\n\n`));
      init.signal.addEventListener("abort", () => { try { controller.close(); } catch {} }, { once: true });
    } }));
  } });
  client.realtime.onStatus((status) => { if (status.state === "reconnecting") reconnecting(status); });
  const topics = Array.from({ length: 100 }, (_, i) => `notes/${i}`);
  try {
    await client.realtime.subscribeMany(topics, () => {}, { maxReconnects: 1, reconnectDelayMs: 10 });
    assert.deepEqual(syncs, [topics]);
    streams[0].controller.enqueue(new TextEncoder().encode('id: 9007199254740993\nevent: cursor\ndata: {}\n\nevent: PB_ERROR\ndata: {"code":"subscription_failed","retryable":true}\n\n'));
    assert.equal((await began).error.code, "subscription_failed");
    await assert.rejects(client.realtime.subscribe("denied/*", () => {}), { code: "access_denied" });
    assert.equal(streams[1].checkpoint, "9007199254740993");
    assert.deepEqual(syncs, [topics, topics, [...topics, "denied/*"]]);
    assert.equal(client.realtime.status.state, "connected");
  } finally { await client.realtime.unsubscribe(); }
});

test("concurrent subscriptions share one replacement and a denied topic rejects only its own call", async () => {
  const syncs = [];
  let release, held = false;
  const client = new PgBaseClient("http://pgbase.test", { fetch: async (_url, init) => {
    if (init.method === "POST") {
      const topics = JSON.parse(init.body).subscriptions;
      syncs.push(topics);
      if (!held) { held = true; await new Promise((resolve) => { release = resolve; }); }
      return topics.includes("denied/*")
        ? Response.json({ error: { code: "access_denied" } }, { status: 403 })
        : new Response(null, { status: 204 });
    }
    return new Response(new ReadableStream({ start(controller) {
      controller.enqueue(new TextEncoder().encode('event: PB_CONNECT\ndata: {"clientId":"session"}\n\n'));
      init.signal.addEventListener("abort", () => controller.close(), { once: true });
    } }));
  } });
  try {
    // The handshake installs only the first call's topic; the 20 calls queued
    // behind it share the next replacement instead of 20 serial ones.
    const first = client.realtime.subscribe("notes/*", () => {});
    const queued = Array.from({ length: 20 }, (_, i) => client.realtime.subscribe(`notes/${i}`, () => {}));
    while (!release) await new Promise((resolve) => setTimeout(resolve, 1));
    release();
    await Promise.all([first, ...queued]);
    const all = ["notes/*", ...Array.from({ length: 20 }, (_, i) => `notes/${i}`)];
    assert.deepEqual(syncs, [["notes/*"], all]);

    // A denied topic fails the shared replacement without changing the server,
    // so the batch is retried call by call and only that call rejects.
    syncs.length = 0;
    const settled = await Promise.allSettled([
      client.realtime.subscribe("notes/a", () => {}),
      client.realtime.subscribe("notes/b", () => {}),
      client.realtime.subscribe("denied/*", () => {}),
      client.realtime.subscribe("notes/c", () => {}),
    ]);
    assert.deepEqual(settled.map((result) => result.status), ["fulfilled", "fulfilled", "rejected", "fulfilled"]);
    assert.equal(settled[2].reason.code, "access_denied");
    assert.deepEqual(syncs, [
      [...all, "notes/a"],
      [...all, "notes/a", "notes/b", "denied/*", "notes/c"],
      [...all, "notes/a", "notes/b"],
      [...all, "notes/a", "notes/b", "denied/*"],
      [...all, "notes/a", "notes/b", "notes/c"],
    ]);
    assert.equal(client.realtime.status.state, "connected");
  } finally { await client.realtime.unsubscribe(); }
});
