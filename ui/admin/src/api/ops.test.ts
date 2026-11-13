import { afterEach, describe, expect, it, vi } from "vitest";
import { listRunningBackupJobs } from "./backups";
import { clearCsrfToken } from "./http";
import { deleteRequestLogsBefore, getRequestLogStatistics, listRequestLogs } from "./logs";
import { listDeadRealtimeEvents } from "./realtime";

const calls: { method: string; url: string }[] = [];

function respond(body: unknown, status = 200) {
  vi.stubGlobal("fetch", vi.fn(async (url: string, init: RequestInit) => {
    calls.push({ method: init.method ?? "GET", url });
    return new Response(status === 204 ? null : JSON.stringify(body), { status, headers: { "Content-Type": "application/json" } });
  }));
}

describe("operations API", () => {
  afterEach(() => {
    vi.unstubAllGlobals();
    clearCsrfToken();
    calls.length = 0;
  });

  it("sends the log search and scopes statistics to the time window", async () => {
    respond({ items: [], oldestId: 0 });
    await listRequestLogs({ q: "192.0.2.4", to: "2026-11-13T00:00:00Z" });
    expect(calls[0].url).toBe("/api/admin/logs?q=192.0.2.4&to=2026-11-13T00%3A00%3A00Z");
    respond({ total: 0, dropped: 0, averageDurationMs: 0, statusClasses: {} });
    await getRequestLogStatistics({ from: "2026-11-12T00:00:00Z" });
    expect(calls[1].url).toBe("/api/admin/logs/statistics?from=2026-11-12T00%3A00%3A00Z");
  });

  it("deletes only older entries when given a bound", async () => {
    respond({ deleted: 12 });
    await expect(deleteRequestLogsBefore("2026-11-13T00:00:00Z")).resolves.toEqual({ deleted: 12 });
    expect(calls[0]).toEqual({ method: "DELETE", url: "/api/admin/logs?to=2026-11-13T00%3A00%3A00Z" });
  });

  it("pages dead realtime events with the opaque cursor", async () => {
    respond({ items: [], nextBefore: "1_x" });
    await listDeadRealtimeEvents();
    await listDeadRealtimeEvents("1_x");
    expect(calls.map((call) => call.url)).toEqual(["/api/admin/realtime/dead", "/api/admin/realtime/dead?before=1_x"]);
  });

  it("lists running backup operations", async () => {
    respond({ items: [{ id: "j", operation: "create", status: "running" }] });
    await expect(listRunningBackupJobs()).resolves.toMatchObject([{ id: "j" }]);
    expect(calls[0].url).toBe("/api/admin/backup-jobs");
  });
});
