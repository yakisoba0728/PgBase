import { describe, expect, it } from "vitest";
import type { FieldDefinition, FieldType, RecordData, RecordIDPolicy } from "../backend/ports";
import {
  dirtyNames,
  encodeDraft,
  initialDraft,
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
  const textPolicy: RecordIDPolicy = { kind: "text", minLength: 1, maxLength: 255, pattern: "(?i)^[a-z0-9-]+$" };
  const textPolicies = new Map([["target-id", textPolicy]]);

  it("round trips text relation identities exactly, including UUID-shaped text", () => {
    const fields = [relation("owner", "single"), relation("tags", "multiple")];
    const id = "ABCDEFAB-CDEF-4ABC-8DEF-ABCDEFABCDEF";
    const record = { owner: id, tags: ["MixedCase01", "mixedcase01"] };
    const draft = initialDraft(fields, record, false, textPolicies);

    expect(draft.owner).toEqual({ kind: "value", value: id });
    expect(encodeDraft(fields, draft, new Set(["owner", "tags"]), false, textPolicies)).toEqual({ input: record, errors: {} });
  });

  it("does not trim text relation identities into another valid target", () => {
    const fields = [relation("owner", "single"), relation("tags", "multiple")];
    const draft = values(fields, { owner: " MixedCase01", tags: "MixedCase01\nMixedCase02 " });
    const result = encodeDraft(fields, draft, new Set(), true, textPolicies);

    expect(result.input).toEqual({});
    expect(result.errors).toEqual({ owner: "Enter an exact target record ID.", tags: "Enter one exact target record ID per line." });
  });

  it("keeps case-distinct text selections and rejects exact duplicates", () => {
    const tags = relation("tags", "multiple");
    expect(selectRelationTarget(tags, "MixedCase01", "mixedcase01", textPolicy)).toEqual({ draft: "MixedCase01\nmixedcase01", status: "selected" });
    expect(selectRelationTarget(tags, "MixedCase01", "MixedCase01", textPolicy)).toEqual({ draft: "MixedCase01", status: "duplicate" });
    const result = encodeDraft([tags], values([tags], { tags: "MixedCase01\nMixedCase01" }), new Set(), true, textPolicies);
    expect(result.errors.tags).toBe("Related record IDs must be unique.");
  });

  it("checks text identity length as Unicode characters and leaves server regex syntax to the server", () => {
    const owner = relation("owner", "single");
    const policy: RecordIDPolicy = { kind: "text", minLength: 2, maxLength: 2, pattern: "(?i)^.+$" };
    const policies = new Map([["target-id", policy]]);
    expect(encodeDraft([owner], values([owner], { owner: "𐐀A" }), new Set(), true, policies)).toEqual({ input: { owner: "𐐀A" }, errors: {} });
    expect(encodeDraft([owner], values([owner], { owner: "ABC" }), new Set(), true, policies).errors.owner).toBe("Enter an exact target record ID.");
  });

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
