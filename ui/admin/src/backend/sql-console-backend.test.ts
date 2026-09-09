import { describe, expect, it } from "vitest";
import { createSQLConsoleBackend, SQLConsoleError, type SQLConsoleTransport } from "./sql-console-backend";

const execution = {
  id: "8bff0101-0000-4000-8000-000000000001", mode: "read", dryRun: false, status: "succeeded", createdAt: "2026-09-10T00:00:00Z",
  result: { rows: 1, affectedRows: 0, bytes: 42, truncated: false, dryRun: false, elapsedMs: 7, statements: [{ affectedRows: 0, columns: [{ name: "n", typeOid: 20, typeName: "int8" }], rows: [[{ kind: "integer", text: "9223372036854775807" }, { kind: "null" }]] }] },
};

function transport(reply: unknown | unknown[], status = 200): { transport: SQLConsoleTransport; calls: Array<{ path: string; method: string; headers?: Record<string, string>; body?: unknown }> } {
  const calls: Array<{ path: string; method: string; headers?: Record<string, string>; body?: unknown }> = [];
  const replies = Array.isArray(reply) ? [...reply] : [reply];
  return { calls, transport: { request: async (request) => { calls.push(request); return { status, body: replies.shift() }; } } };
}

describe("SQL console backend", () => {
  it("sends the one execution identity and optional write idempotency key without changing tagged exact cells", async () => {
    const scripted = transport(execution, 202);
    const backend = createSQLConsoleBackend(scripted.transport);
    const result = await backend.submit({ operationID: execution.id, idempotencyKey: "write-attempt-0001", mode: "read", dryRun: false, statements: [{ sql: "select $1", params: [{ kind: "integer", text: "9223372036854775807" }] }] });

    expect(scripted.calls).toEqual([{
      path: "/api/admin/sql/queries", method: "POST", headers: { "PgBase-Operation-ID": execution.id, "Idempotency-Key": "write-attempt-0001" },
      body: { mode: "read", dryRun: false, statements: [{ sql: "select $1", params: [{ kind: "integer", text: "9223372036854775807" }] }] },
    }]);
    expect(result.result?.statements?.[0].rows?.[0]).toEqual([{ kind: "integer", text: "9223372036854775807" }, { kind: "null" }]);
  });

  it("uses the dedicated cancel and cursor routes", async () => {
    const scripted = transport([{ executions: [execution], nextCursor: "next", writeEnabled: true, writeTargets: [{ schema: "extension", table: "items", columns: ["id", "amount"] }] }, execution]);
    const backend = createSQLConsoleBackend(scripted.transport);

    const history = await backend.history("prior page");
    expect(history.writeTargets).toEqual([{ schema: "extension", table: "items", columns: ["id", "amount"] }]);
    await backend.cancel(execution.id);
    expect(scripted.calls.map((call) => [call.method, call.path])).toEqual([
      ["GET", "/api/admin/sql/queries?cursor=prior%20page"], ["POST", `/api/admin/sql/queries/${execution.id}/cancel`],
    ]);
  });

  it("keeps malformed exact values and server failures out of the page contract", async () => {
    const malformed = transport({ ...execution, result: { ...execution.result, statements: [{ affectedRows: 0, rows: [[{ kind: "null", text: "must not be here" }]] }] } });
    await expect(createSQLConsoleBackend(malformed.transport).execution(execution.id)).rejects.toMatchObject({ name: "SQLConsoleError", code: "invalid_response" });

    const rejected = transport({ error: { code: "forbidden", message: "SQL execution is not allowed", details: {}, requestId: "request-1" } }, 403);
    await expect(createSQLConsoleBackend(rejected.transport).execution(execution.id)).rejects.toEqual(new SQLConsoleError(403, "forbidden"));

    const malformedError = transport({ error: { message: "missing code" } }, 403);
    await expect(createSQLConsoleBackend(malformedError.transport).execution(execution.id)).rejects.toMatchObject({ status: 502, code: "invalid_response" });
  });
});
