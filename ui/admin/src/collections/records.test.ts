import { afterEach, describe, expect, it } from "vitest";
import type { Collection, CollectionField, FieldType } from "../api/collections";
import { visibleRange } from "../components/virtualRows";
import { collectAll, settleLimited } from "./bulk";
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
  it("limits concurrency and keeps results in order", async () => {
    let running = 0;
    let peak = 0;
    const results = await settleLimited([1, 2, 3, 4, 5], 2, async (n) => {
      peak = Math.max(peak, ++running);
      await new Promise((resolve) => setTimeout(resolve, 1));
      running--;
      if (n === 3) throw new Error("no");
      return n * 2;
    });
    expect(peak).toBe(2);
    expect(results.map((result) => result.status)).toEqual(["fulfilled", "fulfilled", "rejected", "fulfilled", "fulfilled"]);
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
