import { describe, expect, it } from "vitest";
import type { FieldDefinition, FieldType, RecordData } from "../backend/ports";
import {
  dirtyNames,
  encodeDraft,
  initialDraft,
  relationTargetOption,
  relationTargetSearch,
  selectRelationTarget,
  validationText,
  type Draft,
} from "./record-codec";

const first = "11111111-1111-4111-8111-111111111111";
const second = "22222222-2222-4222-8222-222222222222";
const third = "33333333-3333-4333-8333-333333333333";

function field(name: string, type: FieldType, patch: Partial<FieldDefinition> = {}): FieldDefinition {
  return { name, type, required: false, unique: false, indexed: false, ordinal: 0, ...patch };
}

function relation(name: string, cardinality: "single" | "multiple", patch: Partial<NonNullable<FieldDefinition["relation"]>> = {}): FieldDefinition {
  return {
    name, type: "relation", required: false, unique: false, indexed: true, ordinal: 0,
    relation: { targetCollectionId: "target-id", cardinality, minSelect: 0, maxSelect: cardinality === "single" ? 1 : 3, onDelete: "restrict", ...patch },
  };
}

function values(fields: FieldDefinition[], entries: Record<string, string | boolean | null>): Draft {
  const draft: Draft = {};
  for (const definition of fields) {
    const value = entries[definition.name];
    draft[definition.name] = value === undefined ? { kind: "unset" } : value === null ? { kind: "null" } : { kind: "value", value };
  }
  return draft;
}

function created(fields: FieldDefinition[], entries: Record<string, string | boolean | null>) {
  return encodeDraft(fields, values(fields, entries), new Set(), true);
}

describe("record draft encoding", () => {
  it("encodes every field type exactly, without rounding a decimal string", () => {
    const fields = [
      field("enabled", "boolean"),
      field("count", "integer"),
      field("views", "bigint"),
      field("amount", "numeric"),
      field("title", "text"),
      field("published_at", "timestamptz"),
      field("external_id", "uuid"),
      field("metadata", "jsonb"),
    ];

    const { input, errors } = created(fields, {
      enabled: true,
      count: "42",
      views: "9223372036854775807",
      amount: "12345678901234567890.12345678901234567890",
      title: "hello",
      published_at: "2026-08-28T12:34:56Z",
      external_id: "ABCDEFAB-CDEF-0ABC-0DEF-ABCDEFABCDEF",
      metadata: `{"tags":["safe"],"rank":2}`,
    });

    expect(errors).toEqual({});
    expect(input).toEqual({
      enabled: true,
      count: 42,
      views: "9223372036854775807",
      amount: "12345678901234567890.12345678901234567890",
      title: "hello",
      published_at: "2026-08-28T12:34:56.000Z",
      external_id: "abcdefab-cdef-0abc-0def-abcdefabcdef",
      metadata: { tags: ["safe"], rank: 2 },
    });
  });

  it("omits an unset field so the database default applies", () => {
    expect(created([field("defaulted", "bigint")], {}).input).toEqual({});
  });

  it("keeps a NULL boolean and a concrete false apart", () => {
    const fields = [field("nullable_flag", "boolean"), field("false_flag", "boolean")];
    expect(created(fields, { nullable_flag: null, false_flag: false }).input).toEqual({ nullable_flag: null, false_flag: false });
  });

  it("submits empty text as a concrete string rather than null", () => {
    const fields = [field("empty_text", "text"), field("required_empty_text", "text", { required: true })];
    expect(created(fields, { empty_text: "", required_empty_text: "" }).input).toEqual({ empty_text: "", required_empty_text: "" });
  });

  it("accepts nil and mixed-case canonical UUIDs and normalises the case", () => {
    const fields = [field("nil_id", "uuid"), field("mixed_id", "uuid")];
    expect(created(fields, { nil_id: "00000000-0000-0000-0000-000000000000", mixed_id: "ABCDEFAB-CDEF-0ABC-0DEF-ABCDEFABCDEF" }).input).toEqual({
      nil_id: "00000000-0000-0000-0000-000000000000",
      mixed_id: "abcdefab-cdef-0abc-0def-abcdefabcdef",
    });
  });

  it.each(["2147483648", "-2147483649", "1.5"])("rejects the integer %s before sending it", (value) => {
    const { input, errors } = created([field("count", "integer")], { count: value });
    expect(input).toEqual({});
    expect(errors.count).toBe("Value does not match the field type.");
  });

  it("reports invalid JSON against the jsonb field that holds it", () => {
    expect(created([field("metadata", "jsonb")], { metadata: "{" }).errors).toEqual({ metadata: "Enter valid JSON." });
  });

  it("sends only the fields an edit actually changed", () => {
    const fields = [field("title", "text"), field("subtitle", "text")];
    const record: RecordData = { title: "old", subtitle: "kept" };
    const initial = initialDraft(fields, record, false);
    const draft: Draft = { ...initial, title: { kind: "value", value: "new" } };

    const dirty = dirtyNames(draft, initial);
    expect([...dirty]).toEqual(["title"]);
    expect(encodeDraft(fields, draft, dirty, false).input).toEqual({ title: "new" });
  });
});

describe("record validation messages", () => {
  it("names the byte limit that applies to the field", () => {
    expect(validationText(field("title", "text", { indexed: true }), "value_too_long")).toBe("Value exceeds the 2,048-byte limit for text values.");
    expect(validationText(field("body", "text"), "value_too_long")).toBe("Value exceeds the 1 MiB limit for text values.");
  });

  it("maps relation codes to the bound the administrator has to satisfy", () => {
    const tags = relation("tags", "multiple", { minSelect: 2, maxSelect: 3 });
    expect(validationText(tags, "relation_min_select")).toBe("Select at least 2 related records.");
    expect(validationText(tags, "relation_max_select")).toBe("Select no more than 3 related records.");
    expect(validationText(tags, "relation_invalid_value")).toBe("Enter one canonical, non-zero UUID per line.");
    expect(validationText(relation("owner", "single"), "relation_invalid_value")).toBe("Enter a canonical, non-zero UUID.");
    expect(validationText(tags, "duplicate_relation_value")).toBe("Related record IDs must be unique.");
    expect(validationText(tags, "relation_target_invalid")).toBe("One or more related records do not exist or are unavailable.");
    expect(validationText(field("title", "text"), "unrecognised")).toBe("Value is invalid.");
  });
});

describe("relation values", () => {
  it("encodes a single relation and an ordered multiple relation", () => {
    const fields = [relation("owner", "single"), relation("tags", "multiple")];
    expect(created(fields, { owner: first, tags: `${second}\n${first}` }).input).toEqual({ owner: first, tags: [second, first] });
  });

  it("clears a single relation with null and a multiple relation with an empty ordered array", () => {
    const fields = [relation("owner", "single"), relation("tags", "multiple")];
    expect(created(fields, { owner: null, tags: "" }).input).toEqual({ owner: null, tags: [] });
  });

  it("starts a multiple relation as a value because it has no NULL", () => {
    const draft = initialDraft([relation("tags", "multiple"), relation("owner", "single")], {}, false);
    expect(draft.tags).toEqual({ kind: "value", value: "" });
    expect(draft.owner).toEqual({ kind: "null" });
  });

  it("rejects invalid, duplicate and out-of-bounds relation values before a request", () => {
    expect(created([relation("owner", "single")], { owner: "not-a-uuid" }).errors.owner).toBe("Enter a canonical, non-zero UUID.");
    expect(created([relation("tags", "multiple")], { tags: `${first}\n${first}` }).errors.tags).toBe("Related record IDs must be unique.");
    expect(created([relation("tags", "multiple", { maxSelect: 2 })], { tags: `${first}\n${second}\n${third}` }).errors.tags).toBe("Select no more than 2 related records.");
    expect(created([relation("tags", "multiple", { minSelect: 2, maxSelect: 3 })], { tags: first }).errors.tags).toBe("Select at least 2 related records.");
  });

  it("requires a required relation the administrator left unset or null", () => {
    const tags = relation("tags", "multiple", { minSelect: 1, maxSelect: 3 });
    expect(created([tags], {}).errors.tags).toBe("Select at least 1 related record.");
    expect(created([tags], { tags: null }).errors.tags).toBe("Enter one canonical, non-zero UUID per line.");
    expect(created([relation("owner", "single", { minSelect: 1 })], { owner: null }).errors.owner).toBe("Select at least 1 related record.");
  });

  it("appends ordered unique selections up to maxSelect", () => {
    const tags = relation("tags", "multiple", { maxSelect: 2 });
    expect(selectRelationTarget(tags, "", first)).toEqual({ draft: first, status: "selected" });
    expect(selectRelationTarget(tags, first, second)).toEqual({ draft: `${first}\n${second}`, status: "selected" });
    expect(selectRelationTarget(tags, `${first}\n${second}`, first)).toEqual({ draft: `${first}\n${second}`, status: "duplicate" });
    expect(selectRelationTarget(tags, `${first}\n${second}`, third)).toEqual({ draft: `${first}\n${second}`, status: "max" });
    expect(selectRelationTarget(relation("owner", "single"), first, second)).toEqual({ draft: second, status: "selected" });
  });
});

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
