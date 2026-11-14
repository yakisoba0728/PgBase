import { InfiniteQueryObserver, QueryClient } from "@tanstack/react-query";
import { afterEach, expect, it, vi } from "vitest";
import * as sql from "../api/sql";
import { refreshSqlHistory, sqlHistoryKey, sqlHistoryQuery } from "./queries";

afterEach(() => vi.restoreAllMocks());

it("puts a shifted SQL history window back when the jump to the newest page fails", async () => {
  vi.spyOn(sql, "listSqlExecutions").mockRejectedValue(new Error("down"));
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  const shifted = { pages: [{ executions: [], nextCursor: "c2" }, { executions: [] }], pageParams: ["c1", "c2"] };
  client.setQueryData(sqlHistoryKey, shifted);
  // Shown on screen: an active observer, so the invalidation refetches.
  const stop = new InfiniteQueryObserver(client, { ...sqlHistoryQuery, staleTime: Infinity }).subscribe(() => {});
  try {
    await refreshSqlHistory(client, true);
    expect(client.getQueryData(sqlHistoryKey)).toEqual(shifted);
    expect(client.getQueryState(sqlHistoryKey)?.status).toBe("error");
  } finally {
    stop();
  }
});
