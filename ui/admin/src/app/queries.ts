import { MutationCache, QueryCache, QueryClient, queryOptions } from "@tanstack/react-query";
import { ApiError } from "../api/http";
import { getSession, logout } from "../api/session";
import { getSetupStatus } from "../api/system";
import { en } from "../i18n/en";
import type { Translate } from "../i18n";
import { readStorage, removeStorage } from "./store";

/** Pages a scrolled list keeps in memory (and in the DOM); further pages push the first ones out. */
const windowPages = 10;

/**
 * Infinite-query options for those lists. The API pages one way only, so a page pushed out of the window is reached
 * again by starting over.
 */
export const pageWindow = { initialPageParam: undefined as string | undefined, getPreviousPageParam: () => undefined, maxPages: windowPages };

/** The window has moved past the first page. */
export const windowShifted = (data: { pageParams: unknown[] } | undefined) => data !== undefined && data.pageParams[0] !== undefined;

/** Pages load on scroll until the window is full; past that a click asks for each one. */
export const windowOpen = (data: { pages: unknown[] } | undefined) => (data?.pages.length ?? 0) < windowPages;

/** Set by a confirmed sign-out until the server has ended that session. */
export const signingOutKey = "pgbase.signingOut";

/** Ends the session a confirmed sign-out left behind (the page was left before the server answered), so its cookie is never trusted again. */
export async function endSignedOutSession(): Promise<void> {
  const gone = (error: unknown) => {
    if (!isUnauthorized(error)) throw error;
  };
  try {
    await getSession();
    await logout();
  } catch (error) {
    // The earlier request may have ended it in between; a session that is gone now is all this needs.
    await getSession().then(() => Promise.reject(error), gone);
  }
  removeStorage(signingOutKey);
}

export const sessionQuery = queryOptions({
  queryKey: ["session"],
  queryFn: async ({ signal }) => {
    if (readStorage(signingOutKey)) await endSignedOutSession();
    return getSession(signal);
  },
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

/**
 * How long an answer is shown again without asking the server: a revisit inside the window renders from cache, a later
 * one renders the cached data and refetches behind it. Lists are the default; schema-like data opts into `catalog`.
 */
export const freshFor = { list: 30_000, catalog: 5 * 60_000 };

// Never marked stale by a write: the session check, file URLs (a re-mint discards the browser's image cache), the open
// record form (keyed by ETag) and per-row file lists (each would refetch on scroll; their list page reseeds them).
const unaffected = (key: readonly unknown[]) =>
  ["session", "setup-status", "file-url"].includes(String(key[0])) || (key[0] === "records" && (key[2] === "one" || key[2] === "files"));

/**
 * After a write: every cached screen is marked stale without fetching, so the next visit refetches behind the data it
 * shows. A safety net for what a write touched elsewhere (views, relations, users ↔ auth records); a mutation's own
 * onSuccess runs after this, so its setQueryData stays fresh.
 */
export const markStale = (client: QueryClient = queryClient) => client.invalidateQueries({ refetchType: "none", predicate: (query) => !unaffected(query.queryKey) });

export const queryClient: QueryClient = new QueryClient({
  queryCache: new QueryCache({ onError }),
  mutationCache: new MutationCache({ onError, onSuccess: () => void markStale() }),
  defaultOptions: {
    queries: {
      staleTime: freshFor.list,
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
