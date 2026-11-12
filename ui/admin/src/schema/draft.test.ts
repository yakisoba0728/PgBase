import { describe, expect, it } from "vitest";
import type { Collection, PlannedCollection } from "../api/collections";
import { buildChange, dangerousStatement, diffPlan, draftFromCollection, DraftInvalid, emptyDraft, filterFromRows, filterRows, indexInput, mapErrors, pruneOptions, rawIndexTemplate, rebaseDraft } from "./draft";

const field = (id: string, name: string, type: Collection["fields"][number]["type"], extra: Partial<Collection["fields"][number]> = {}) => ({
  id, name, columnName: name, type, required: false, unique: false, indexed: false, ordinal: 0, hidden: false, presentable: false, ...extra,
});

const authors: Collection = { id: "c0", name: "authors", tableName: "authors", fields: [field("a1", "name", "text")], createdAt: "" };
const posts: Collection = {
  id: "c1",
  name: "posts",
  type: "base",
  tableName: "posts",
  createdAt: "",
  fields: [
    field("f1", "title", "text", { required: true, options: { maxLength: 40 } }),
    field("f2", "author", "relation", {
      relation: { targetCollectionId: "c0", cardinality: "single", minSelect: 0, maxSelect: 1, onDelete: "set_null", valueCodecVersion: 1, storage: { kind: "inline_fk_v1", name: "x" } },
    }),
    field("f3", "cover", "file"),
  ],
  indexes: [{ id: "i1", name: "by_title", fields: ["title", "author"], indexName: "idx_x", unique: false, keys: [{ field: "title", order: "asc" }, { field: "author", order: "desc" }], where: "title <> ''" }],
};

const planned = (collection: Collection, change: (fields: PlannedCollection["fields"]) => PlannedCollection["fields"]): PlannedCollection => ({
  name: collection.name,
  type: "base",
  validationMode: "postgres",
  tableName: collection.name,
  fields: change(collection.fields.map(({ columnName: _c, relation: _r, ...rest }) => rest)),
  indexes: collection.indexes,
});

describe("draft conversion", () => {
  it("round-trips the catalog into an update with ids, relation names and one index shape", () => {
    const draft = draftFromCollection(posts, [authors, posts]);
    const { change, fieldKeys } = buildChange(draft, posts, draft);
    expect(change).toMatchObject({ operation: "update", name: "posts", collectionId: "c1" });
    if (change.operation !== "update" || !change.fields) throw new Error("expected fields");
    expect(change.fields.map((input) => input.id)).toEqual(["f1", "f2", "f3"]);
    expect(change.fields[1].relation).toEqual({ targetCollection: "authors", cardinality: "single", onDelete: "set_null", maxSelect: 1 });
    expect(change.indexes?.[0]).toEqual({ name: "by_title", unique: false, keys: posts.indexes![0].keys, where: "title <> ''" });
    expect(fieldKeys).toEqual(["f1", "f2", "f3"]);
  });

  it("never sends field ids on create and rejects a bad default before planning", () => {
    const draft = { ...draftFromCollection(posts, [authors]), name: "copy" };
    const { change } = buildChange(draft, undefined, undefined);
    expect(change.operation === "create" && change.fields?.every((input) => input.id === undefined)).toBe(true);
    draft.fields[0] = { ...draft.fields[0], defaultText: "{oops" };
    expect(() => buildChange(draft, undefined, undefined)).toThrow(DraftInvalid);
  });

  it("plans a collection rename alone and refuses it mixed with other edits", () => {
    const base = draftFromCollection(posts, [authors]);
    expect(buildChange({ ...base, name: "articles" }, posts, base).change).toEqual({ operation: "update", name: "articles", collectionId: "c1" });
    const mixed = { ...base, name: "articles", fields: base.fields.slice(1) };
    expect(() => buildChange(mixed, posts, base)).toThrow(DraftInvalid);
  });

  it("drops options a type does not take", () => {
    expect(pruneOptions("integer", { min: "0", maxLength: 5, pattern: "" })).toEqual({ min: "0" });
    expect(pruneOptions("file", { protected: false, maxSelect: 2 })).toEqual({ maxSelect: 2, protected: false });
    expect(pruneOptions("text", { allowedValues: [] })).toBeUndefined();
    expect(indexInput({ id: "", indexName: "", name: "raw", fields: ["a"], unique: true, rawSql: "CREATE UNIQUE INDEX raw ON t (a)" })).toEqual({ name: "raw", unique: true, rawSql: "CREATE UNIQUE INDEX raw ON t (a)" });
  });
});

describe("diffPlan", () => {
  it("reports renames and moves (which emit no SQL) by field id", () => {
    const next = planned(posts, (fields) => [{ ...fields[1] }, { ...fields[0], name: "headline" }, fields[2]]);
    const items = diffPlan(posts, next);
    expect(items).toContainEqual({ kind: "rename", target: "field", name: "headline", detail: "title", danger: false });
    expect(items.filter((item) => item.kind === "move").map((item) => item.name)).toEqual(["author", "headline"]);
    expect(items.some((item) => item.danger)).toBe(false);
  });

  it("flags dropped fields (including file fields with no column), type changes and deletes as data loss", () => {
    const next = planned(posts, (fields) => [{ ...fields[0], type: "editor" }]);
    const items = diffPlan(posts, next);
    expect(items.filter((item) => item.danger).map((item) => `${item.kind}:${item.name}`)).toEqual(["change:title", "remove:author", "remove:cover"]);
    expect(diffPlan(posts, undefined)).toEqual([{ kind: "remove", target: "collection", name: "posts", danger: true }]);
    const view = { ...posts, type: "view" as const };
    expect(diffPlan(view, { ...next, type: "view" }).some((item) => item.danger)).toBe(false);
  });

  it("sees option and index changes", () => {
    const next = planned(posts, (fields) => fields.map((input) => (input.name === "title" ? { ...input, options: { maxLength: 80 } } : input)));
    next.indexes = [];
    const items = diffPlan(posts, next);
    expect(items).toContainEqual({ kind: "change", target: "field", name: "title", detail: "options", danger: false });
    expect(items).toContainEqual({ kind: "remove", target: "index", name: "by_title", danger: false });
  });

  it("marks destructive SQL", () => {
    expect(dangerousStatement('ALTER TABLE "app"."posts" DROP COLUMN "title"')).toBe(true);
    expect(dangerousStatement('ALTER TABLE "app"."posts" ALTER COLUMN "n" TYPE bigint')).toBe(true);
    expect(dangerousStatement('ALTER TABLE "app"."posts" ADD COLUMN "bio" text')).toBe(false);
    expect(dangerousStatement('DROP INDEX "app"."idx"')).toBe(false);
    expect(dangerousStatement('DROP VIEW "app"."v"')).toBe(false);
  });
});

describe("mapErrors", () => {
  it("maps positional, import-prefixed and collection paths back to rows", () => {
    const draft = draftFromCollection(posts, [authors]);
    const keys = { fieldKeys: ["f1", "f2", "f3"], indexKeys: ["i1"] };
    const mapped = mapErrors(
      { "fields.1.name": ["duplicate_name"], "collections.0.fields.2.options": ["invalid_options"], "indexes.0": ["invalid_shape"], name: ["invalid_name"], weird: ["x"] },
      keys,
      draft,
    );
    expect(mapped.fields).toEqual({ f2: ["name: duplicate_name"], f3: ["options: invalid_options"] });
    expect(mapped.indexes).toEqual({ i1: ["invalid_shape"] });
    expect(mapped.collection).toEqual({ name: ["invalid_name"] });
    expect(mapped.other).toEqual(["weird: x"]);
  });
});

describe("views and index authoring", () => {
  const view: Collection = { id: "v1", name: "recent", type: "view", tableName: "recent", createdAt: "", fields: [field("t", "title", "text")], view: { sourceCollectionId: "c1", source: "posts", projection: ["title"], filter: { op: "eq", field: "title", value: "x" } } };

  it("creates and updates a declarative view through `view`, never viewQuery", () => {
    const draft = { ...emptyDraft("view"), name: "recent", viewMode: "declarative" as const, view: { source: "posts", projection: ["title"] } };
    expect(buildChange(draft, undefined, undefined).change).toEqual({ operation: "create", name: "recent", type: "view", validationMode: "postgres", view: { source: "posts", projection: ["title"] } });
    const loaded = draftFromCollection(view, [posts, view]);
    expect(loaded.viewMode).toBe("declarative");
    const edited = { ...loaded, view: { ...loaded.view, projection: [] as string[] } };
    expect(buildChange(edited, view, loaded).change).toEqual({ operation: "update", name: "recent", collectionId: "v1", view: { source: "posts", projection: [], filter: { op: "eq", field: "title", value: "x" } } });
  });

  it("edits flat filters as rows and refuses nested ones", () => {
    expect(filterRows({ op: "eq", field: "n", value: 3 })).toEqual({ join: "all", rows: [{ field: "n", op: "eq", value: "3" }] });
    expect(filterRows({ op: "all", args: [{ op: "any", args: [] }] })).toBeNull();
    const types = { n: "integer", ok: "boolean", title: "text" } as const;
    const rows = [{ field: "n", op: "gte" as const, value: "10" }, { field: "ok", op: "eq" as const, value: "false" }, { field: "title", op: "ne" as const, value: "null" }];
    expect(filterFromRows("any", rows, types)).toEqual({ op: "any", args: [{ op: "gte", field: "n", value: 10 }, { op: "eq", field: "ok", value: false }, { op: "ne", field: "title", value: null }] });
    expect(filterFromRows("all", [{ field: "title", op: "eq", value: "10" }], types)).toEqual({ op: "eq", field: "title", value: "10" });
    expect(filterFromRows("all", [], types)).toBeUndefined();
  });

  it("writes a raw index whose name, uniqueness and keys match the input", () => {
    expect(rawIndexTemplate({ name: "by_title", unique: true, keys: [{ field: "title", collation: "C" }, { expression: "lower(slug)", order: "desc" }], where: "published" }, "posts")).toBe(
      'CREATE UNIQUE INDEX by_title ON app.posts (title COLLATE "C", (lower(slug)) DESC) WHERE published',
    );
    expect(rawIndexTemplate({ name: "Idx", unique: false, fields: ["a"] }, "my posts")).toBe('CREATE INDEX "Idx" ON app."my posts" (a)');
  });
});

describe("rebaseDraft", () => {
  const base = draftFromCollection(posts, [authors, posts]);
  // Another administrator added "price" and made "cover" required.
  const latestCollection: Collection = { ...posts, fields: [...posts.fields.map((f) => (f.id === "f3" ? { ...f, required: true } : f)), field("f4", "price", "numeric")] };
  const latest = draftFromCollection(latestCollection, [authors, latestCollection]);

  it("keeps the other administrator's new field and flag while replaying this draft's edit", () => {
    const draft = { ...base, fields: base.fields.map((f) => (f.key === "f1" ? { ...f, help: "Shown on cards" } : f)) };
    const result = rebaseDraft(draft, base, latest);
    expect(result.conflicts).toEqual([]);
    expect(result.draft.fields.map((f) => f.name)).toEqual(["title", "author", "cover", "price"]);
    expect(result.draft.fields.find((f) => f.key === "f1")?.help).toBe("Shown on cards");
    expect(result.draft.fields.find((f) => f.key === "f3")?.required).toBe(true);
    // The rebased change no longer drops "price".
    const { change } = buildChange(result.draft, latestCollection, latest);
    if (change.operation !== "update") throw new Error("expected update");
    expect(change.fields?.map((f) => f.name)).toContain("price");
  });

  it("keeps a local removal and a local addition, and reports edits both sides made", () => {
    const added = { ...base.fields[0], key: "new1", id: undefined, name: "subtitle" };
    const draft = { ...base, fields: [base.fields[0], { ...base.fields[2], required: false, help: "mine" }, added] };
    const result = rebaseDraft(draft, base, latest);
    expect(result.draft.fields.map((f) => f.name)).toEqual(["title", "cover", "price", "subtitle"]);
    expect(result.conflicts).toEqual(["cover"]);
    expect(result.draft.fields.find((f) => f.key === "f3")?.required).toBe(true);
  });

  it("restores a field removed here but changed there, as a conflict", () => {
    const draft = { ...base, fields: base.fields.filter((f) => f.key !== "f3") };
    const result = rebaseDraft(draft, base, latest);
    expect(result.draft.fields.map((f) => f.name)).toEqual(["title", "author", "cover", "price"]);
    expect(result.conflicts).toEqual(["cover"]);
  });
});
