import assert from "node:assert/strict";
import { createServer } from "node:http";
import { once } from "node:events";
import { PgBaseClient, PgBaseError } from "../dist/index.js";

async function withRealtimeFailure(status, code, check) {
  let connections = 0;
  const server = createServer((request, response) => {
    if (request.url === "/api/realtime" && request.method === "GET") {
      connections += 1;
      response.statusCode = status;
      response.setHeader("content-type", "application/json");
      response.end(JSON.stringify({ error: { code } }));
      return;
    }
    response.statusCode = 404;
    response.end();
  });
  server.listen(0, "127.0.0.1");
  await once(server, "listening");
  try {
    await check(new PgBaseClient(`http://127.0.0.1:${server.address().port}`), () => connections);
  } finally {
    await new Promise((resolve, reject) => server.close((error) => error ? reject(error) : resolve()));
  }
}

for (const [status, code] of [[400, "request_failed"], [401, "request_failed"], [403, "request_failed"], [410, "request_failed"], [500, "cursor_expired"]]) {
  await withRealtimeFailure(status, code, async (client, connections) => {
    await assert.rejects(
      client.realtime.subscribe("notes/*", () => {}, { maxReconnects: 3, reconnectDelayMs: 1 }),
      (error) => error instanceof PgBaseError && error.status === status && error.code === code,
    );
    assert.equal(connections(), 1, `${status}/${code} terminates without reconnecting`);
  });
}

await withRealtimeFailure(500, "temporarily_unavailable", async (client, connections) => {
  await assert.rejects(
    client.realtime.subscribe("notes/*", () => {}, { maxReconnects: 0, reconnectDelayMs: 1 }),
    (error) => error instanceof PgBaseError && error.status === 500,
  );
  assert.equal(connections(), 1, "the reconnect budget bounds initial readiness");
});

const eofServer = createServer((request, response) => {
  if (request.url === "/api/realtime" && request.method === "GET") {
    response.setHeader("content-type", "text/event-stream");
    response.end();
    return;
  }
  response.statusCode = 404;
  response.end();
});
eofServer.listen(0, "127.0.0.1");
await once(eofServer, "listening");
try {
  const client = new PgBaseClient(`http://127.0.0.1:${eofServer.address().port}`);
  await assert.rejects(
    client.realtime.subscribe("notes/*", () => {}, { maxReconnects: 0 }),
    (error) => error instanceof PgBaseError && error.code === "realtime_reconnect_exhausted",
  );
} finally {
  await new Promise((resolve, reject) => eofServer.close((error) => error ? reject(error) : resolve()));
}

const pendingServer = createServer((request, response) => {
  if (request.url === "/api/realtime" && request.method === "GET") {
    response.setHeader("content-type", "text/event-stream");
    return;
  }
  response.statusCode = 404;
  response.end();
});
pendingServer.listen(0, "127.0.0.1");
await once(pendingServer, "listening");
try {
  const client = new PgBaseClient(`http://127.0.0.1:${pendingServer.address().port}`);
  const pending = client.realtime.subscribe("notes/*", () => {});
  await new Promise((resolve) => setTimeout(resolve, 5));
  await client.realtime.unsubscribe();
  await assert.rejects(pending, (error) => error instanceof PgBaseError && error.code === "realtime_unsubscribed");
} finally {
  await new Promise((resolve, reject) => pendingServer.close((error) => error ? reject(error) : resolve()));
}

let connects = 0;
let syncs = 0;
let firstResponse;
let firstEnded = false;
let resolveFirstSynced;
const firstSynced = new Promise((resolve) => { resolveFirstSynced = resolve; });
const reconnectServer = createServer((request, response) => {
  if (request.url === "/api/realtime" && request.method === "POST") {
    syncs += 1;
    if (connects === 1 && firstEnded) {
      response.statusCode = 410;
      response.setHeader("content-type", "application/json");
      response.end(JSON.stringify({ error: { code: "stale_client" } }));
      return;
    }
    if (connects === 1) {
      resolveFirstSynced();
      firstEnded = true;
      firstResponse.end();
    }
    response.statusCode = 204;
    response.end();
    return;
  }
  if (request.url === "/api/realtime" && request.method === "GET") {
    connects += 1;
    response.setHeader("content-type", "text/event-stream");
    response.write(`event: PB_CONNECT\ndata: {"clientId":"client-${connects}"}\n\n`);
    if (connects === 1) firstResponse = response;
    else response.end('event: notes/second\ndata: {"action":"create","record":{"id":"reconnected"}}\n\n');
    return;
  }
  response.statusCode = 404;
  response.end();
});
reconnectServer.listen(0, "127.0.0.1");
await once(reconnectServer, "listening");
try {
  const client = new PgBaseClient(`http://127.0.0.1:${reconnectServer.address().port}`);
  const first = await client.realtime.subscribe("notes/*", () => {}, { reconnectDelayMs: 50 });
  await firstSynced;
  await new Promise((resolve) => setTimeout(resolve, 0));
  assert.equal(firstEnded, true);
  const received = new Promise((resolve) => {
    client.realtime.subscribe("notes/second", resolve);
  });
  assert.deepEqual(await received, { topic: "notes/second", action: "create", record: { id: "reconnected" } });
  await first();
  await client.realtime.unsubscribe();
  assert.equal(connects, 2, "a recoverable stream close reconnects");
  assert.ok(syncs >= 2, "each successful connection synchronizes the remaining listener");
} finally {
  await new Promise((resolve, reject) => reconnectServer.close((error) => error ? reject(error) : resolve()));
}

console.log("realtime terminal failure conformance passed");
