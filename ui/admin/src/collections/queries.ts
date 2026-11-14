import { infiniteQueryOptions, queryOptions, useQuery, type InfiniteData, type QueryClient } from "@tanstack/react-query";
import { listCollections, type Collection } from "../api/collections";
import { fileURL, getRecord, listRecordFiles, listRecords, mintFileToken, publicFileURL, type RecordData, type RecordPage, type StoredFile } from "../api/records";
import type { Translate } from "../i18n";

export const collectionsQuery = queryOptions({
  queryKey: ["collections"],
  queryFn: ({ signal }) => listCollections(signal),
  select: (catalog): Collection[] => Object.values(catalog.collections).sort((a, b) => a.name.localeCompare(b.name)),
});

export function useCollection(name: string) {
  const query = useQuery(collectionsQuery);
  return { ...query, collection: query.data?.find((candidate) => candidate.name === name) };
}

// The server's page maximum: fewer round trips when scrolling through large collections.
const pageSize = 200;

export const recordsQuery = (collection: string, filter: string, sort: string, expand: string) =>
  infiniteQueryOptions({
    queryKey: ["records", collection, "list", { filter, sort, expand }],
    queryFn: async ({ pageParam, signal, client }) => {
      const page = await listRecords(collection, { filter: filter || undefined, sort: sort || undefined, expand: expand || undefined, cursor: pageParam, limit: pageSize, total: pageParam ? undefined : true, files: true, signal });
      for (const [id, fields] of Object.entries(page.fileMetadata ?? {})) {
        for (const [field, files] of Object.entries(fields)) client.setQueryData(recordFilesQuery(collection, id, field).queryKey, files);
      }
      return page;
    },
    initialPageParam: undefined as string | undefined,
    getNextPageParam: (page) => page.nextCursor,
    // Refetching an infinite list replays every loaded page; past a few pages that is left to Refresh.
    refetchOnMount: (query) => (query.state.data?.pages.length ?? 0) <= 3,
    refetchOnReconnect: (query) => (query.state.data?.pages.length ?? 0) <= 3,
  });

/** "200 of 1,234 loaded" while pages remain (the first page carries the bounded total), else the loaded count. */
export function countText(t: Translate, loaded: number, more: boolean, first?: RecordPage): string {
  if (!more || first?.totalItems === undefined) return t("records.count", { count: loaded });
  return t(first.totalCapped ? "records.loadedOfAtLeast" : "records.loadedOf", { count: loaded, total: first.totalItems.toLocaleString() });
}

/** Let the server apply filters and sort again, fetching only the first page. */
export function replaceInLists(queryClient: QueryClient, collection: string, _record: RecordData, _withExpand = false) {
  void queryClient.resetQueries({ queryKey: ["records", collection, "list"] });
}
export function addToLists(queryClient: QueryClient, collection: string, _record: RecordData) {
  void queryClient.resetQueries({ queryKey: ["records", collection, "list"] });
}

/** Drops deleted records from the loaded pages in place (no refetch of every loaded page); an exact total shrinks with them, so "N matching" stays true. */
export function removeFromLists(queryClient: QueryClient, collection: string, ids: string[]) {
  const gone = new Set(ids);
  queryClient.setQueriesData<InfiniteData<RecordPage>>({ queryKey: ["records", collection, "list"] }, (data) => {
    if (!data) return data;
    let removed = 0;
    const pages = data.pages.map((page) => {
      const items = page.items.filter((item) => !gone.has(item.id));
      removed += page.items.length - items.length;
      return { ...page, items };
    });
    const first = pages[0];
    if (first?.totalItems !== undefined && !first.totalCapped) pages[0] = { ...first, totalItems: Math.max(0, first.totalItems - removed) };
    return { ...data, pages };
  });
}

/** `expand` only shapes the fetch (relation labels), not the key, so a save can replace the cached record. */
export const recordQuery = (collection: string, id: string, expand = "") =>
  queryOptions({
    queryKey: ["records", collection, "one", id],
    queryFn: ({ signal }) => getRecord(collection, id, { signal, expand: expand || undefined }),
    // The open form is keyed by ETag: only an explicit reload or a save may replace it,
    // and nothing is kept once the pane closes, so reopening always fetches fresh.
    staleTime: Infinity,
    gcTime: 0,
    refetchOnReconnect: false,
  });

// shortcut: per-row file requests share one small pool instead of a per-page file endpoint;
// upgrade when the server lists a page's files (or mints tokens) in one request.
let free = 4;
const waiting: (() => void)[] = [];

/** Runs `task` once one of the pool's slots is free; a query cancelled while queued never runs. */
export async function pooled<T>(signal: AbortSignal | undefined, task: () => Promise<T>): Promise<T> {
  signal?.throwIfAborted();
  if (free > 0) free--;
  else {
    await new Promise<void>((resolve, reject) => {
      const start = () => {
        signal?.removeEventListener("abort", cancel);
        resolve();
      };
      const cancel = () => {
        waiting.splice(waiting.indexOf(start), 1);
        reject(signal!.reason);
      };
      waiting.push(start);
      signal?.addEventListener("abort", cancel, { once: true });
    });
  }
  try {
    return await task();
  } finally {
    const next = waiting.shift();
    if (next) next();
    else free++;
  }
}

export const recordFilesQuery = (collection: string, id: string, field: string) =>
  queryOptions({
    queryKey: ["records", collection, "files", id, field],
    queryFn: ({ signal }) => pooled(signal, () => listRecordFiles(collection, id, field, signal)),
    staleTime: 60_000,
  });

/** A same-origin URL for one file variant: the public URL when the field is public, else a short-lived token URL. */
export const fileUrlQuery = (file: StoredFile, variant: "original" | "thumbnail") =>
  queryOptions({
    queryKey: ["file-url", file.id, variant],
    queryFn: async ({ signal }) =>
      !file.protected && file.publicUrl ? publicFileURL(file.id, variant === "original" ? undefined : variant) : fileURL(file.id, await pooled(signal, () => mintFileToken(file.id, variant)), variant),
    // Token lifetime is a server setting (at most 15 minutes, possibly far less). Grid thumbnails
    // reuse theirs briefly (FileThumb re-mints when an image fails); a preview or download mints on every open.
    staleTime: variant === "original" ? 0 : 60_000,
    gcTime: variant === "original" ? 0 : 2 * 60_000,
  });
