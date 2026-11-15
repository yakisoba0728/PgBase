import { collectionsQuery, recordsListQuery } from "../collections/queries";
import { jobsQuery, logFilterFrom, logsQuery } from "../ops/queries";
import { queryClient } from "./queries";
import { collectionPath, recentCollections } from "./recent";
import { preloadScreen } from "./screens";

// Lists only when not cached yet: prefetching a cached one replays every loaded page, past the mount's own refetch rule.
const cached = (key: readonly unknown[]) => queryClient.getQueryData(key) !== undefined;

/** The Records tab's first page once the catalog says which fields it needs; nothing for a missing name. */
async function prefetchRecords(encoded: string | undefined, params: URLSearchParams) {
  const name = encoded === undefined ? undefined : decodeURIComponent(encoded);
  // A cached catalog serves even when stale: the visit revalidates it, and the hover should not wait on it.
  const catalog = Object.values((queryClient.getQueryData(collectionsQuery.queryKey) ?? (await queryClient.fetchQuery(collectionsQuery))).collections);
  // /collections opens the most recent collection that still exists, else the first by name (CollectionsIndexPage).
  const target = name ?? recentCollections().find((recent) => catalog.some((collection) => collection.name === recent)) ?? catalog.map((collection) => collection.name).sort((a, b) => a.localeCompare(b))[0];
  const collection = catalog.find((candidate) => candidate.name === target);
  const list = collection && recordsListQuery(collection, name ? (params.get("filter") ?? "") : "", name ? (params.get("sort") ?? "") : "");
  if (list && !cached(list.queryKey)) await queryClient.prefetchInfiniteQuery(list);
}

/** A collection link in the navigation opens the tab last used there (NavItem `target`), not its href. */
export function navTarget(pathname: string): string {
  const name = /^\/collections\/([^/]+)$/.exec(pathname)?.[1];
  try {
    return name ? collectionPath(decodeURIComponent(name)) : pathname;
  } catch {
    return pathname;
  }
}

/**
 * Starts what a screen asks for first (its chunk and its cheap first list), so a visit runs them together instead of
 * one after another. Cached answers still fresh are not asked for again; failures are left to the visit.
 */
export function warmPath(pathname: string, search = "") {
  preloadScreen(pathname);
  const [section = "", name, tab] = pathname.replace(/^\/+/, "").split("/");
  const params = new URLSearchParams(search);
  const run = (task: () => Promise<unknown>) => void task().catch(() => {});
  if (section === "" || section === "collections") {
    if (tab) run(() => queryClient.prefetchQuery(collectionsQuery));
    else run(() => prefetchRecords(name, params));
  } else if (section === "logs") {
    const list = logsQuery(logFilterFrom(params));
    if (!cached(list.queryKey)) run(() => queryClient.prefetchInfiniteQuery(list));
  }
  else if (section === "jobs") run(() => queryClient.prefetchQuery(jobsQuery));
}
