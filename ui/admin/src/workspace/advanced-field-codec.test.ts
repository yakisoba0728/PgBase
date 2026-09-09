import { describe, expect, it } from "vitest";
import type { FieldDefinition, FieldType } from "../backend/ports";
import { stringifyJSON } from "../backend/json";
import { acceptedDraft, blankState, encodeDraft, initialDraft, type Draft } from "./record-codec";

const field = (name: string, type: FieldType, extra: Partial<FieldDefinition> = {}): FieldDefinition => ({ name, type, ordinal: 0, required: false, indexed: false, unique: false, ...extra });
const encode = (f: FieldDefinition, value: string) => encodeDraft([f], { [f.name]: { kind: "value", value } }, new Set([f.name]), true);

describe("advanced field input contracts", () => {
  it("keeps HTML as text and measures its UTF-8 byte limit", () => {
    const f = field("body", "editor", { options: { maxSize: 4 } });
    expect(encode(f, "éé")).toEqual({ input: { body: "éé" }, errors: {} });
    expect(encode(f, "ééa").errors.body).toContain("4 UTF-8 bytes");
    expect(encode(field("body", "editor"), "<p>Hello &amp; goodbye</p>").input.body).toBe("<p>Hello &amp; goodbye</p>");
    expect(encode(field("body", "editor", { required: true }), "").errors.body).toContain("required");
  });
  it("accepts exactly two numeric coordinates, including precision-preserving input", () => {
    const f = field("place", "geoPoint");
    expect(encode(f, '{"lon":180,"lat":-90}').input.place).toEqual({ lon: 180, lat: -90 });
    const precise = encode(f, '{"lon":127.1234567890123456789,"lat":37.5}');
    expect(precise.errors).toEqual({});
    expect(stringifyJSON(precise.input)).toContain("127.1234567890123456789");
    for (const raw of ['{"lon":181,"lat":0}', '{"lon":0,"lat":91}', '{"lon":null,"lat":0}', '{"lon":"1","lat":0}', '{"lon":1}', '{"lon":1,"lat":2,"alt":3}', '{"lon":1,"lon":2,"lat":0}']) expect(encode(f, raw).errors.place).toBeDefined();
    expect(encode(field("place", "geoPoint", { required: true }), '{"lon":0,"lat":0}').errors.place).toBeDefined();
    expect(blankState(f, "value")).toEqual({ kind: "value", value: '{"lon":0,"lat":0}' });
  });
  it("never submits automatic timestamps, including restored or tampered drafts", () => {
    const f = field("modified", "autodate");
    expect(encode(f, "2000-01-01T00:00:00Z")).toEqual({ input: {}, errors: {} });
    expect(initialDraft([f], { modified: "2026-09-09T00:00:00Z" }, false).modified).toEqual({ kind: "value", value: "2026-09-09T00:00:00Z" });
  });
  it("keeps passwords unset on edit, allows explicit clearing and counts Unicode characters", () => {
    const f = field("pin", "password");
    expect(initialDraft([f], { pin: "server-must-not-send-this" }, false)).toEqual({ pin: { kind: "unset" } });
    expect(encode(f, "").input).toEqual({ pin: "" });
    expect(encode(f, "😀".repeat(71)).errors).toEqual({});
    expect(encode(f, "😀".repeat(72)).errors.pin).toBeDefined();
    expect(encode(field("pin", "password", { required: true }), "").errors.pin).toBeDefined();
  });
  it("clears only accepted password input when editing continues during save", () => {
    const fields = [field("pin", "password"), field("body", "editor")];
    const submitted: Draft = { pin: { kind: "value", value: "submitted" }, body: { kind: "value", value: "first" } };
    const next = acceptedDraft(fields, submitted, { ...submitted, body: { kind: "value", value: "second" } });
    expect(next.initial.pin).toEqual({ kind: "unset" });
    expect(next.draft).toEqual({ pin: { kind: "unset" }, body: { kind: "value", value: "second" } });
    expect(acceptedDraft(fields, submitted, { ...submitted, pin: { kind: "value", value: "newer" } }).draft.pin).toEqual({ kind: "value", value: "newer" });
  });
});
