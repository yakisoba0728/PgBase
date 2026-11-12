import { describe, expect, it } from "vitest";
import type { Collection, PlannedCollection } from "../api/collections";
import { buildChange, dangerousStatement, diffPlan, draftFromCollection, DraftInvalid, indexInput, mapErrors, pruneOptions } from "./draft";

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
