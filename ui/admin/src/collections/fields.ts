import { LosslessNumber, parse, stringify } from "lossless-json";
import type { CollectionField } from "../api/collections";
import { parseJson } from "../api/http";
import type { RecordData } from "../api/records";
import { htmlText } from "./relation";

// How each catalog field type is shown in the grid and edited in the form.
// Wire contract (internal/query/codec.go): integer is a JSON number, bigint and
// numeric are JSON strings, timestamps are RFC 3339 UTC, file fields have no column.

type FieldKind = "bool" | "number" | "text" | "email" | "url" | "editor" | "select" | "multiselect" | "json" | "geo" | "date" | "relation" | "relations" | "file" | "password" | "autodate";

export function fieldKind(field: CollectionField): FieldKind {
  switch (field.type) {
    case "boolean":
      return "bool";
    case "integer":
    case "bigint":
    case "numeric":
      return "number";
    case "text":
      if (field.options?.allowedValues?.length) return "select";
      return field.options?.format ?? "text";
    case "jsonb":
      return field.options?.allowedValues?.length ? "multiselect" : "json";
    case "geoPoint":
      return "geo";
    case "timestamptz":
      return "date";
    case "relation":
      return field.relation?.cardinality === "multiple" ? "relations" : "relation";
    case "editor":
    case "file":
    case "password":
    case "autodate":
      return field.type;
    case "uuid":
      return "text";
  }
}

const isEmpty = (value: unknown) => value === undefined || value === null || value === "" || (Array.isArray(value) && value.length === 0);

/** "2026-11-12T10:30:00.123456Z" -> "2026-11-12 10:30:00Z": exact to the second, no time zone conversion. */
export function shortDate(value: string): string {
  const match = /^(\d{4}-\d{2}-\d{2})T(\d{2}:\d{2}:\d{2})/.exec(value);
  return match ? `${match[1]} ${match[2]}Z` : value;
}

type CellView =
  | { kind: "empty" }
  | { kind: "text" | "number" | "json"; text: string }
  | { kind: "bool"; value: boolean }
  | { kind: "date"; text: string; title: string }
  | { kind: "chips"; items: string[]; more: number; mono: boolean }
  | { kind: "relation"; ids: string[] }
  | { kind: "file" };

/** What a grid cell shows; numbers keep the exact text the server sent. */
export function cellView(field: CollectionField, value: unknown): CellView {
  const kind = fieldKind(field);
  if (kind === "file") return { kind: "file" };
  if (isEmpty(value)) return { kind: "empty" };
  switch (kind) {
    case "bool":
      return { kind: "bool", value: value === true };
    case "number":
      return { kind: "number", text: String(value) };
    case "date":
    case "autodate":
      return typeof value === "string" ? { kind: "date", text: shortDate(value), title: value } : { kind: "text", text: String(value) };
    case "select":
      return { kind: "chips", items: [String(value)], more: 0, mono: false };
    case "relation":
    case "relations":
      return { kind: "relation", ids: Array.isArray(value) ? value.map(String) : [String(value)] };
    case "multiselect": {
      const items = Array.isArray(value) ? value.map(String) : [String(value)];
      return { kind: "chips", items: items.slice(0, 3), more: Math.max(0, items.length - 3), mono: false };
    }
    case "editor":
      return typeof value === "string" ? { kind: "text", text: htmlText(value, 300) } : { kind: "text", text: String(value) };
    case "geo": {
      const point = value as { lon?: unknown; lat?: unknown };
      return { kind: "number", text: `${String(point.lon)}, ${String(point.lat)}` };
    }
    case "json":
      return { kind: "json", text: jsonPreview(value) };
    default: {
      // A cell shows one line; the full value is one click away (ValuePeek / the detail pane).
      const text = typeof value === "string" ? value : String(stringify(value));
      return { kind: "text", text: text.length > 300 ? `${text.slice(0, 300)}…` : text };
    }
  }
}

/** Inspects only a few shallow entries and a short prefix of each string. */
function jsonPreview(value: unknown): string {
  const limit = 120;
  const entries = 4;
  const quote = (text: string) => text.length > limit ? `${JSON.stringify(text.slice(0, limit)).slice(0, limit)}…` : JSON.stringify(text);
  const visit = (item: unknown, depth: number): string => {
    if (typeof item === "string") return quote(item);
    if (Array.isArray(item)) {
      if (!item.length) return "[]";
      if (depth === 0) return "[…]";
      return `[${item.slice(0, entries).map((child) => visit(child, depth - 1)).join(",")}${item.length > entries ? ",…" : ""}]`;
    }
    if (item && typeof item === "object" && !(item instanceof LosslessNumber)) {
      if (depth === 0) return "{…}";
      const values: string[] = [];
      for (const key in item) {
        if (!Object.hasOwn(item, key)) continue;
        if (values.length === entries) {
          values.push("…");
          break;
        }
        values.push(`${quote(key)}:${visit((item as Record<string, unknown>)[key], depth - 1)}`);
      }
      return `{${values.join(",")}}`;
    }
    return stringify(item) ?? "";
  };
  const text = visit(value, 2);
  return text.length > limit ? `${text.slice(0, limit)}…` : text;
}

// ---------------------------------------------------------------------------
// Form drafts: every value is edited as text (or bool / string list) and only
// turned back into JSON on save, so numbers never pass through a JS double.
// ---------------------------------------------------------------------------

export type Draft = string | boolean | string[];

const datePattern = /^(\d{4}-\d{2}-\d{2}T\d{2}:\d{2}(?::\d{2})?)(\.\d{1,3})?\d*Z$/;

function toDraft(field: CollectionField, value: unknown): Draft {
  const kind = fieldKind(field);
  switch (kind) {
    case "bool":
      return value === true;
    case "multiselect":
      return Array.isArray(value) ? value.map(String) : [];
    case "password":
      return "";
    case "json":
      return value === undefined || value === null ? "" : (stringify(value, null, 2) ?? "");
    case "relations":
      return Array.isArray(value) ? value.map(String) : [];
    case "geo": {
      const point = (value ?? {}) as { lon?: unknown; lat?: unknown };
      return [point.lon === undefined ? "" : String(point.lon), point.lat === undefined ? "" : String(point.lat)];
    }
    case "date": {
      // datetime-local has no zone; the form edits the UTC wall time and shows "UTC".
      const match = typeof value === "string" ? datePattern.exec(value) : null;
      return match ? match[1] + (match[2] ?? "") : "";
    }
    default:
      return value === undefined || value === null ? "" : String(value);
  }
}

type Parsed = { ok: true; value: unknown } | { ok: false; error: "invalid_number" | "invalid_json" | "invalid_date" | "invalid_point" };

// JSON integer syntax: no plus sign, no leading zeros.
const integerPattern = /^-?(0|[1-9]\d*)$/;
const decimalPattern = /^-?(0|[1-9]\d*)(\.\d+)?$/;

function fromDraft(field: CollectionField, draft: Draft): Parsed {
  const kind = fieldKind(field);
  if (kind === "geo" && Array.isArray(draft)) return pointFromDraft(draft);
  if (typeof draft === "boolean" || Array.isArray(draft)) return { ok: true, value: draft };
  const text = kind === "text" || kind === "editor" || kind === "password" ? draft : draft.trim();
  switch (kind) {
    case "number":
      if (text === "") return { ok: true, value: null };
      if (field.type === "numeric") return decimalPattern.test(text) ? { ok: true, value: text } : { ok: false, error: "invalid_number" };
      if (!integerPattern.test(text)) return { ok: false, error: "invalid_number" };
      // bigint travels as a string; integer as a JSON number written verbatim.
      return { ok: true, value: field.type === "bigint" ? text : new LosslessNumber(text) };
    case "json":
      if (text === "") return { ok: true, value: null };
      try {
        return { ok: true, value: parseJson(text) };
      } catch {
        return { ok: false, error: "invalid_json" };
      }
    case "date":
      if (text === "") return { ok: true, value: null };
      if (!/^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}(:\d{2}(\.\d+)?)?$/.test(text)) return { ok: false, error: "invalid_date" };
      return { ok: true, value: `${text.length === 16 ? `${text}:00` : text}Z` };
    case "editor":
      // Editor columns are non-null text: empty is "", never null.
      return { ok: true, value: text };
    case "text":
    case "email":
    case "url":
      return { ok: true, value: text === "" && !field.required ? null : text };
    default:
      return { ok: true, value: text === "" ? null : text };
  }
}

// Exact decimal text, so a coordinate is sent with the digits that were typed.
const coordinatePattern = /^-?\d+(\.\d+)?$/;

function pointFromDraft([lon = "", lat = ""]: string[]): Parsed {
  const [x, y] = [lon.trim(), lat.trim()];
  if (x === "" && y === "") return { ok: true, value: { lon: 0, lat: 0 } };
  if (!coordinatePattern.test(x) || !coordinatePattern.test(y) || Math.abs(Number(x)) > 180 || Math.abs(Number(y)) > 90) return { ok: false, error: "invalid_point" };
  return { ok: true, value: { lon: new LosslessNumber(x), lat: new LosslessNumber(y) } };
}

/** Fields the record form edits as values; files have their own uploader and autodates are server-set. */
const editableFields = (fields: CollectionField[]) => fields.filter((field) => field.type !== "file" && field.type !== "autodate");

const sameDraft = (a: Draft, b: Draft) => (Array.isArray(a) && Array.isArray(b) ? a.length === b.length && a.every((item, index) => item === b[index]) : a === b);

const emptyDraft = (draft: Draft) => draft === "" || draft === false || (Array.isArray(draft) && draft.every((item) => item === ""));

export function initialDrafts(fields: CollectionField[], record?: RecordData): Record<string, Draft> {
  return Object.fromEntries(editableFields(fields).map((field) => {
    const value = record ? record[field.name] : field.type === "boolean" ? field.default : undefined;
    return [field.name, toDraft(field, value)];
  }));
}

export function isDirty(fields: CollectionField[], drafts: Record<string, Draft>, initial: Record<string, Draft>): boolean {
  return editableFields(fields).some((field) => !sameDraft(drafts[field.name], initial[field.name]));
}

/** The request body: on create every non-empty draft, on update only changed ones (a PATCH). */
export function buildInput(
  fields: CollectionField[],
  drafts: Record<string, Draft>,
  initial: Record<string, Draft>,
  isNew: boolean,
): { input: Record<string, unknown>; errors: Record<string, string> } {
  const input: Record<string, unknown> = {};
  const errors: Record<string, string> = {};
  for (const field of editableFields(fields)) {
    const draft = drafts[field.name];
    const explicitFalse = field.type === "boolean" && (field.required || typeof field.default === "boolean") && draft === false;
    if (isNew && emptyDraft(draft) && !explicitFalse && missingRequired(field)) errors[field.name] = "required";
    if (isNew ? emptyDraft(draft) && !explicitFalse : sameDraft(draft, initial[field.name])) continue;
    if (field.type === "password" && draft === "") continue;
    const parsed = fromDraft(field, draft);
    if (!parsed.ok) errors[field.name] = parsed.error;
    else {
      const code = valueError(field, parsed.value);
      if (code) errors[field.name] = code;
      else input[field.name] = parsed.value;
    }
  }
  return { input, errors };
}

// Checks run before the request, limited to what the server rejects in every validation mode; the rest
// (lengths, patterns, domains, uniqueness) stays with the server, whose codes share these messages.

/** A required field left out of a create, which the server fills only from a default or an autogenerate pattern
 *  (system fields, such as an auth collection's, may be filled by the server and are left to it). */
const missingRequired = (field: CollectionField) =>
  field.required && !field.system && field.type !== "boolean" && (field.default === undefined || field.default === null) && !field.options?.autogeneratePattern;

/** "required", "email" or "url" for a value about to be sent; null when it may go. */
function valueError(field: CollectionField, value: unknown): "required" | "email" | "url" | null {
  const kind = fieldKind(field);
  // JSON null may satisfy a required jsonb column, and a required plain text field may be "" in postgres mode.
  if (field.required && ((value === null && kind !== "json") || (value === "" && (kind === "editor" || kind === "email" || kind === "url")))) return "required";
  if (typeof value !== "string" || value === "") return null;
  if (kind === "email" && !validEmail(value)) return "email";
  if (kind === "url" && !validUrl(value)) return "url";
  return null;
}

/** One "@" with something on both sides and no spaces; the server makes the full RFC 5322 check. */
export const validEmail = (value: string) => /^[^\s@]+@[^\s@]+$/.test(value);

/** An absolute http(s) URL with a host. */
export function validUrl(value: string): boolean {
  try {
    const url = new URL(value);
    return (url.protocol === "http:" || url.protocol === "https:") && url.hostname !== "";
  } catch {
    return false;
  }
}

/** Live check for the JSON textarea. */
export function jsonError(text: string): boolean {
  if (text.trim() === "") return false;
  try {
    parse(text);
    return false;
  } catch {
    return true;
  }
}
