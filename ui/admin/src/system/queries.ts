import { infiniteQueryOptions, queryOptions } from "@tanstack/react-query";
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

export const serviceTokensQuery = queryOptions({
  queryKey: ["system", "service-tokens"],
  queryFn: ({ signal }) => listServiceTokens(signal),
});

