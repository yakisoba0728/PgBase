import assert from "node:assert/strict";
import { test } from "node:test";
import { PgBaseClient, PgBaseError } from "../dist/index.js";

const tick = () => new Promise((resolve) => setTimeout(resolve, 0));
function streamClient() {
  const connections = [];
  const client = new PgBaseClient("http://pgbase.test", { fetch: async (_url, init) => {
    if (init.method === "POST") return new Response(null, { status: 204 });
    return new Response(new ReadableStream({ start(controller) {
      const write = (frame) => controller.enqueue(new TextEncoder().encode(frame));
      connections.push({ headers: init.headers, write, end: () => controller.close() });
      init.signal.addEventListener("abort", () => { try { controller.close(); } catch {} }, { once: true });
      write('event: PB_CONNECT\ndata: {"clientId":"session"}\n\n');
    } }));
  } });
  return { client, connections };
}

test("reentrant observer transitions never deliver an older state after stopped", async () => {
  const { client, connections } = streamClient();
  const observed = [];
  client.realtime.onStatus((status) => {
    if (status.state === "connecting") return client.realtime.unsubscribe();
  });
  client.realtime.onStatus((status) => observed.push(status.state));
  await assert.rejects(client.realtime.subscribe("notes/*", () => {}), { code: "realtime_unsubscribed" });
  assert.deepEqual(observed, ["stopped", "stopped"]);
  assert.equal(client.realtime.status.state, "stopped");
  assert.equal(connections.length, 0);
});

test("reset requested by a reconnecting observer does not wait through the backoff", async () => {
  let reset;
  let beganReset;
  const resetting = new Promise((resolve) => { beganReset = resolve; });
  const client = new PgBaseClient("http://pgbase.test", { fetch: async (_url, init) => init.method === "POST"
    ? new Response(null, { status: 204 })
    : new Response('event: PB_CONNECT\ndata: {"clientId":"session"}\n\n') });
  client.realtime.onStatus((status) => {
    if (status.state === "reconnecting") { reset = client.realtime.resetCursor(); beganReset(); }
  });
  await client.realtime.subscribe("notes/*", () => {}, { maxReconnects: 2, reconnectDelayMs: 1000 });
  await resetting;
  let timer;
  try {
    const completed = await Promise.race([reset.then(() => true), new Promise((resolve) => { timer = setTimeout(() => resolve(false), 200); })]);
    assert.equal(completed, true, "reset must settle before the configured one-second backoff");
    assert.equal(client.realtime.status.reason, "reset");
  } finally { clearTimeout(timer); await reset; }
});

test("status observers may register another subscription without starting a second connection", async () => {
  const { client, connections } = streamClient();
  let added;
  client.realtime.onStatus((status) => {
    if (status.state === "connecting" && !added) {
      added = true;
      added = client.realtime.subscribe("notes/one", () => {});
    }
  });
  try {
    await client.realtime.subscribe("notes/*", () => {});
    await added;
    assert.equal(connections.length, 1);
  } finally { await client.realtime.unsubscribe(); }
});

test("status observers receive post-readiness terminal errors and explicit cursor reset allows resubscribe", async () => {
  const { client, connections } = streamClient();
  const statuses = [];
  const remove = client.realtime.onStatus((status) => statuses.push(status));
  assert.equal(client.realtime.status.state, "stopped");
  await client.realtime.subscribe("notes/*", () => {});
  connections[0].write('id: 9007199254740993\nevent: cursor\ndata: {}\n\nevent: PB_ERROR\ndata: {"code":"cursor_expired"}\n\n');
  await tick();
  const failed = statuses.at(-1);
  assert.equal(failed.state, "stopped");
  assert.equal(failed.reason, "terminal");
  assert.equal(failed.lastEventId, "9007199254740993");
  assert.ok(failed.error instanceof PgBaseError);
  assert.equal(failed.error.code, "cursor_expired");
  await client.realtime.subscribe("notes/*", () => {});
  assert.equal(connections[1].headers.get("Last-Event-ID"), "9007199254740993");
  await client.realtime.resetCursor();
  assert.equal(client.realtime.status.lastEventId, undefined);
  assert.equal(client.realtime.status.reason, "reset");
  await client.realtime.subscribe("notes/*", () => {});
  assert.equal(connections[2].headers.get("Last-Event-ID"), null);
  await client.realtime.unsubscribe();
  await tick();
  assert.equal(statuses.at(-1).reason, "unsubscribed");
  remove();
});

test("consumer and observer exceptions cannot interrupt other listeners or reconnect", async () => {
  const { client, connections } = streamClient();
  const statuses = [];
  const delivered = [];
  client.realtime.onStatus(() => { throw new Error("broken observer"); });
  client.realtime.onStatus(async () => { throw new Error("async observer"); });
  client.realtime.onStatus((status) => statuses.push(status));
  await client.realtime.subscribe("notes/*", () => { throw new Error("broken consumer"); });
  await client.realtime.subscribe("notes/*", async () => { throw new Error("async consumer"); });
  await client.realtime.subscribe("notes/*", (event) => delivered.push(event.action));
  await client.realtime.subscribeApplication("feed", async () => { throw new Error("async application consumer"); });
  for (const action of ["create", "update"]) connections[0].write(`id: 8\nevent: notes/*\ndata: {"action":"${action}","record":{"id":"one"}}\n\n`);
  connections[0].write('event: @app/ZmVlZA\ndata: {}\n\n');
  await tick();
  assert.deepEqual(delivered, ["create", "update"]);
  assert.equal(connections.length, 1);
  assert.equal(statuses.filter((status) => status.source === "listener").length, 5);
  assert.equal(statuses.find((status) => status.error?.message === "async application consumer").topic, "feed");
  assert.equal(client.realtime.status.state, "connected");
  await client.realtime.unsubscribe();
});

test("recoverable failures report attempts and exhaustion after initial readiness", async () => {
  const statuses = [];
  let requests = 0;
  const client = new PgBaseClient("http://pgbase.test", { fetch: async (_url, init) => {
    if (init.method === "POST") return new Response(null, { status: 204 });
    if (++requests === 1) return new Response('event: PB_CONNECT\ndata: {"clientId":"session"}\n\n');
    return Response.json({ error: { code: "temporarily_unavailable" } }, { status: 503 });
  } });
  client.realtime.onStatus((status) => statuses.push(status));
  await client.realtime.subscribe("notes/*", () => {}, { maxReconnects: 1, reconnectDelayMs: 0 });
  for (let i = 0; i < 10 && client.realtime.status.state !== "stopped"; i++) await tick();
  assert.equal(requests, 2);
  assert.equal(statuses.find((status) => status.state === "reconnecting").attempt, 1);
  assert.equal(statuses.at(-1).reason, "exhausted");
  assert.equal(statuses.at(-1).error.code, "temporarily_unavailable");
});

test("repeated empty sessions exhaust the reconnect budget despite successful handshakes", async () => {
  let connections = 0;
  const client = new PgBaseClient("http://pgbase.test", { fetch: async (_url, init) => {
    if (init.method === "POST") return new Response(null, { status: 204 });
    connections++;
    return new Response('event: PB_CONNECT\ndata: {"clientId":"session"}\n\n');
  } });
  try {
    await client.realtime.subscribe("notes/*", () => {}, { maxReconnects: 2, reconnectDelayMs: 0 });
    for (let i = 0; i < 10 && client.realtime.status.state !== "stopped"; i++) await tick();
    assert.equal(client.realtime.status.reason, "exhausted");
    assert.equal(connections, 3);
    assert.equal(client.realtime.status.attempt, 2);
  } finally { await client.realtime.unsubscribe(); }
});
