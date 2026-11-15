import { useEffect, useState } from "react";
import { infiniteQueryOptions, queryOptions, useQuery, type InfiniteData, type QueryClient } from "@tanstack/react-query";
import { listCollections, type Collection } from "../api/collections";
import { fileURL, getRecord, listRecordFiles, listRecords, mintFileToken, publicFileURL, type RecordData, type RecordPage, type StoredFile } from "../api/records";
import { freshFor } from "../app/queries";
import type { Translate } from "../i18n";
import { storedVisibleColumns } from "./columns";
import { expandFor } from "./relation";
import { buildFilter } from "./useRecordsUrl";

export const collectionsQuery = queryOptions({
  queryKey: ["collections"],
  queryFn: ({ signal }) => listCollections(signal),
  select: (catalog): Collection[] => Object.values(catalog.collections).sort((a, b) => a.name.localeCompare(b.name)),
  staleTime: freshFor.catalog,
});

/** After a schema write (apply, import, delete): both catalogs are fetched again (other screens are marked stale by markStale). */
export function catalogChanged(queryClient: QueryClient) {
  return Promise.all([
    queryClient.invalidateQueries({ queryKey: collectionsQuery.queryKey }),
    queryClient.invalidateQueries({ queryKey: ["auth", "collections"] }),
  ]);
}

export function useCollection(name: string) {
  const query = useQuery(collectionsQuery);
  const { data, refetch } = query;
  const collection = data?.find((candidate) => candidate.name === name);
  // The catalog is cached for minutes: a name it lacks (made in another tab) is looked up once more before "missing".
  const [checked, setChecked] = useState<string | null>(null);
  const unchecked = name !== "" && data !== undefined && !collection && checked !== name;
  useEffect(() => {
    if (!unchecked) return;
    setChecked(name);
    void refetch();
  }, [unchecked, name, refetch]);
  return { ...query, collection, looking: query.isPending || unchecked || (!collection && query.isFetching) };
}

// The server's page maximum: fewer round trips when scrolling through large collections.
const pageSize = 200;

/** `files`: the collection has a file field, so pages carry their file metadata (extra work on the server otherwise). */
export const recordsQuery = (collection: string, filter: string, sort: string, expand: string, files = false) =>
  infiniteQueryOptions({
    queryKey: ["records", collection, "list", { filter, sort, expand }],
    queryFn: async ({ pageParam, signal, client }) => {
      const page = await listRecords(collection, { filter: filter || undefined, sort: sort || undefined, expand: expand || undefined, cursor: pageParam, limit: pageSize, total: pageParam ? undefined : true, files: files || undefined, signal });
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

/** The Records grid's list for the URL's filter and sort and this device's columns; the grid and its prefetch share it, so both use one key. */
export const recordsListQuery = (collection: Collection, filter: string, sort: string, visible = storedVisibleColumns(collection)) =>
  recordsQuery(collection.name, buildFilter(filter, collection.fields), sort, expandFor(collection, visible), collection.fields.some((field) => field.type === "file"));

/** "200 of 1,234 loaded" while pages remain (the first page carries the bounded total), else the loaded count. */
export function countText(t: Translate, loaded: number, more: boolean, first?: RecordPage): string {
  if (!more || first?.totalItems === undefined) return t("records.count", { count: loaded });
  return t(first.totalCapped ? "records.loadedOfAtLeast" : "records.loadedOf", { count: loaded, total: first.totalItems.toLocaleString() });
}

type ListKey = ["records", string, "list", { filter: string; sort: string; expand: string }];

/**
 * A saved record replaces its loaded row where neither membership nor place can have changed (no filter, the default
 * created_at order) and its relation labels still hold; other lists that show it start over from the first page.
 */
export function replaceInLists(queryClient: QueryClient, collection: string, record: RecordData) {
  for (const query of queryClient.getQueryCache().findAll({ queryKey: ["records", collection, "list"] })) {
    const data = query.state.data as InfiniteData<RecordPage> | undefined;
    const row = data?.pages.flatMap((page) => page.items).find((item) => item.id === record.id);
    if (!data || !row) continue;
    const { filter, sort } = (query.queryKey as ListKey)[3];
    // A save does not return expand: a changed relation needs the server's labels.
    const relinked = Object.keys(row.expand ?? {}).some((name) => JSON.stringify(row[name]) !== JSON.stringify(record[name]));
    if (filter || sort || relinked) {
      void queryClient.resetQueries({ queryKey: query.queryKey, exact: true });
      continue;
    }
    queryClient.setQueryData<InfiniteData<RecordPage>>(query.queryKey, {
      ...data,
      pages: data.pages.map((page) => ({ ...page, items: page.items.map((item) => (item.id === record.id ? { ...record, expand: item.expand } : item)) })),
    });
  }
}
/** A new record's place depends on the server's order: lists start over from the first page. */
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

const tokenLifetimeMax = 15 * 60_000;

/**
 * When a file token stops working (ms since epoch), read from its signed payload (internal/filestore/service.go
 * signDownloadToken: ... 8-byte big-endian expiry in seconds, then a 32-byte HMAC); undefined when unreadable.
 */
export function tokenExpiry(token: string): number | undefined {
  try {
    const bytes = Uint8Array.from(atob(token.replaceAll("-", "+").replaceAll("_", "/")), (char) => char.charCodeAt(0));
    if (bytes.length < 16 + 16 + 1 + 8 + 32) return undefined;
    return Number(new DataView(bytes.buffer).getBigUint64(bytes.length - 32 - 8)) * 1000;
  } catch {
    return undefined;
  }
}

/** How long a thumbnail URL stays usable after it was fetched: a public URL always, a token URL until shortly before it expires. */
export function thumbnailLifetime(url: string | undefined, fetchedAt: number): number {
  const token = url && new URL(url, "http://local").searchParams.get("token");
  if (!token) return Infinity;
  const expires = tokenExpiry(token);
  // 30 s of slack for clock skew; an unreadable token gets the old fixed minute (FileThumb re-mints on a failed image).
  const left = expires === undefined ? 60_000 : expires - fetchedAt - 30_000;
  return Math.max(0, Math.min(left, tokenLifetimeMax));
}

/** A same-origin URL for one file variant: the public URL when the field is public, else a short-lived token URL. */
export const fileUrlQuery = (file: StoredFile, variant: "original" | "thumbnail") =>
  queryOptions({
    queryKey: ["file-url", file.id, variant],
    queryFn: async ({ signal }) =>
      !file.protected && file.publicUrl ? publicFileURL(file.id, variant === "original" ? undefined : variant) : fileURL(file.id, await pooled(signal, () => mintFileToken(file.id, variant)), variant),
    // A thumbnail keeps its token (and so the browser's cached image) for the token's life; a preview or download mints on every open.
    staleTime: variant === "original" ? 0 : (query) => thumbnailLifetime(query.state.data, query.state.dataUpdatedAt),
    gcTime: variant === "original" ? 0 : tokenLifetimeMax,
  });
