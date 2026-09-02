import { describe, expect, it } from "vitest";
import type { FieldDefinition, FieldType } from "../backend/ports";
import { relationTargetOption, relationTargetSearch } from "./relation-query";

const first = "11111111-1111-4111-8111-111111111111";

function field(name: string, type: FieldType, patch: Partial<FieldDefinition> = {}): FieldDefinition {
  return { name, type, required: false, unique: false, indexed: false, ordinal: 0, ...patch };
}

describe("relation target search", () => {
  const searchable = { id: "t", name: "users", tableName: "users", fields: [field("biography", "text", { ordinal: 0 }), field("name", "text", { ordinal: 1, indexed: true })] };
  const opaque = { id: "t", name: "tags", tableName: "tags", fields: [] };

  it("looks up the first indexed text field and escapes the literal", () => {
    expect(relationTargetSearch(searchable, "O'Hara")).toEqual({
      displayField: expect.objectContaining({ name: "name" }),
      filter: "name = 'O''Hara'",
      sort: "name",
    });
  });

  it("falls back to a canonical UUID lookup when the target has no searchable text field", () => {
    expect(relationTargetSearch(opaque, first.toUpperCase())).toEqual({ filter: `id = '${first}'` });
    expect(relationTargetSearch(opaque, "not-a-uuid")).toEqual({ problem: "invalid-uuid" });
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
