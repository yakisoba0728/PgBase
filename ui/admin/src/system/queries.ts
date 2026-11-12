import { infiniteQueryOptions, queryOptions } from "@tanstack/react-query";
import { listRequestLogs } from "../api/logs";
import { getIpAllowlist, getProfile } from "../api/session";
import { getSchemaHealth, getSettings, getSystemStatus, listAdministrators, listServiceTokens } from "../api/system";

export const settingsQuery = queryOptions({
  queryKey: ["system", "settings"],
  queryFn: ({ signal }) => getSettings(signal),
  // The form draft is keyed by revision; only a save, reload or explicit refresh replaces it.
  staleTime: Infinity,
  refetchOnMount: "always",
});

export const systemStatusQuery = queryOptions({
  queryKey: ["system", "status"],
  queryFn: ({ signal }) => getSystemStatus(signal),
  refetchInterval: 15000,
});

export const schemaHealthQuery = queryOptions({
  queryKey: ["system", "health"],
  queryFn: ({ signal }) => getSchemaHealth(signal),
  refetchInterval: 15000,
});

export const administratorsQuery = infiniteQueryOptions({
  queryKey: ["system", "administrators"],
  queryFn: ({ pageParam, signal }) => listAdministrators({ limit: 100, after: pageParam }, signal),
  initialPageParam: undefined as string | undefined,
  getNextPageParam: (page) => page.nextAfter,
});

export const profileQuery = queryOptions({
  queryKey: ["system", "profile"],
  queryFn: ({ signal }) => getProfile(signal),
});

export const allowlistQuery = queryOptions({
  queryKey: ["system", "ip-allowlist"],
  queryFn: ({ signal }) => getIpAllowlist(signal),
});

/**
 * The address the server saw for this administrator, from the request log of the allowlist read.
 * No endpoint reports it directly; logs carry it only with "include IP" and "include auth" on, and
 * only entries tagged with this administrator's id count (another administrator may be active).
 */
export const callerAddressQuery = (administratorId: string) =>
  queryOptions({
    queryKey: ["system", "caller-address", administratorId],
    queryFn: async ({ signal }) => {
      const page = await listRequestLogs({ route: "/api/admin/profile/ip-allowlist", method: "GET" }, signal);
      const logged = page.items.find((entry) => entry.authID === administratorId && entry.clientIP)?.clientIP;
      // Logged as a host prefix ("203.0.113.7/32"); the address part is what the allowlist is checked against.
      return logged ? logged.replace(/\/\d+$/, "") : null;
    },
    staleTime: 0,
    retry: false,
  });

export const serviceTokensQuery = queryOptions({
  queryKey: ["system", "service-tokens"],
  queryFn: ({ signal }) => listServiceTokens(signal),
});

