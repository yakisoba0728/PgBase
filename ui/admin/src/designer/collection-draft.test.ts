import { describe, expect, it } from "vitest";
import { stringifyJSON } from "../backend/json";
import { buildInput, type DraftField } from "./collection-designer";

function field(name: string, type: DraftField["type"], defaultText: string): DraftField {
  return { name, type, required: false, unique: false, indexed: false, hasDefault: true, defaultText };
}

describe("collection default contract", () => {
  it("emits the exact server representation for every non-trivial default", () => {
    const input = buildInput("create", "defaults", [
      field("enabled", "boolean", "false"),
      field("count", "integer", "42"),
      field("views", "bigint", "9223372036854775807"),
      field("amount", "numeric", "12345678901234567890.123456789"),
      field("empty", "text", ""),
      field("published_at", "timestamptz", "now"),
      field("metadata", "jsonb", `{"sequence":9007199254740993}`),
    ]);

    expect(stringifyJSON(input)).toBe(`{"operation":"create","name":"defaults","fields":[{"name":"enabled","type":"boolean","required":false,"unique":false,"indexed":false,"default":false},{"name":"count","type":"integer","required":false,"unique":false,"indexed":false,"default":"42"},{"name":"views","type":"bigint","required":false,"unique":false,"indexed":false,"default":"9223372036854775807"},{"name":"amount","type":"numeric","required":false,"unique":false,"indexed":false,"default":"12345678901234567890.123456789"},{"name":"empty","type":"text","required":false,"unique":false,"indexed":false,"default":""},{"name":"published_at","type":"timestamptz","required":false,"unique":false,"indexed":false,"default":{"kind":"now"}},{"name":"metadata","type":"jsonb","required":false,"unique":false,"indexed":false,"default":{"sequence":9007199254740993}}]}`);
  });

  it("keeps auth collection kind and server-owned field markers in the request", () => {
    const input = buildInput("create", "members", [{
      ...field("email", "text", ""), system: true, hasDefault: false,
    }], undefined, undefined, "auth");

    expect(input).toMatchObject({
      operation: "create", name: "members", type: "auth",
      fields: [{ name: "email", system: true }],
    });
  });

  it("rejects boolean and integer defaults that the backend will reject", () => {
    expect(() => buildInput("create", "invalid", [field("enabled", "boolean", "yes")])).toThrow();
    expect(() => buildInput("create", "invalid", [field("count", "integer", "1.5")])).toThrow();
    expect(() => buildInput("create", "invalid", [field("amount", "numeric", "1e3")])).toThrow();
  });
});

describe("compound indexes", () => {
  const fields = [field("title", "text", ""), field("rank", "integer", "0")];
  it("preserves untouched update indexes but makes an explicit empty list a clear request", () => {
    expect(buildInput("update", "notes", fields)).not.toHaveProperty("indexes");
    expect(buildInput("update", "notes", fields, [])).toMatchObject({ indexes: [] });
  });
  it("keeps ordered fields and rejects invalid index selections", () => {
    expect(buildInput("create", "notes", fields, [{ name: "title_rank", fields: ["title", "rank"], unique: true }]).indexes).toEqual([{ name: "title_rank", fields: ["title", "rank"], unique: true }]);
    expect(() => buildInput("create", "notes", fields, [{ name: "bad", fields: ["title"], unique: false }])).toThrow("two to eight");
  });
});

describe("view collection payload", () => {
  it("omits a view for normal collections and carries source, projection, and static filter for a view", () => {
    const fields = [field("title", "text", "")];
    expect(buildInput("create", "notes", fields)).not.toHaveProperty("view");
    expect(buildInput("create", "recent_notes", fields, undefined, {
      source: "notes", projection: ["title"], filter: { op: "eq", field: "title", value: "published" },
    })).toEqual({ operation: "create", name: "recent_notes", fields: [], view: { source: "notes", projection: ["title"], filter: { op: "eq", field: "title", value: "published" } } });
  });
});

describe("collection relation input contract", () => {
  const relationField = (patch: Partial<DraftField> = {}): DraftField => ({
    name: "tags",
    type: "relation",
    required: true,
    unique: false,
    indexed: true,
    hasDefault: false,
    defaultText: "",
    relation: {
      targetCollection: "tags",
      cardinality: "multiple",
      minSelect: "2",
      maxSelect: "20",
      onDelete: "cascade",
    },
    ...patch,
  });

  it("emits numeric bounded relation members without draft-only state", () => {
    expect(buildInput("create", "posts", [relationField()])).toEqual({
      operation: "create",
      name: "posts",
      fields: [{
        name: "tags",
        type: "relation",
        required: true,
        unique: false,
        indexed: true,
        relation: { targetCollection: "tags", cardinality: "multiple", minSelect: 2, maxSelect: 20, onDelete: "cascade" },
      }],
    });
  });

  it("rejects out-of-range, inconsistent, and required set-null relation drafts", () => {
    expect(() => buildInput("create", "posts", [relationField({ relation: {
      targetCollection: "tags", cardinality: "multiple", minSelect: "0", maxSelect: "1", onDelete: "restrict",
    }, required: false })])).toThrow(/maximum 2–1000/);
    expect(() => buildInput("create", "posts", [relationField({ required: false })])).toThrow(/required setting must match/);
    expect(() => buildInput("create", "posts", [relationField({ relation: {
      targetCollection: "tags", cardinality: "multiple", minSelect: "2", maxSelect: "20", onDelete: "set_null",
    } })])).toThrow(/set-null relation must be optional/);
  });
});
