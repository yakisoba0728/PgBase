import assert from "node:assert/strict";
import { PgBaseClient, PgBaseError } from "../dist/index.js";

let requests = 0;
const client = new PgBaseClient(process.env.PGBASE_SDK_URL, { fetch: (...args) => {
  requests++;
  return fetch(...args);
} });
const token = process.env.PGBASE_SDK_TOKEN;
const other = process.env.PGBASE_SDK_OTHER_TOKEN;
const changed = (error) => error instanceof PgBaseError && error.status === 403 && error.code === "session_changed";

for (const limit of [1, 2]) {
  client.auth.save(token);
  const before = requests;
  const iterator = client.iterate("iterator_notes", { limit });
  try {
    assert.equal((await iterator.next()).done, false);
    client.auth.save(other);
    await assert.rejects(iterator.next(), changed);
    assert.equal(requests, before + 1, "stale iterator made another HTTP request");
  } finally { await iterator.return(); }
}

client.auth.save(token);
const rows = [];
for await (const row of client.iterate("iterator_notes", { limit: 1 })) rows.push(row.title);
assert.deepEqual(rows.sort(), ["private-first", "private-second"]);
client.auth.save(other);
const otherRows = [];
for await (const row of client.iterate("iterator_notes", { limit: 1 })) otherRows.push(row);
assert.deepEqual(otherRows, [], "replacement user's fresh iterator must remain isolated");
console.log("TypeScript iterator HTTP conformance passed: buffered/page boundaries, stable pagination, fresh-user isolation");
