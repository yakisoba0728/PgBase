import { afterEach, describe, expect, it, vi } from "vitest";
import { act, renderHook, waitFor } from "@testing-library/react";
import { QueryClient, QueryClientProvider, useQuery } from "@tanstack/react-query";
import type { ReactNode } from "react";
import * as collectionsApi from "../api/collections";
import * as recordsApi from "../api/records";
import { recordsListQuery } from "../collections/queries";
import { freshFor, markStale, queryClient } from "./queries";
import { navTarget, warmPath } from "./prefetch";
import { useDebounced } from "./search";

afterEach(() => {
  queryClient.clear();
  vi.restoreAllMocks();
});

describe("cache policy", () => {
  it("serves a revisit from cache inside the list window", async () => {
    expect(queryClient.getDefaultOptions().queries?.staleTime).toBe(freshFor.list);
    const queryFn = vi.fn().mockResolvedValue(1);
    await queryClient.fetchQuery({ queryKey: ["jobs"], queryFn });
    await queryClient.prefetchQuery({ queryKey: ["jobs"], queryFn });
    expect(queryFn).toHaveBeenCalledTimes(1);
  });

  it("marks screens stale after a write, but not the session, file URLs or the open record, and a later setQueryData stays fresh", async () => {
    for (const key of [["jobs"], ["session"], ["file-url", "f", "thumbnail"], ["records", "notes", "one", "r"], ["records", "notes", "list", {}], ["system", "settings"]]) queryClient.setQueryData(key, {});
    await queryClient
      .getMutationCache()
      .build(queryClient, { mutationFn: async () => "saved", onSuccess: () => queryClient.setQueryData(["system", "settings"], { saved: true }) })
      .execute(undefined);
    const invalidated = (key: unknown[]) => queryClient.getQueryCache().find({ queryKey: key, exact: true })!.state.isInvalidated;
    expect(invalidated(["jobs"])).toBe(true);
    expect(invalidated(["records", "notes", "list", {}])).toBe(true);
    expect(invalidated(["session"])).toBe(false);
    expect(invalidated(["file-url", "f", "thumbnail"])).toBe(false);
    expect(invalidated(["records", "notes", "one", "r"])).toBe(false);
    expect(invalidated(["system", "settings"])).toBe(false);
  });

  it("marking stale fetches nothing", async () => {
    const queryFn = vi.fn().mockResolvedValue(1);
    await queryClient.fetchQuery({ queryKey: ["jobs"], queryFn });
    await markStale();
    expect(queryFn).toHaveBeenCalledTimes(1);
  });
});

describe("warmPath", () => {
  it("asks for the catalog, then the records page under the grid's own key", async () => {
    const notes = { id: "c1", name: "notes", tableName: "notes", createdAt: "", fields: [{ id: "f", name: "cover", columnName: "cover", type: "file", required: false, unique: false, indexed: false, ordinal: 0, hidden: false }] };
    vi.spyOn(collectionsApi, "listCollections").mockResolvedValue({ collections: { c1: notes } } as unknown as Awaited<ReturnType<typeof collectionsApi.listCollections>>);
    const list = vi.spyOn(recordsApi, "listRecords").mockResolvedValue({ items: [] });
    warmPath("/collections/notes", "?sort=-created_at");
    await waitFor(() => expect(list).toHaveBeenCalledTimes(1));
    expect(list.mock.calls[0][1]).toMatchObject({ sort: "-created_at", files: true });
    expect(queryClient.getQueryData(recordsListQuery(notes as collectionsApi.Collection, "", "-created_at").queryKey)).toBeDefined();
    // A second hover inside the window asks for nothing.
    warmPath("/collections/notes", "?sort=-created_at");
    await new Promise((resolve) => setTimeout(resolve));
    expect(collectionsApi.listCollections).toHaveBeenCalledTimes(1);
    expect(list).toHaveBeenCalledTimes(1);
  });
});

describe("warmPath on a cached list", () => {
  it("leaves a loaded list to its mount, even when stale, and survives a malformed path", async () => {
    const notes = { id: "c1", name: "notes", tableName: "notes", createdAt: "", fields: [] };
    queryClient.setQueryData(["collections"], { collections: { c1: notes } });
    queryClient.setQueryData(recordsListQuery(notes as collectionsApi.Collection, "", "").queryKey, { pages: [{ items: [] }, { items: [] }], pageParams: [undefined, "next"] });
    await markStale();
    const catalog = vi.spyOn(collectionsApi, "listCollections");
    const list = vi.spyOn(recordsApi, "listRecords");
    warmPath("/collections/notes");
    expect(() => warmPath("/collections/%zz")).not.toThrow();
    await new Promise((resolve) => setTimeout(resolve));
    expect(catalog).not.toHaveBeenCalled();
    expect(list).not.toHaveBeenCalled();
    expect(navTarget("/collections/%zz")).toBe("/collections/%zz");
    expect(navTarget("/logs")).toBe("/logs");
  });
});

describe("debounced search", () => {
  it("asks once per pause and aborts the request it replaces", async () => {
    vi.useFakeTimers();
    const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
    const signals: Record<string, AbortSignal> = {};
    const wrapper = ({ children }: { children: ReactNode }) => <QueryClientProvider client={client}>{children}</QueryClientProvider>;
    const { rerender } = renderHook(({ text }) => {
      const q = useDebounced(text);
      return useQuery({ queryKey: ["search", q], queryFn: ({ signal }) => new Promise<string>((resolve) => { signals[q] = signal; setTimeout(() => resolve(q), 1000); }), enabled: q !== "" });
    }, { wrapper, initialProps: { text: "" } });
    for (const text of ["a", "ad", "ada"]) {
      rerender({ text });
      await act(() => vi.advanceTimersByTimeAsync(100));
    }
    await act(() => vi.advanceTimersByTimeAsync(250));
    expect(Object.keys(signals)).toEqual(["ada"]);
    rerender({ text: "bob" });
    await act(() => vi.advanceTimersByTimeAsync(250));
    expect(signals.ada.aborted).toBe(true);
    expect(signals.bob.aborted).toBe(false);
    vi.useRealTimers();
    client.clear();
  });
});
