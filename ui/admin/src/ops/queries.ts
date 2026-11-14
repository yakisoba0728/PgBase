import { infiniteQueryOptions, queryOptions } from "@tanstack/react-query";
import { pageWindow } from "../app/queries";
import { listJobs } from "../api/jobs";
import { getRequestLog, getRequestLogStatistics, listRequestLogs, type RequestLogFilter } from "../api/logs";
import { getRealtimeStatus, listDeadRealtimeEvents } from "../api/realtime";
import { localToRfc3339 } from "./format";

export const logsKey = ["logs"] as const;

export const logsQuery = (filter: Omit<RequestLogFilter, "before">) =>
  infiniteQueryOptions({
    queryKey: [...logsKey, "list", filter],
    queryFn: ({ pageParam, signal }) => listRequestLogs({ ...filter, before: pageParam }, signal),
    ...pageWindow,
    getNextPageParam: (page) => (page.nextBefore === undefined ? undefined : String(page.nextBefore)),
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

/** Dead events, newest first; "load more" follows the opaque cursor, and older pages push the newest ones out. */
export const deadEventsQuery = infiniteQueryOptions({
  queryKey: [...realtimeKey, "dead"],
  queryFn: ({ pageParam, signal }) => listDeadRealtimeEvents(pageParam, signal),
  ...pageWindow,
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
