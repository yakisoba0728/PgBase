import { LosslessNumber } from "lossless-json";
import { describe, expect, it } from "vitest";
import type { CollectionField, FieldType } from "../api/collections";
import { parseJson, stringifyJson } from "../api/http";
import type { RecordData } from "../api/records";
import { buildInput, cellView, initialDrafts, isDirty, shortDate } from "./fields";

const field = (name: string, type: FieldType, extra: Partial<CollectionField> = {}): CollectionField => ({
  id: name,
  name,
  columnName: name,
  type,
  required: false,
  unique: false,
  indexed: false,
  ordinal: 0,
  hidden: false,
  presentable: false,
  ...extra,
});

describe("cellView", () => {
  it("shows numbers exactly as received", () => {
    const record = parseJson('{"big": 9007199254740993, "price": "1.50", "count": 42}') as Record<string, unknown>;
    expect(record.big).toBeInstanceOf(LosslessNumber);
    expect(cellView(field("big", "integer"), record.big)).toEqual({ kind: "number", text: "9007199254740993" });
    expect(cellView(field("price", "numeric"), record.price)).toEqual({ kind: "number", text: "1.50" });
    expect(cellView(field("count", "bigint"), record.count)).toEqual({ kind: "number", text: "42" });
  });

  it("renders type-specific views and empties", () => {
    expect(cellView(field("t", "text"), "")).toEqual({ kind: "empty" });
    expect(cellView(field("t", "text"), null)).toEqual({ kind: "empty" });
    expect(cellView(field("b", "boolean"), false)).toEqual({ kind: "bool", value: false });
    expect(cellView(field("d", "timestamptz"), "2026-11-12T10:30:05.123456Z")).toEqual({ kind: "date", text: "2026-11-12 10:30:05Z", title: "2026-11-12T10:30:05.123456Z" });
    expect(cellView(field("s", "text", { options: { allowedValues: ["a", "b"] } }), "a")).toEqual({ kind: "chips", items: ["a"], more: 0, mono: false });
    const relation = field("r", "relation", { relation: { cardinality: "multiple" } as CollectionField["relation"] });
    expect(cellView(relation, ["1", "2", "3", "4", "5"])).toEqual({ kind: "chips", items: ["1", "2", "3"], more: 2, mono: true });
    expect(cellView(field("j", "jsonb"), { a: [1, 2] })).toEqual({ kind: "json", text: '{"a":[1,2]}' });
    expect(cellView(field("f", "file"), undefined)).toEqual({ kind: "file" });
  });

  it("formats dates without converting time zones", () => {
    expect(shortDate("2026-01-02T03:04:05Z")).toBe("2026-01-02 03:04:05Z");
    expect(shortDate("not a date")).toBe("not a date");
  });
});

describe("record form drafts", () => {
  const fields = [
    field("big", "integer"),
    field("wide", "bigint"),
    field("price", "numeric"),
    field("title", "text", { required: true }),
    field("meta", "jsonb"),
    field("when", "timestamptz"),
    field("secret", "password"),
    field("created", "autodate"),
  ];
  const record = parseJson(
    '{"id":"r1","big":9007199254740993,"wide":"9223372036854775807","price":"1.50","title":"Hello","meta":{"n":12345678901234567890},"when":"2026-11-12T10:30:05.123456Z","created":"2026-01-01T00:00:00Z"}',
  ) as RecordData;

  it("round-trips exact numbers through an edit", () => {
    const initial = initialDrafts(fields, record);
    expect(initial.big).toBe("9007199254740993");
    expect(initial.price).toBe("1.50");
    const drafts = { ...initial, big: "9007199254740995", wide: "-9223372036854775808", price: "1.500" };
    const { input, errors } = buildInput(fields, drafts, initial, false);
    expect(errors).toEqual({});
    expect(stringifyJson(input)).toBe('{"big":9007199254740995,"wide":"-9223372036854775808","price":"1.500"}');
  });

  it("sends only changed fields on update and nothing for untouched JSON", () => {
    const initial = initialDrafts(fields, record);
    expect(isDirty(fields, initial, initial)).toBe(false);
    const drafts = { ...initial, title: "Hi" };
    expect(isDirty(fields, drafts, initial)).toBe(true);
    expect(buildInput(fields, drafts, initial, false).input).toEqual({ title: "Hi" });
  });

  it("keeps exact numbers inside edited JSON", () => {
    const initial = initialDrafts(fields, record);
    const drafts = { ...initial, meta: '{"n": 12345678901234567890, "x": 1}' };
    expect(stringifyJson(buildInput(fields, drafts, initial, false).input)).toBe('{"meta":{"n":12345678901234567890,"x":1}}');
  });

  it("reports invalid drafts per field instead of sending them", () => {
    const initial = initialDrafts(fields, record);
    const drafts = { ...initial, big: "1.5", meta: "{nope", when: "yesterday" };
    expect(buildInput(fields, drafts, initial, false).errors).toEqual({ big: "invalid_number", meta: "invalid_json", when: "invalid_date" });
  });

  it("edits timestamps as UTC wall time and skips empty creates and passwords", () => {
    const initial = initialDrafts(fields, record);
    expect(initial.when).toBe("2026-11-12T10:30:05.123");
    expect(initial.secret).toBe("");
    expect("created" in initial).toBe(false);
    const blank = initialDrafts(fields);
    const { input } = buildInput(fields, { ...blank, title: "New", when: "2026-11-12T10:30" }, blank, true);
    expect(input).toEqual({ title: "New", when: "2026-11-12T10:30:00Z" });
  });
});
