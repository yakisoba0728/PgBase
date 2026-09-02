import { parseJSON, stringifyJSON } from "../backend/json";
import type { CollectionDefinition, FieldDefinition, RecordData, RecordValue } from "../backend/ports";

export type FieldState = { kind: "unset" } | { kind: "null" } | { kind: "value"; value: string | boolean };
export type Draft = Record<string, FieldState>;

export interface RelationTargetOption { id: string; label: string }
export type RelationTargetSearch =
  | { filter: string; sort?: string; displayField?: FieldDefinition }
  | { problem: "invalid-uuid" | "too-long" };

const canonicalUUID = /^[0-9a-f]{8}(?:-[0-9a-f]{4}){3}-[0-9a-f]{12}$/i;
const nilUUID = "00000000-0000-0000-0000-000000000000";
const filterFieldPattern = /^[a-z_][a-z0-9_]*$/;
const maximumSearchBytes = 1024;

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

function canonical(value: string): string {
  const normalized = value.trim().toLowerCase();
  if (!canonicalUUID.test(normalized) || normalized === nilUUID) throw new RelationValueError("relation_invalid_value");
  return normalized;
}

function members(draft: string): string[] {
  return draft.split(/\r?\n/).map((member) => member.trim()).filter((member) => member !== "");
}

function relationDraft(field: FieldDefinition, value: RecordValue | undefined): string {
  const relation = relationOf(field);
  if (value === undefined || value === null) return "";
  if (relation.cardinality === "single") {
    if (typeof value !== "string") return String(value);
    try { return canonical(value); } catch { return value; }
  }
  if (!Array.isArray(value)) return String(value);
  return value.map((member) => {
    if (typeof member !== "string") return String(member);
    try { return canonical(member); } catch { return member; }
  }).join("\n");
}

function encodeRelation(field: FieldDefinition, draft: string): RecordValue {
  const relation = relationOf(field);
  if (relation.cardinality === "single") return canonical(draft);
  const entries = members(draft);
  if (entries.length < relation.minSelect) throw new RelationValueError("relation_min_select");
  if (entries.length > relation.maxSelect) throw new RelationValueError("relation_max_select");
  const ids = entries.map(canonical);
  if (new Set(ids).size !== ids.length) throw new RelationValueError("duplicate_relation_value");
  return ids;
}

function draftValue(field: FieldDefinition, value: RecordValue | undefined): string | boolean {
  if (field.type === "boolean") return value === true;
  if (field.type === "jsonb") return value === undefined || value === null ? "" : stringifyJSON(value, 2);
  if (field.type === "relation") return relationDraft(field, value);
  return value === undefined || value === null ? "" : String(value);
}

function encodeValue(field: FieldDefinition, value: string | boolean): RecordValue {
  if (field.type === "boolean") return value === true;
  const text = String(value);
  if (field.type === "relation") return encodeRelation(field, text);
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
    if (isCreate) draft[field.name] = { kind: "unset" };
    else if (field.type === "relation" && field.relation?.cardinality === "multiple") draft[field.name] = { kind: "value", value: draftValue(field, value) };
    else if (value === null || value === undefined) draft[field.name] = { kind: "null" };
    else draft[field.name] = { kind: "value", value: draftValue(field, value) };
  }
  return draft;
}

export function blankState(field: FieldDefinition, kind: FieldState["kind"]): FieldState {
  return kind === "value" ? { kind, value: field.type === "boolean" ? false : "" } : { kind };
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
    if (!isCreate && !dirty.has(field.name)) continue;
    const state = draft[field.name] ?? { kind: "unset" };
    if (state.kind === "unset") {
      if (isCreate && field.type === "relation" && (field.relation?.minSelect ?? 0) > 0) errors[field.name] = validationText(field, "relation_min_select");
      continue;
    }
    if (state.kind === "null") {
      if (field.type === "relation" && field.relation?.cardinality === "multiple") errors[field.name] = validationText(field, "relation_invalid_value");
      else if (field.type === "relation" && (field.relation?.minSelect ?? 0) > 0) errors[field.name] = validationText(field, "relation_min_select");
      else input[field.name] = null;
      continue;
    }
    try { input[field.name] = encodeValue(field, state.value); }
    catch (caught: unknown) {
      if (field.type === "relation" && caught instanceof RelationValueError) errors[field.name] = validationText(field, caught.code);
      else errors[field.name] = field.type === "jsonb" ? "Enter valid JSON." : "Value does not match the field type.";
    }
  }
  return { input, errors };
}

export function relationTargetSearchField(collection: CollectionDefinition): FieldDefinition | undefined {
  return collection.fields
    .slice()
    .sort((left, right) => left.ordinal - right.ordinal)
    .find((field) => field.type === "text" && (field.indexed || field.unique) && filterFieldPattern.test(field.name));
}

function quoted(value: string): string {
  return `'${value.replaceAll("'", "''")}'`;
}

/** Builds one exact, indexed lookup; the record port supplies the page bound. */
export function relationTargetSearch(collection: CollectionDefinition, query: string): RelationTargetSearch {
  const displayField = relationTargetSearchField(collection);
  if (displayField === undefined) {
    try { return { filter: `id = ${quoted(canonical(query))}` }; }
    catch { return { problem: "invalid-uuid" }; }
  }
  if (query.includes("\0") || new TextEncoder().encode(query).length > maximumSearchBytes) return { problem: "too-long" };
  return { displayField, filter: `${displayField.name} = ${quoted(query)}`, sort: displayField.name };
}

/** Ignores malformed server rows rather than putting an invalid id in a draft. */
export function relationTargetOption(record: RecordData, displayField?: FieldDefinition): RelationTargetOption | undefined {
  if (typeof record.id !== "string") return undefined;
  let id: string;
  try { id = canonical(record.id); } catch { return undefined; }
  const display = displayField === undefined ? undefined : record[displayField.name];
  return { id, label: typeof display === "string" && display !== "" ? display : id };
}

/** Applies a picker selection while preserving ordered-unique multi semantics. */
export function selectRelationTarget(field: FieldDefinition, draft: string, selectedID: string): { draft: string; status: "selected" | "duplicate" | "max" } {
  const relation = relationOf(field);
  const id = canonical(selectedID);
  if (relation.cardinality === "single") return { draft: id, status: "selected" };
  const entries = members(draft);
  const duplicate = entries.some((member) => {
    try { return canonical(member) === id; } catch { return false; }
  });
  if (duplicate) return { draft: entries.join("\n"), status: "duplicate" };
  if (entries.length >= relation.maxSelect) return { draft: entries.join("\n"), status: "max" };
  return { draft: [...entries, id].join("\n"), status: "selected" };
}
