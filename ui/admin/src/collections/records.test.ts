import { afterEach, describe, expect, it } from "vitest";
import type { Collection, CollectionField, FieldType } from "../api/collections";
import { visibleRange } from "../components/virtualRows";
import { ApiError } from "../api/http";
import { collectAll, deleteAll } from "./bulk";
import { moveColumn, orderedColumns, toggleColumn, visibleColumns } from "./columns";
import { changedFields, keepDraft, clearDraft, readDraft, writeDraft } from "./recordDraft";
import { expandFor, htmlText, labelField, recordLabel, relationSearchFilter } from "./relation";

const field = (name: string, type: FieldType, extra: Partial<CollectionField> = {}): CollectionField => ({
  id: name, name, columnName: name, type, required: false, unique: false, indexed: false, ordinal: 0, hidden: false, presentable: false, ...extra,
});
const collection = (fields: CollectionField[], extra: Partial<Collection> = {}): Collection => ({ id: "c1", name: "posts", tableName: "posts", fields, createdAt: "", ...extra });

describe("column preferences", () => {
  const available = ["title", "body", "score", "created_at"];
  it("keeps the saved order, drops removed fields and appends new ones", () => {
    expect(visibleColumns(available, { order: ["score", "gone", "title"], hidden: ["body"] })).toEqual(["score", "title", "created_at"]);
  });
  it("moves and toggles columns", () => {
    const moved = moveColumn(available, { order: [], hidden: [] }, "score", -1);
    expect(orderedColumns(available, moved)).toEqual(["title", "score", "body", "created_at"]);
    expect(moveColumn(available, moved, "title", -1)).toBe(moved);
    expect(toggleColumn(toggleColumn(moved, "body", false), "body", true).hidden).toEqual([]);
  });
});

describe("record drafts", () => {
  const fields = [field("title", "text"), field("secret", "password"), field("api_token", "text"), field("done", "boolean")];
  const scope = { admin: "a", collection: "c", record: "r" };
  afterEach(() => clearDraft(scope));

  it("stores only changed, persistable fields", () => {
    const initial = { title: "a", secret: "", api_token: "x", done: false };
    expect(changedFields(fields, initial, initial)).toBeNull();
    expect(changedFields(fields, { title: "b", secret: "pw", api_token: "y", done: true }, initial)).toEqual({ title: "b", done: true });
  });

  it("restores fresh drafts, drops expired or mistyped ones", () => {
    writeDraft(scope, { title: "b", done: "yes" }, 1000);
    expect(readDraft(scope, fields, 2000)?.fields).toEqual({ title: "b" });
    expect(readDraft(scope, fields, 1000 + 8 * 24 * 3600 * 1000)).toBeNull();
    expect(readDraft(scope, fields, 2000)).toBeNull();
  });
});

describe("relations", () => {
  const authors = collection([field("bio", "editor", { ordinal: 1 }), field("email", "text", { ordinal: 2, unique: true }), field("name", "text", { ordinal: 3, presentable: true })]);
  it("labels records with the presentable field, falling back to the id", () => {
    expect(labelField(authors)?.name).toBe("name");
    expect(labelField(collection([field("email", "text", { unique: true })]))?.name).toBe("email");
    expect(recordLabel({ id: "1", name: "Ada" }, labelField(authors), "1")).toBe("Ada");
    expect(recordLabel(undefined, labelField(authors), "1")).toBe("1");
  });
  it("builds search filters that escape quotes and only match ids that can exist", () => {
    expect(relationSearchFilter(authors, "  ")).toBe("");
    expect(relationSearchFilter(authors, "O'Hara")).toBe("name:lower ~ 'o''hara'");
    expect(relationSearchFilter(authors, "01a1d080-ebcf-7ea9-9a95-7cf7ca573cd2")).toBe("name:lower ~ '01a1d080-ebcf-7ea9-9a95-7cf7ca573cd2' || id = '01a1d080-ebcf-7ea9-9a95-7cf7ca573cd2'");
    expect(relationSearchFilter(collection([]), "abc")).toBeNull();
    expect(relationSearchFilter(collection([], { recordId: { kind: "text" } }), "abc")).toBe("id = 'abc'");
  });
  it("expands at most ten relation fields", () => {
    const many = collection(Array.from({ length: 12 }, (_, index) => field(`r${index}`, "relation")));
    expect(expandFor(many).split(",")).toHaveLength(10);
    expect(expandFor(many, ["r3", "x"])).toBe("r3");
  });
  it("summarises HTML as plain text", () => {
    expect(htmlText("<h2>T&lt;1&gt;</h2><script>x()</script><p>a&nbsp;b</p>")).toBe("T<1> a b");
    expect(htmlText("<p>c<em>e</em>ll</p><p>next</p>")).toBe("cell next");
  });
});

describe("bulk helpers", () => {
  it("walks pages until the cursor ends or the page cap", async () => {
    const pages = [{ items: [{ id: "1" }], nextCursor: "a" }, { items: [{ id: "2" }] }];
    expect(await collectAll(async (cursor) => pages[cursor ? 1 : 0])).toEqual({ items: [{ id: "1" }, { id: "2" }], complete: true });
    expect((await collectAll(async () => pages[0], 3)).complete).toBe(false);
  });
});

describe("deleteAll", () => {
  const ids = Array.from({ length: 60 }, (_, index) => `r${index}`);
  const run = (options: Partial<Parameters<typeof deleteAll>[1]> & { signal?: AbortSignal }) =>
    deleteAll(ids, { batchSize: 0, batch: async () => {}, one: async () => {}, signal: new AbortController().signal, onProgress: () => {}, ...options });

  it("sends one batch per chunk and reports progress", async () => {
    const chunks: number[] = [];
    const progress: number[] = [];
    const outcome = await run({ batchSize: 25, batch: async (chunk) => void chunks.push(chunk.length), onProgress: (done) => progress.push(done) });
    expect(chunks).toEqual([25, 25, 10]);
    expect(progress).toEqual([25, 50, 60]);
    expect(outcome).toMatchObject({ failed: [], stopped: false });
    expect(outcome.deleted).toHaveLength(60);
  });
  it("retries a refused (atomic) batch record by record, counting a 404 as deleted", async () => {
    const outcome = await run({
      batchSize: 50,
      batch: async (chunk) => { if (chunk.includes("r3")) throw new ApiError(409, "restricted", "no"); },
      one: async (id) => {
        if (id === "r3") throw new ApiError(409, "restricted", "no");
        if (id === "r4") throw new ApiError(404, "not_found", "gone");
      },
    });
    expect(outcome.failed).toEqual(["r3"]);
    expect(outcome.reason).toBeInstanceOf(ApiError);
    expect(outcome.deleted).toHaveLength(59);
  });
  it("falls back to single deletes for good when batches are disabled", async () => {
    let batches = 0;
    const singles: string[] = [];
    await run({ batchSize: 10, batch: async () => { batches++; throw new ApiError(503, "batch_disabled", "off"); }, one: async (id) => void singles.push(id) });
    expect(batches).toBe(1);
    expect(singles).toHaveLength(60);
  });
  it("stops issuing requests once cancelled; what was sent stays deleted", async () => {
    const abort = new AbortController();
    const sent: string[] = [];
    const outcome = await run({ signal: abort.signal, one: async (id) => void sent.push(id), onProgress: () => abort.abort() });
    expect(sent).toHaveLength(24);
    expect(outcome).toMatchObject({ stopped: true, failed: [] });
    expect(outcome.deleted).toEqual(sent);
  });
  it("sends nothing more once cancelled mid-chunk", async () => {
    const abort = new AbortController();
    const sent: string[] = [];
    const outcome = await run({ signal: abort.signal, one: async (id) => { sent.push(id); if (sent.length === 3) abort.abort(); } });
    expect(sent.length).toBeLessThanOrEqual(8);
    expect(outcome.stopped).toBe(true);
    expect(outcome.deleted).toEqual(sent);
  });
});

describe("visibleRange", () => {
  it("renders only the rows in view plus overscan", () => {
    expect(visibleRange(0, 400, 40, 20000, 5)).toEqual({ start: 0, end: 15 });
    expect(visibleRange(40_000, 400, 40, 20000, 5)).toEqual({ start: 995, end: 1015 });
    expect(visibleRange(10_000_000, 400, 40, 100, 5)).toEqual({ start: 99, end: 100 });
    expect(visibleRange(0, 400, 40, 0)).toEqual({ start: 0, end: 0 });
  });
});

it("keeps sensitive 412 edits only in memory and rejects mismatched persisted shapes", () => {
  const scope = { admin: "a", collection: "c", record: "r" };
  const fields = [field("api_token", "text"), field("title", "text"), field("tags", "jsonb", { options: { allowedValues: ["a"] } })];
  writeDraft(scope, { title: ["wrong"], tags: "wrong" });
  expect(readDraft(scope, fields)).toBeNull();
  keepDraft(scope, { api_token: "secret", title: "mine" });
  expect(readDraft(scope, fields)?.fields).toEqual({ api_token: "secret", title: "mine" });
  expect(JSON.stringify(localStorage)).not.toContain("secret");
  clearDraft(scope);
});

it("restores a stored geoPoint draft", () => {
  const scope = { admin: "a", collection: "c", record: "geo" };
  writeDraft(scope, { place: ["126.9", "37.5"] });
  expect(readDraft(scope, [field("place", "geoPoint")])?.fields).toEqual({ place: ["126.9", "37.5"] });
  clearDraft(scope);
});

it("clearing a draft also forgets edits kept in memory", () => {
  const scope = { admin: "a", collection: "c", record: "mem" };
  keepDraft(scope, { title: "kept" });
  clearDraft(scope);
  expect(readDraft(scope, [field("title", "text")])).toBeNull();
});
