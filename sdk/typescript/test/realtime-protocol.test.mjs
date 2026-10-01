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
