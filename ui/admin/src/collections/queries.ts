import { infiniteQueryOptions, queryOptions, useQuery } from "@tanstack/react-query";
import { listCollections, type Collection } from "../api/collections";
import { getRecord, listRecordFiles, listRecords } from "../api/records";

export const collectionsQuery = queryOptions({
  queryKey: ["collections"],
  queryFn: ({ signal }) => listCollections(signal),
  select: (catalog): Collection[] => Object.values(catalog.collections).sort((a, b) => a.name.localeCompare(b.name)),
});

export function useCollection(name: string) {
  const query = useQuery(collectionsQuery);
  return { ...query, collection: query.data?.find((candidate) => candidate.name === name) };
}

const pageSize = 50;

export const recordsQuery = (collection: string, filter: string, sort: string) =>
  infiniteQueryOptions({
    queryKey: ["records", collection, "list", { filter, sort }],
    queryFn: ({ pageParam, signal }) =>
      listRecords(collection, { filter: filter || undefined, sort: sort || undefined, cursor: pageParam, limit: pageSize, signal }),
    initialPageParam: undefined as string | undefined,
    getNextPageParam: (page) => page.nextCursor,
  });

export const recordQuery = (collection: string, id: string) =>
  queryOptions({
    queryKey: ["records", collection, "one", id],
    queryFn: ({ signal }) => getRecord(collection, id, { signal }),
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
  });
