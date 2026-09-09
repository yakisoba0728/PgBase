import { isLosslessNumber } from "lossless-json";
import { parseJSON, stringifyJSON } from "../backend/json";
import type { FieldDefinition, RecordData, RecordValue } from "../backend/ports";

export type FieldState = { kind: "unset" } | { kind: "null" } | { kind: "value"; value: string | boolean };
export type Draft = Record<string, FieldState>;

const canonicalUUID = /^[0-9a-f]{8}(?:-[0-9a-f]{4}){3}-[0-9a-f]{12}$/i;
const nilUUID = "00000000-0000-0000-0000-000000000000";

type RelationCode = "relation_invalid_value" | "duplicate_relation_value" | "relation_min_select" | "relation_max_select";

class RelationValueError extends Error {
  constructor(readonly code: RelationCode) {
    super(code);
  }
}

function relationOf(field: FieldDefinition) {
  if (field.type !== "relation" || field.relation === undefined) throw new RelationValueError("relation_invalid_value");
  return field.relation;
}

/** The canonical, non-zero form of a relation id, or null when the text is not
 * one. A relation column is a foreign key, so the all-zero UUID names no row. */
export function canonicalRelationID(value: string): string | null {
  const normalized = value.trim().toLowerCase();
  return canonicalUUID.test(normalized) && normalized !== nilUUID ? normalized : null;
}

function canonical(value: string): string {
  const id = canonicalRelationID(value);
  if (id === null) throw new RelationValueError("relation_invalid_value");
  return id;
}

/** The ordered, non-blank ids a multiple-relation draft holds. */
export function relationDraftMembers(draft: string): string[] {
  return draft.split(/\r?\n/).map((member) => member.trim()).filter((member) => member !== "");
}

function relationDraft(field: FieldDefinition, value: RecordValue | undefined): string {
  const relation = relationOf(field);
  if (value === undefined || value === null) return "";
  if (relation.cardinality === "single") {
    if (typeof value !== "string") return String(value);
    return canonicalRelationID(value) ?? value;
  }
  if (!Array.isArray(value)) return String(value);
  return value.map((member) => {
    if (typeof member !== "string") return String(member);
    return canonicalRelationID(member) ?? member;
  }).join("\n");
}

function encodeRelation(field: FieldDefinition, draft: string): RecordValue {
  const relation = relationOf(field);
  if (relation.cardinality === "single") return canonical(draft);
  const entries = relationDraftMembers(draft);
  if (entries.length < relation.minSelect) throw new RelationValueError("relation_min_select");
  if (entries.length > relation.maxSelect) throw new RelationValueError("relation_max_select");
  const ids = entries.map(canonical);
  if (new Set(ids).size !== ids.length) throw new RelationValueError("duplicate_relation_value");
  return ids;
}

function draftValue(field: FieldDefinition, value: RecordValue | undefined): string | boolean {
  if (field.type === "boolean") return value === true;
  if (field.type === "jsonb" || field.type === "geoPoint") return value === undefined || value === null ? "" : stringifyJSON(value, 2);
  if (field.type === "relation") return relationDraft(field, value);
  return value === undefined || value === null ? "" : String(value);
}

function encodeValue(field: FieldDefinition, value: string | boolean): RecordValue {
  if (field.type === "boolean") return value === true;
  const text = String(value);
  if (field.type === "relation") return encodeRelation(field, text);
  if (field.type === "editor") {
    if (new TextEncoder().encode(text).length > (field.options?.maxSize || 5 * 1024 * 1024)) throw new Error("size");
    if (field.required && text === "") throw new Error("required");
    return text;
  }
  if (field.type === "password") {
    const length = Array.from(text).length;
    if (text === "" && !field.required) return "";
    if (length < Math.max(field.required ? 1 : 0, field.options?.minLength ?? 0) || length > (field.options?.maxLength ?? 71)) throw new Error("length");
    return text;
  }
  if (field.type === "geoPoint") {
    const point = parseJSON(text);
    if (point === null || typeof point !== "object" || Array.isArray(point)) throw new Error("point");
    const coordinates = point as Record<string, unknown>;
    if (Object.keys(coordinates).length !== 2 || (typeof coordinates.lon !== "number" && !isLosslessNumber(coordinates.lon)) || (typeof coordinates.lat !== "number" && !isLosslessNumber(coordinates.lat))) throw new Error("point");
    const lon = Number(String(coordinates.lon));
    const lat = Number(String(coordinates.lat));
    if (!Number.isFinite(lon) || !Number.isFinite(lat) || Math.abs(lon) > 180 || Math.abs(lat) > 90 || (field.required && lon === 0 && lat === 0)) throw new Error("point");
    return point as RecordValue;
  }
  if (field.type === "integer") {
    if (!/^-?\d+$/.test(text)) throw new Error("type");
    const integer = BigInt(text);
    if (integer < -2147483648n || integer > 2147483647n) throw new Error("type");
    return Number(integer);
  }
  if (field.type === "bigint" || field.type === "numeric" || field.type === "text") return text;
  if (field.type === "timestamptz") {
    const parsed = new Date(text);
    if (Number.isNaN(parsed.valueOf())) throw new Error("type");
    return parsed.toISOString();
  }
  if (field.type === "uuid") {
    if (!canonicalUUID.test(text)) throw new Error("type");
    return text.toLowerCase();
  }
  return parseJSON(text) as RecordValue;
}

const fieldErrorText: Record<string, string> = {
  required: "This field is required.",
  type_mismatch: "Value does not match the field type.",
  unknown_field: "This field is not recognized.",
  value_too_long: "Value exceeds the 2,048-byte limit for text values.",
};

function selectionLabel(count: number): string {
  return `${count} related record${count === 1 ? "" : "s"}`;
}

function relationErrorText(field: FieldDefinition, code: string): string {
  const relation = field.relation;
  if (code === "duplicate_relation_value") return "Related record IDs must be unique.";
  if (code === "relation_target_invalid") return "One or more related records do not exist or are unavailable.";
  if (code === "relation_min_select" && relation !== undefined) return `Select at least ${selectionLabel(relation.minSelect)}.`;
  if (code === "relation_max_select" && relation !== undefined) return `Select no more than ${selectionLabel(relation.maxSelect)}.`;
  if (code === "required") return "This relation is required.";
  if (code === "relation_invalid_value") {
    return relation?.cardinality === "multiple"
      ? "Enter one canonical, non-zero UUID per line."
      : "Enter a canonical, non-zero UUID.";
  }
  return "Relation value is invalid.";
}

// The server bounds indexed and unique text at 2,048 bytes so a btree entry
// always fits, and every other text value at the 1 MiB body limit restated in
// the codec. Naming the wrong bound sends people trimming a value that was
// never near it, so the limit follows the field the same way maxLength does.
export function validationText(field: FieldDefinition, code: string): string {
  if (field.type === "relation") return relationErrorText(field, code);
  if (field.type === "autodate") return "This timestamp is set by the server.";
  if (field.type === "geoPoint") return "Enter longitude from −180 to 180 and latitude from −90 to 90. Required points cannot be (0, 0).";
  if (field.type === "password") return `Enter ${field.options?.minLength ?? (field.required ? 1 : 0)}–${field.options?.maxLength ?? 71} characters. Stored passwords are never returned.`;
  if (field.type === "editor" && code === "value_too_long") return `HTML must be at most ${field.options?.maxSize || 5 * 1024 * 1024} UTF-8 bytes.`;
  if (code === "value_too_long") {
    return field.type === "text" && !field.indexed && !field.unique
      ? "Value exceeds the 1 MiB limit for text values."
      : fieldErrorText.value_too_long;
  }
  return fieldErrorText[code] ?? "Value is invalid.";
}

// A multiple relation has no NULL: an empty ordered array clears it, so its
// draft starts as a value even when the record has none.
export function initialDraft(fields: FieldDefinition[], record: RecordData, isCreate: boolean): Draft {
  const draft: Draft = {};
  for (const field of fields) {
    const value = record[field.name];
    if (isCreate || field.type === "password") draft[field.name] = { kind: "unset" };
    else if (field.type === "relation" && field.relation?.cardinality === "multiple") draft[field.name] = { kind: "value", value: draftValue(field, value) };
    else if (value === null || value === undefined) draft[field.name] = { kind: "null" };
    else draft[field.name] = { kind: "value", value: draftValue(field, value) };
  }
  return draft;
}

export function blankState(field: FieldDefinition, kind: FieldState["kind"]): FieldState {
  return kind === "value" ? { kind, value: field.type === "boolean" ? false : field.type === "geoPoint" ? '{"lon":0,"lat":0}' : "" } : { kind };
}

export function dirtyNames(draft: Draft, initial: Draft): Set<string> {
  return new Set(Object.keys(draft).filter((name) => {
    const left = draft[name];
    const right = initial[name];
    return !(left?.kind === right?.kind && (left?.kind !== "value" || right?.kind !== "value" || left.value === right.value));
  }));
}

/** Turns the tri-state draft into the exact body to send: an unset field is
 * omitted so the database default applies, and an unchanged field never
 * reaches a PATCH. */
export function encodeDraft(fields: FieldDefinition[], draft: Draft, dirty: Set<string>, isCreate: boolean): { input: RecordData; errors: Record<string, string> } {
  const errors: Record<string, string> = {};
  const input: RecordData = {};
  for (const field of fields) {
    if (field.type === "autodate" || field.type === "file") continue;
    if (!isCreate && !dirty.has(field.name)) continue;
    const state = draft[field.name] ?? { kind: "unset" };
    if (state.kind === "unset") {
      if (isCreate && field.type === "relation" && (field.relation?.minSelect ?? 0) > 0) errors[field.name] = validationText(field, "relation_min_select");
      continue;
    }
    if (state.kind === "null") {
      if (["password", "geoPoint", "editor"].includes(field.type)) errors[field.name] = "Use a value or omit this field; NULL is not supported.";
      else if (field.type === "relation" && field.relation?.cardinality === "multiple") errors[field.name] = validationText(field, "relation_invalid_value");
      else if (field.type === "relation" && (field.relation?.minSelect ?? 0) > 0) errors[field.name] = validationText(field, "relation_min_select");
      else input[field.name] = null;
      continue;
    }
    try { input[field.name] = encodeValue(field, state.value); }
    catch (caught: unknown) {
      if (field.type === "relation" && caught instanceof RelationValueError) errors[field.name] = validationText(field, caught.code);
      else if (["geoPoint", "password"].includes(field.type)) errors[field.name] = validationText(field, "type_mismatch");
      else if (field.type === "editor") errors[field.name] = validationText(field, caught instanceof Error && caught.message === "size" ? "value_too_long" : "required");
      else errors[field.name] = field.type === "jsonb" ? "Enter valid JSON." : "Value does not match the field type.";
    }
  }
  return { input, errors };
}

/** Applies a picker selection while preserving ordered-unique multi semantics. */
export function selectRelationTarget(field: FieldDefinition, draft: string, selectedID: string): { draft: string; status: "selected" | "duplicate" | "max" } {
  const relation = relationOf(field);
  const id = canonical(selectedID);
  if (relation.cardinality === "single") return { draft: id, status: "selected" };
  const entries = relationDraftMembers(draft);
  const duplicate = entries.some((member) => canonicalRelationID(member) === id);
  if (duplicate) return { draft: entries.join("\n"), status: "duplicate" };
  if (entries.length >= relation.maxSelect) return { draft: entries.join("\n"), status: "max" };
  return { draft: [...entries, id].join("\n"), status: "selected" };
}

/** Forget accepted password input while preserving edits made during a save. */
export function acceptedDraft(fields: FieldDefinition[], submitted: Draft, current: Draft): { initial: Draft; draft: Draft } {
  const initial = { ...submitted };
  const draft = { ...current };
  const changed = dirtyNames(current, submitted);
  for (const field of fields) {
    if (field.type !== "password") continue;
    initial[field.name] = { kind: "unset" };
    if (!changed.has(field.name)) draft[field.name] = { kind: "unset" };
  }
  return { initial, draft };
}
