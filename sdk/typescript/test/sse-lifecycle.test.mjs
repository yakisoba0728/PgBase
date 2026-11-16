import assert from "node:assert/strict";
import { test } from "node:test";
import { PgBaseClient } from "../dist/index.js";
import { parseEventStream } from "../dist/sse.js";

test("leaving an SSE consumer cancels its unread body and releases the reader", async () => {
  let cancelled = 0;
  const stream = new ReadableStream({
    start(controller) { controller.enqueue(new TextEncoder().encode("data: first\n\n")); },
    cancel() { cancelled++; },
  });
  for await (const message of parseEventStream(stream)) {
    assert.equal(message.data, "first");
    break;
  }
  assert.equal(cancelled, 1);
  assert.equal(stream.locked, false);
});

test("a malformed realtime frame closes its old response before reconnecting", { timeout: 10_000 }, async () => {
  const connections = [];
  let reconnected;
  const connectedAgain = new Promise((resolve) => { reconnected = resolve; });
  const client = new PgBaseClient("http://pgbase.test", { fetch: async (_url, init) => {
    if (init.method === "POST") return new Response(null, { status: 204 });
    let connection;
    const stream = new ReadableStream({
      start(controller) {
        connection = {
          cancelled: false,
          close() { try { controller.close(); } catch {} },
          write(frame) { controller.enqueue(new TextEncoder().encode(frame)); },
        };
        connections.push(connection);
        init.signal.addEventListener("abort", () => connection.close(), { once: true });
        connection.write('event: PB_CONNECT\ndata: {"clientId":"session"}\n\n');
      },
      cancel() { connection.cancelled = true; },
    });
    return new Response(stream);
  } });
  const stopObserving = client.realtime.onStatus((status) => {
    if (status.state === "connected" && status.attempt === 1) reconnected();
  });
  try {
    await client.realtime.subscribe("notes/*", () => {}, { reconnectDelayMs: 0, maxReconnects: 1 });
    connections[0].write("event: notes/*\ndata: malformed-json\n\n");
    await connectedAgain;
    assert.equal(connections.length, 2);
    assert.equal(connections[0].cancelled, true, "releasing a reader alone leaves the server stream open");
  } finally {
    stopObserving();
    await client.realtime.unsubscribe();
    for (const connection of connections) connection.close();
  }
});
