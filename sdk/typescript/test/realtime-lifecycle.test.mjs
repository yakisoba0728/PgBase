import assert from "node:assert/strict";
import { test } from "node:test";
import { PgBaseClient } from "../dist/index.js";

function fixture({ holdFirstAbort = false } = {}) {
  const connections = [];
  const client = new PgBaseClient("http://pgbase.test", { fetch: async (_url, init) => {
    if (init.method === "POST") return new Response(null, { status: 204 });
    return new Response(new ReadableStream({ start(controller) {
      const connection = {
        authorization: init.headers.get("Authorization"),
        close() { try { controller.close(); } catch {} },
        write(frame) { controller.enqueue(new TextEncoder().encode(frame)); },
      };
      connections.push(connection);
      if (!holdFirstAbort || connections.length > 1) init.signal.addEventListener("abort", () => connection.close(), { once: true });
      connection.write('event: PB_CONNECT\ndata: {"clientId":"session"}\n\n');
    } }));
  } });
  client.auth.save("account-A");
  return { client, connections };
}

test("a subscription waiting for an old connection cannot adopt another identity", async () => {
  const { client, connections } = fixture({ holdFirstAbort: true });
  try {
    await client.realtime.subscribe("notes/*", () => {});
    await client.realtime.unsubscribe();
    const pending = client.realtime.subscribe("notes/second", () => {});
    const rejected = assert.rejects(pending, { code: "realtime_unsubscribed" });
    client.auth.save("account-B");
    connections[0].close();
    await rejected;
    assert.equal(connections.length, 1, "obsolete subscriptions must not issue a new-account request");
  } finally {
    await client.realtime.unsubscribe();
    for (const connection of connections) connection.close();
  }
});

test("an old subscription disposer cannot stop a replacement session's listener", async () => {
  const { client, connections } = fixture();
  const received = [];
  const listener = (event) => received.push(event.record.id);
  try {
    const oldDispose = await client.realtime.subscribe("notes/*", listener);
    client.auth.save("account-B");
    await client.realtime.subscribe("notes/*", listener);
    await oldDispose();
    assert.equal(client.realtime.status.state, "connected");
    assert.equal(connections[1].authorization, "Bearer account-B");
    connections[1].write('event: notes/*\ndata: {"action":"create","record":{"id":"B-record"}}\n\n');
    await new Promise((resolve) => setImmediate(resolve));
    assert.deepEqual(received, ["B-record"]);
  } finally { await client.realtime.unsubscribe(); }
});
