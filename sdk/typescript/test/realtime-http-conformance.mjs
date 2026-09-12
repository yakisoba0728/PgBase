// Runs against a real PgBase server. Required: PGBASE_RT_URL, PGBASE_RT_TOKEN,
// PGBASE_RT_COLLECTION. The script creates its own record after subscribing.
import assert from "node:assert/strict";
import { PgBaseClient } from "../dist/index.js";

const baseURL = required("PGBASE_RT_URL");
const token = required("PGBASE_RT_TOKEN");
const collection = required("PGBASE_RT_COLLECTION");
const client = new PgBaseClient(baseURL);
client.auth.save(token);
const suffix = Date.now().toString(36);
const topic = `${collection}?filter=title%3D'wanted-${suffix}'&fields=id%2Ctitle`;
const all = `${collection}/*`;
const events = [];
const stopFiltered = await client.realtime.subscribe(topic, (event) => events.push(event));
const stopAll = await client.realtime.subscribe(all, (event) => events.push(event));
await client.records(collection).create({ title: `wanted-${suffix}`, ignored: "not requested" }, `rt-conformance-${suffix}`.padEnd(16, "x"));
for (let wait = 0; wait < 100 && events.length < 2; wait++) await new Promise((resolve) => setTimeout(resolve, 25));
assert.equal(events.length, 2, "multiplexed filtered and wildcard subscriptions receive the create");
const projected = events.find((event) => !("ignored" in event.record));
assert.equal(projected?.record.title, `wanted-${suffix}`);
await stopFiltered();
await stopAll();
const stoppedCount = events.length;
await client.records(collection).create({ title: `stopped-${suffix}` }, `rt-stopped-${suffix}`.padEnd(16, "x"));
await new Promise((resolve) => setTimeout(resolve, 100));
assert.equal(events.length, stoppedCount, "unsubscribe closes the multiplex connection");
const resumed = [];
const stopResumed = await client.realtime.subscribe(all, (event) => resumed.push(event));
await client.records(collection).create({ title: `resumed-${suffix}` }, `rt-resumed-${suffix}`.padEnd(16, "x"));
for (let wait = 0; wait < 100 && !resumed.some((event) => event.record.title === `resumed-${suffix}`); wait++) await new Promise((resolve) => setTimeout(resolve, 25));
assert.ok(resumed.some((event) => event.record.title === `resumed-${suffix}`), "resubscribe reconnects and synchronizes before returning");
await stopResumed();
console.log("realtime HTTP conformance passed");
function required(name) { const value = process.env[name]; if (!value) throw new Error(`${name} is required`); return value; }
