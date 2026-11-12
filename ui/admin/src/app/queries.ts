import { MutationCache, QueryCache, QueryClient, queryOptions } from "@tanstack/react-query";
import { ApiError } from "../api/http";
import { getSession } from "../api/session";
import { getSetupStatus } from "../api/system";
import { en } from "../i18n/en";
import type { Translate } from "../i18n";

export const sessionQuery = queryOptions({
  queryKey: ["session"],
  queryFn: ({ signal }) => getSession(signal),
  retry: false,
  staleTime: Infinity,
});

export const setupStatusQuery = queryOptions({
  queryKey: ["setup-status"],
  queryFn: ({ signal }) => getSetupStatus(signal),
  retry: false,
});

export const isUnauthorized = (error: unknown) => error instanceof ApiError && error.status === 401;

// Any 401 outside the session check means the session ended: drop it so the guard redirects.
function onError(error: unknown) {
  if (isUnauthorized(error) && queryClient.getQueryData(sessionQuery.queryKey)) {
    void queryClient.resetQueries({ queryKey: sessionQuery.queryKey });
  }
}

export const queryClient = new QueryClient({
  queryCache: new QueryCache({ onError }),
  mutationCache: new MutationCache({ onError }),
  defaultOptions: {
    queries: {
      refetchOnWindowFocus: false,
      retry: (count, error) => !(error instanceof ApiError && error.status >= 400 && error.status < 500) && count < 2,
    },
  },
});

/** Localised text for a failed call; raw server messages are English-only, so codes are mapped. */
export function errorText(t: Translate, error: unknown): string {
  const code = error instanceof ApiError ? error.code : "client_error";
  const key = `error.${code}`;
  if (key in en) return t(key as keyof typeof en);
  if (error instanceof ApiError && error.status === 401) return t("error.unauthorized");
  if (error instanceof ApiError && error.status === 403) return t("error.forbidden");
  return t("error.generic", { code });
}
