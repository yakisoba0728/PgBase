import { describe, expect, it } from "vitest";
import { stringifyJSON } from "../backend/json";
import { buildInput, type DraftField } from "./collection-field-editor";

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

  it("rejects boolean and integer defaults that the backend will reject", () => {
    expect(() => buildInput("create", "invalid", [field("enabled", "boolean", "yes")])).toThrow();
    expect(() => buildInput("create", "invalid", [field("count", "integer", "1.5")])).toThrow();
    expect(() => buildInput("create", "invalid", [field("amount", "numeric", "1e3")])).toThrow();
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
