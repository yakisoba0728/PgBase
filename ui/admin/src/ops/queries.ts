import { infiniteQueryOptions, queryOptions } from "@tanstack/react-query";
import { listJobs } from "../api/jobs";
import { getRequestLog, getRequestLogStatistics, listRequestLogs, type RequestLogFilter } from "../api/logs";
import { getRealtimeStatus, listDeadRealtimeEvents } from "../api/realtime";
import { localToRfc3339 } from "./format";

export const logsKey = ["logs"] as const;

/** Pages of 50 kept in memory (and in the DOM); older pages push the newest ones out. */
export const logWindowPages = 10;

export const logsQuery = (filter: Omit<RequestLogFilter, "before">) =>
  infiniteQueryOptions({
    queryKey: [...logsKey, "list", filter],
    queryFn: ({ pageParam, signal }) => listRequestLogs({ ...filter, before: pageParam }, signal),
    initialPageParam: undefined as string | undefined,
    getNextPageParam: (page) => (page.nextBefore === undefined ? undefined : String(page.nextBefore)),
    // The API pages only towards older entries, so a dropped newer page is reached again by starting over.
    getPreviousPageParam: () => undefined,
    maxPages: logWindowPages,
  });

/** Statistics for the list's time window only; the other filters do not apply to them. */
export const logStatsQuery = (range: Pick<RequestLogFilter, "from" | "to"> = {}) =>
  queryOptions({
    queryKey: [...logsKey, "statistics", { from: range.from, to: range.to }],
    queryFn: ({ signal }) => getRequestLogStatistics({ from: range.from, to: range.to }, signal),
  });

export const logQuery = (id: string) =>
  queryOptions({
    queryKey: [...logsKey, "one", id],
    queryFn: ({ signal }) => getRequestLog(id, signal),
  });

export const jobsQuery = queryOptions({
  queryKey: ["jobs"],
  queryFn: ({ signal }) => listJobs(signal),
  // Keeps last/next run and the running flag current after "Run now".
  refetchInterval: 5000,
});

export const realtimeKey = ["realtime"] as const;

export const realtimeQuery = queryOptions({
  queryKey: [...realtimeKey, "status"],
  queryFn: ({ signal }) => getRealtimeStatus(signal),
  refetchInterval: 5000,
});

/** Dead events, newest first; "load more" follows the opaque cursor. */
export const deadEventsQuery = infiniteQueryOptions({
  queryKey: [...realtimeKey, "dead"],
  queryFn: ({ pageParam, signal }) => listDeadRealtimeEvents(pageParam, signal),
  initialPageParam: undefined as string | undefined,
  getNextPageParam: (page) => page.nextBefore,
});

/** The logs filter as the API takes it, read from the URL. */
export function logFilterFrom(params: URLSearchParams): Omit<RequestLogFilter, "before"> {
  const status = Number(params.get("status"));
  return {
    statusClass: status >= 1 && status <= 5 ? (status as RequestLogFilter["statusClass"]) : undefined,
    method: params.get("method") || undefined,
    route: params.get("route") || undefined,
    q: params.get("q")?.trim() || undefined,
    from: localToRfc3339(params.get("from") ?? ""),
    to: localToRfc3339(params.get("to") ?? ""),
  };
}
