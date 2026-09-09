import { describe, expect, it } from "vitest";
import type { CollectionDefinition, FieldDefinition, FieldType } from "../backend/ports";
import {
  deselectRelationTarget,
  relationDraftSelection,
  relationHydrationBatch,
  relationHydrationFilter,
  relationTargetOption,
  relationTargetSearch,
	relationTargetSearchField,
} from "./relation-query";

const first = "11111111-1111-4111-8111-111111111111";
const second = "22222222-2222-4222-8222-222222222222";
const third = "33333333-3333-4333-8333-333333333333";

function field(name: string, type: FieldType, patch: Partial<FieldDefinition> = {}): FieldDefinition {
  return { name, type, required: false, unique: false, indexed: false, ordinal: 0, ...patch };
}

describe("relation target search", () => {
  const searchable = { id: "t", name: "users", tableName: "users", fields: [field("biography", "text", { ordinal: 0 }), field("name", "text", { ordinal: 1, indexed: true })] };
  const opaque = { id: "t", name: "tags", tableName: "tags", fields: [] };

	it("prefers presentable text and excludes hidden candidates", () => {
		const collection = { id: "t", name: "people", tableName: "people", fields: [field("internal", "text", { indexed: true, hidden: true }), field("fallback", "text", { indexed: true }), field("label", "text", { indexed: true, presentable: true })] };
		expect(relationTargetSearchField(collection)?.name).toBe("label");
	});

  it("looks up the first indexed text field and escapes the literal", () => {
    expect(relationTargetSearch(searchable, "O'Hara")).toEqual({
      displayField: expect.objectContaining({ name: "name" }),
      filter: "name = 'O''Hara'",
      sort: "name",
    });
  });

  it("browses the indexed order rather than demanding a value for an empty query", () => {
    expect(relationTargetSearch(searchable, "  ")).toEqual({ displayField: expect.objectContaining({ name: "name" }), sort: "name" });
    // No sort leaves the read kernel on its own indexed `created_at, id` order.
    expect(relationTargetSearch(opaque, "")).toEqual({});
  });

  it("falls back to a canonical UUID lookup when the target has no searchable text field", () => {
    expect(relationTargetSearch(opaque, first.toUpperCase())).toEqual({ filter: `id = '${first}'` });
    expect(relationTargetSearch(opaque, "not-a-uuid")).toEqual({ problem: "invalid-uuid" });
  });

  it("never names a field the read kernel cannot serve from an index", () => {
    // Only text field present, and it carries neither an index nor a unique
    // constraint: naming it would be the unindexed scan the planner refuses.
    const unindexed: CollectionDefinition = { id: "t", name: "notes", tableName: "notes", fields: [field("body", "text", { ordinal: 0 })] };
    expect(relationTargetSearch(unindexed, "anything")).toEqual({ problem: "invalid-uuid" });
    expect(relationTargetSearch(unindexed, "")).toEqual({});
    expect(relationTargetSearch(unindexed, first)).toEqual({ filter: `id = '${first}'` });

    // A jsonb or non-text field is never a lookup field either, indexed or not.
    const wrongType: CollectionDefinition = { id: "t", name: "notes", tableName: "notes", fields: [field("payload", "jsonb", { ordinal: 0, indexed: true })] };
    expect(relationTargetSearch(wrongType, "anything")).toEqual({ problem: "invalid-uuid" });
  });

  it("refuses a query longer than the search bound or carrying a NUL", () => {
    expect(relationTargetSearch(searchable, "x".repeat(1025))).toEqual({ problem: "too-long" });
    expect(relationTargetSearch(searchable, "a\0b")).toEqual({ problem: "too-long" });
  });

  it("ignores a result row whose id is not a canonical UUID", () => {
    const displayField = field("name", "text", { indexed: true });
    expect(relationTargetOption({ id: first, name: "Ada" }, displayField)).toEqual({ id: first, label: "Ada" });
    expect(relationTargetOption({ id: first, name: "" }, displayField)).toEqual({ id: first, label: first });
    expect(relationTargetOption({ id: "00000000-0000-0000-0000-000000000000" })).toBeUndefined();
    expect(relationTargetOption({ id: 7 })).toBeUndefined();
  });
});

describe("relation draft selection", () => {
  it("reads the draft as ordered unique canonical ids", () => {
    expect(relationDraftSelection(`${second}\n${first}\n${second.toUpperCase()}\n\nnot-a-uuid`)).toEqual([second, first]);
    expect(relationDraftSelection("00000000-0000-0000-0000-000000000000")).toEqual([]);
    expect(relationDraftSelection(first)).toEqual([first]);
  });

  it("drops one selection without reordering the rest", () => {
    expect(deselectRelationTarget(`${third}\n${second}\n${first}`, second)).toBe(`${third}\n${first}`);
    expect(deselectRelationTarget(first, first)).toBe("");
    expect(deselectRelationTarget(`${first}\n${second}`, third)).toBe(`${first}\n${second}`);
  });
});

describe("relation hydration", () => {
  it("keeps a full batch inside the filter's node and byte budgets", () => {
    const ids = Array.from({ length: relationHydrationBatch }, (_ignored, index) => `00000000-0000-4000-8000-${index.toString(16).padStart(12, "0")}`);
    const filter = relationHydrationFilter(ids);

    expect(filter.startsWith(`id = '${ids[0]}' || id = '${ids[1]}'`)).toBe(true);
    // The server rejects a filter over 4,096 bytes or 100 parsed nodes; one
    // comparison per id plus the enclosing disjunction is the node cost.
    expect(new TextEncoder().encode(filter).length).toBeLessThanOrEqual(4096);
    expect(ids.length + 1).toBeLessThanOrEqual(100);
  });
});
