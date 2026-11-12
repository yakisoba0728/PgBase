import { useCallback, useMemo } from "react";
import { useSearchParams } from "react-router";
import { infiniteQueryOptions, queryOptions } from "@tanstack/react-query";
import { listJobs } from "../api/jobs";
import { getRequestLog, getRequestLogStatistics, listRequestLogs, type RequestLogFilter } from "../api/logs";
import { getRealtimeStatus } from "../api/realtime";
import { useLanguage } from "../i18n";
import { localToRfc3339 } from "./format";

export const logsKey = ["logs"] as const;

export const logsQuery = (filter: Omit<RequestLogFilter, "before">) =>
  infiniteQueryOptions({
    queryKey: [...logsKey, "list", filter],
    queryFn: ({ pageParam, signal }) => listRequestLogs({ ...filter, before: pageParam }, signal),
    initialPageParam: undefined as string | undefined,
    getNextPageParam: (page) => (page.nextBefore === undefined ? undefined : String(page.nextBefore)),
  });

export const logStatsQuery = queryOptions({
  queryKey: [...logsKey, "statistics"],
  queryFn: ({ signal }) => getRequestLogStatistics(signal),
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

export const realtimeQuery = queryOptions({
  queryKey: ["realtime", "status"],
  queryFn: ({ signal }) => getRealtimeStatus(signal),
  refetchInterval: 5000,
});

/** Query-string state for an ops screen; empty values are removed. */
export function useParams() {
  const [params, setParams] = useSearchParams();
  const update = useCallback(
    (changes: Record<string, string | null>) => {
      // The live URL, not the render's params: a quick second change would otherwise undo the first.
      const next = new URLSearchParams(window.location.search);
      for (const [key, value] of Object.entries(changes)) {
        if (value) next.set(key, value);
        else next.delete(key);
      }
      setParams(next);
    },
    [setParams],
  );
  return { params, update };
}

/** The logs filter as the API takes it, read from the URL. */
export function logFilterFrom(params: URLSearchParams): Omit<RequestLogFilter, "before"> {
  const status = Number(params.get("status"));
  return {
    statusClass: status >= 1 && status <= 5 ? (status as RequestLogFilter["statusClass"]) : undefined,
    method: params.get("method") || undefined,
    route: params.get("route") || undefined,
    from: localToRfc3339(params.get("from") ?? ""),
    to: localToRfc3339(params.get("to") ?? ""),
  };
}

/** Formats server timestamps in the UI language; `full` adds the date and seconds. */
export function useTimeFormat() {
  const language = useLanguage();
  return useMemo(() => {
    const short = new Intl.DateTimeFormat(language, { month: "2-digit", day: "2-digit", hour: "2-digit", minute: "2-digit", second: "2-digit", hourCycle: "h23" });
    const full = new Intl.DateTimeFormat(language, { dateStyle: "medium", timeStyle: "medium" });
    return (iso: string | undefined, long = false) => {
      if (!iso) return "";
      const date = new Date(iso);
      return Number.isNaN(date.getTime()) ? iso : (long ? full : short).format(date);
    };
  }, [language]);
}
