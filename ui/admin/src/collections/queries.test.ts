import { describe, expect, it, vi } from "vitest";
import { QueryClient } from "@tanstack/react-query";
import * as api from "../api/records";
import { advanceSession } from "../api/http";
import { translate, type MessageKey } from "../i18n";
import type { Collection, CollectionField } from "../api/collections";
import { countText, addToLists, replaceInLists, pooled, recordsListQuery, recordsQuery, recordFilesQuery, thumbnailLifetime, tokenExpiry } from "./queries";

describe("pooled", () => {
  it("runs at most four tasks at once and skips tasks cancelled while queued", async () => {
    let running = 0;
    let peak = 0;
    const releases: (() => void)[] = [];
    const ran: number[] = [];
    const task = (n: number) => () =>
      new Promise<number>((resolve) => {
        running++;
        peak = Math.max(peak, running);
        ran.push(n);
        releases.push(() => {
          running--;
          resolve(n);
        });
      });
    const cancelled = new AbortController();
    const results = Array.from({ length: 8 }, (_, n) => pooled(n === 5 ? cancelled.signal : undefined, task(n)));
    await Promise.resolve();
    expect(ran).toEqual([0, 1, 2, 3]);
    cancelled.abort();
    await expect(results[5]).rejects.toBeDefined();
    while (releases.length) {
      releases.shift()!();
      await new Promise((resolve) => setTimeout(resolve));
    }
    expect(await Promise.all(results.filter((_, n) => n !== 5))).toEqual([0, 1, 2, 3, 4, 6, 7]);
    expect(peak).toBe(4);
    expect(ran).not.toContain(5);
  });

  it.each(["cancellation", "session change"])("does not dispatch a pooled request after %s while it waits for a slot", async (change) => {
    const abort = new AbortController();
    let release!: () => void;
    const gate = new Promise<void>((resolve) => { release = resolve; });
    // On the old implementation the first slot is granted before this queued
    // microtask, but the waiting task resumes afterwards. Its abort listener
    // has already been removed: checking only at queue entry is insufficient.
    if (change === "cancellation") void gate.then(() => queueMicrotask(() => abort.abort()));
    const occupied = Promise.allSettled(Array.from({ length: 4 }, () => pooled(undefined, () => gate)));
    const task = vi.fn(async () => "unexpected");
    const outcome = pooled(abort.signal, task).then((value) => ({ value }), (error: unknown) => ({ error }));
    if (change === "session change") advanceSession();
    release();
    await occupied;
    expect(await outcome).toHaveProperty("error");
    expect(task).not.toHaveBeenCalled();
  });
});

describe("countText", () => {
  const t = (key: MessageKey, vars?: Record<string, string | number>) => translate("en", key, vars);
  it("shows the server's bounded total while pages remain", () => {
    expect(countText(t, 200, true, { totalItems: 1234 })).toBe("200 of 1,234 loaded");
    expect(countText(t, 200, true, { totalItems: 10000, totalCapped: true })).toBe("200 of 10,000+ loaded");
    expect(countText(t, 3, false, { totalItems: 5 })).toBe("3 records");
  });
});

it("seeds per-field file caches from the record page, including empty fields", async () => {
 const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
 const spy = vi.spyOn(api, "listRecords").mockResolvedValue({ items: [{ id: "row" }], fileMetadata: { row: { attachment: [] } } });
 await client.fetchInfiniteQuery(recordsQuery("notes", "", "", "", { fileFields: "attachment" }));
 expect(client.getQueryData(recordFilesQuery("notes", "row", "attachment").queryKey)).toEqual([]);
 expect(spy).toHaveBeenCalledWith("notes", expect.objectContaining({ files: true, fileFields: "attachment" }));
 spy.mockRestore();
 client.clear();
});

it("resets filtered lists instead of inserting a row outside their predicate", async () => {
  const client = new QueryClient();
  const key = recordsQuery("notes", "published=true", "-created_at", "").queryKey;
  client.setQueryData(key, { pages: [{ items: [{ id: "old", published: true }] }, { items: [{ id: "old2" }] }], pageParams: [undefined, "cursor"] });
  addToLists(client, "notes", { id: "new", published: false });
  expect(client.getQueryData(key)).toBeUndefined();
  client.setQueryData(key, { pages: [{ items: [{ id: "old" }] }], pageParams: [undefined] });
  replaceInLists(client, "notes", { id: "old", published: false });
  expect(client.getQueryData(key)).toBeUndefined();
  client.clear();
});

const field = (name: string, type: CollectionField["type"]) => ({ id: name, name, columnName: name, type, required: false, unique: false, indexed: false, ordinal: 0, hidden: false }) as CollectionField;
const collection = (fields: CollectionField[]): Collection => ({ id: "c1", name: "notes", tableName: "notes", fields, createdAt: "" });

it("asks for file metadata only when the collection has a file field", async () => {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  const spy = vi.spyOn(api, "listRecords").mockResolvedValue({ items: [] });
  await client.fetchInfiniteQuery(recordsListQuery(collection([field("title", "text")]), "", ""));
  expect(spy.mock.calls[0][1]?.files).toBeUndefined();
  await client.fetchInfiniteQuery(recordsListQuery(collection([field("title", "text"), field("cover", "file")]), "ada", ""));
  expect(spy.mock.calls[1][1]).toMatchObject({ files: true, filter: "title:lower ~ 'ada'" });
  spy.mockRestore();
  client.clear();
});

it("projects grid values and file metadata to visible fields", async () => {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  const spy = vi.spyOn(api, "listRecords").mockResolvedValue({ items: [] });
  const schema = collection([field("title", "text"), field("content", "jsonb"), field("cover", "file"), field("hidden_file", "file")]);
  const visible = recordsListQuery(schema, "", "", ["title", "cover"]);
  expect(visible.maxPages).toBe(10);
  await client.fetchInfiniteQuery(visible);
  expect(spy).toHaveBeenLastCalledWith("notes", expect.objectContaining({ fields: "id,title", files: true, fileFields: "cover" }));
  await client.fetchInfiniteQuery(recordsListQuery(schema, "", "", ["title"]));
  expect(spy).toHaveBeenLastCalledWith("notes", expect.objectContaining({ fields: "id,title", files: undefined, fileFields: undefined }));
  spy.mockRestore();
  client.clear();
});

it("patches a saved row into unfiltered lists, keeping its labels, and resets when a relation changed", () => {
  const client = new QueryClient();
  const key = recordsQuery("notes", "", "", "author").queryKey;
  const page = { items: [{ id: "a", title: "old", author: "u1", expand: { author: { id: "u1", name: "Ada" } } }, { id: "b" }] };
  client.setQueryData(key, { pages: [page], pageParams: [undefined] });
  replaceInLists(client, "notes", { id: "a", title: "new", author: "u1" });
  expect(client.getQueryData<{ pages: typeof page[] }>(key)?.pages[0].items[0]).toEqual({ id: "a", title: "new", author: "u1", expand: { author: { id: "u1", name: "Ada" } } });
  replaceInLists(client, "notes", { id: "a", title: "new", author: "u2" });
  expect(client.getQueryData(key)).toBeUndefined();
  client.clear();
});

describe("file tokens", () => {
  // The server's layout: principal(16) file(16) variant length(1) variant expiry(8, big-endian seconds) mac(32).
  const token = (expiresSeconds: number) => {
    const variant = new TextEncoder().encode("thumbnail");
    const bytes = new Uint8Array(33 + variant.length + 8 + 32);
    bytes[32] = variant.length;
    bytes.set(variant, 33);
    new DataView(bytes.buffer).setBigUint64(33 + variant.length, BigInt(expiresSeconds));
    return btoa(String.fromCharCode(...bytes)).replaceAll("+", "-").replaceAll("/", "_").replace(/=+$/, "");
  };
  it("reads the expiry and keeps the URL until shortly before it", () => {
    const now = 1_800_000_000_000;
    expect(tokenExpiry(token(now / 1000 + 600))).toBe(now + 600_000);
    expect(thumbnailLifetime(`/api/admin/files/f?token=${token(now / 1000 + 600)}&variant=thumbnail`, now)).toBe(570_000);
    expect(thumbnailLifetime("/api/files/f?variant=thumbnail", now)).toBe(Infinity);
    expect(thumbnailLifetime("/api/admin/files/f?token=garbage", now)).toBe(60_000);
    expect(thumbnailLifetime(`/api/admin/files/f?token=${token(now / 1000 - 5)}`, now)).toBe(0);
  });
});
