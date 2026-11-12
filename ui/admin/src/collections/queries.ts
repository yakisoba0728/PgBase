import { infiniteQueryOptions, queryOptions, useQuery, type InfiniteData, type QueryClient } from "@tanstack/react-query";
import { listCollections, type Collection } from "../api/collections";
import { fileURL, getRecord, listRecordFiles, listRecords, mintFileToken, publicFileURL, type RecordData, type RecordPage, type StoredFile } from "../api/records";

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
    queryFn: ({ pageParam, signal }) =>
      listRecords(collection, { filter: filter || undefined, sort: sort || undefined, expand: expand || undefined, cursor: pageParam, limit: pageSize, signal }),
    initialPageParam: undefined as string | undefined,
    getNextPageParam: (page) => page.nextCursor,
    // Refetching an infinite list replays every loaded page; past a few pages that is left to Refresh.
    refetchOnMount: (query) => (query.state.data?.pages.length ?? 0) <= 3,
    refetchOnReconnect: (query) => (query.state.data?.pages.length ?? 0) <= 3,
  });

/** Edits the loaded list pages in place, so a save or delete never refetches every loaded page. */
export function patchLists(queryClient: QueryClient, collection: string, update: (items: RecordData[]) => RecordData[]) {
  queryClient.setQueriesData<InfiniteData<RecordPage>>({ queryKey: ["records", collection, "list"] }, (data) =>
    data ? { ...data, pages: data.pages.map((page) => ({ ...page, items: update(page.items) })) } : data,
  );
}

/** A saved record replaces its row; without `withExpand` the row keeps its old expand data until a fresh fetch. */
export function replaceInLists(queryClient: QueryClient, collection: string, record: RecordData, withExpand = false) {
  patchLists(queryClient, collection, (items) => items.map((item) => (item.id === record.id ? { ...record, expand: withExpand ? record.expand : item.expand } : item)));
}

/** A created record goes on top of the loaded list (whatever the sort) until the next refresh. */
export function addToLists(queryClient: QueryClient, collection: string, record: RecordData) {
  queryClient.setQueriesData<InfiniteData<RecordPage>>({ queryKey: ["records", collection, "list"] }, (data) =>
    data ? { ...data, pages: data.pages.map((page, index) => (index === 0 ? { ...page, items: [record, ...page.items.filter((item) => item.id !== record.id)] } : page)) } : data,
  );
}

export function removeFromLists(queryClient: QueryClient, collection: string, ids: string[]) {
  const gone = new Set(ids);
  patchLists(queryClient, collection, (items) => items.filter((item) => !gone.has(item.id)));
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

export const recordFilesQuery = (collection: string, id: string, field: string) =>
  queryOptions({
    queryKey: ["records", collection, "files", id, field],
    queryFn: ({ signal }) => listRecordFiles(collection, id, field, signal),
    staleTime: 60_000,
  });

/** A same-origin URL for one file variant: the public URL when the field is public, else a short-lived token URL. */
export const fileUrlQuery = (file: StoredFile, variant: "original" | "thumbnail") =>
  queryOptions({
    queryKey: ["file-url", file.id, variant],
    queryFn: async () =>
      !file.protected && file.publicUrl ? publicFileURL(file.id, variant === "original" ? undefined : variant) : fileURL(file.id, await mintFileToken(file.id, variant), variant),
    // Tokens live at most 15 minutes; mint a new one well before that.
    staleTime: 5 * 60_000,
    gcTime: 10 * 60_000,
  });
