import { infiniteQueryOptions, queryOptions } from "@tanstack/react-query";
import { pageWindow } from "../app/queries";
import { getIpAllowlist, getProfile } from "../api/session";
import { uuidBefore } from "../auth/forms";
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
  ...pageWindow,
  getNextPageParam: (page) => page.nextAfter,
});

/** One administrator by id, for a deep link past the loaded pages; null when there is none. */
export const administratorQuery = (id: string) =>
  queryOptions({
    queryKey: ["system", "administrators", "one", id],
    queryFn: async ({ signal }) => {
      // The list is `id > after` in id order, so the page after the UUID just below opens on this one.
      const after = uuidBefore(id);
      if (after === null) return null;
      const page = await listAdministrators({ limit: 1, after }, signal);
      return page.items[0]?.id === id ? page.items[0] : null;
    },
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

