import type { Collection, CollectionField } from "../api/collections";
import type { RecordData } from "../api/records";

const textLike = (field: CollectionField) => (field.type === "text" || field.type === "editor") && !field.hidden;
const byOrdinal = (fields: CollectionField[]) => [...fields].sort((a, b) => a.ordinal - b.ordinal);

/** The field that names a record of `collection` in pickers and chips: the first presentable
 * text field, else the first indexed/unique text field, else the first text field; none means the id. */
export function labelField(collection: Collection): CollectionField | undefined {
  const text = byOrdinal(collection.fields).filter(textLike);
  return text.find((field) => field.presentable) ?? text.find((field) => field.indexed || field.unique) ?? text.find((field) => field.type === "text");
}

/** A record's readable name: its label field when that is a non-empty string, else its id. */
export function recordLabel(record: RecordData | undefined, field: CollectionField | undefined, id: string): string {
  const value = field ? record?.[field.name] : undefined;
  if (typeof value !== "string" || value.trim() === "") return id;
  const text = field?.type === "editor" ? htmlText(value) : value;
  return text.length > 80 ? `${text.slice(0, 80)}…` : text;
}

// The server expands at most 20 nodes per request; leave room and fall back to ids past this.
const maxExpand = 10;

/** The `expand` parameter that labels relation values: the first ten relation fields, optionally only those named. */
export function expandFor(collection: Collection, only?: string[]): string {
  return collection.fields
    .filter((field) => field.type === "relation" && (!only || only.includes(field.name)))
    .slice(0, maxExpand)
    .map((field) => field.name)
    .join(",");
}

const quote = (value: string) => `'${value.replaceAll("'", "''")}'`;
const uuidPattern = /^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/i;

/** Filter for the record picker's search box: a case-insensitive "contains" on the label field,
 * or-ed with an exact id match when the text could be an id of this collection. null: nothing can match
 * (a uuid collection without a text field and a query that is not a uuid). */
export function relationSearchFilter(target: Collection, query: string): string | null {
  const text = query.trim();
  if (text === "") return "";
  const field = labelField(target);
  const idMatch = target.recordId?.kind === "text" || uuidPattern.test(text) ? `id = ${quote(text)}` : "";
  const contains = field ? `${field.name}:lower ~ ${quote(text.toLowerCase())}` : "";
  return [contains, idMatch].filter(Boolean).join(" || ") || null;
}

/** One filter that loads the given ids (picker chips need labels for ids the list did not expand). */
export function idsFilter(ids: string[]): string {
  return ids.map((id) => `id = ${quote(id)}`).join(" || ");
}

/** Plain text of an HTML string for one-line summaries; tags dropped, common entities decoded. */
export function htmlText(html: string, limit = 400): string {
  return html
    .slice(0, limit * 8)
    .replace(/<(script|style)[\s\S]*?<\/\1>/gi, " ")
    // Block boundaries become spaces; inline tags (<em>, <b>…) vanish so words stay whole.
    .replace(/<(br|hr|\/(p|div|li|h[1-6]|tr|td|th|blockquote|pre|table))\b[^>]*>/gi, " ")
    .replace(/<[^>]*>/g, "")
    .replace(/&nbsp;/g, " ")
    .replace(/&lt;/g, "<")
    .replace(/&gt;/g, ">")
    .replace(/&quot;/g, '"')
    .replace(/&#39;/g, "'")
    .replace(/&amp;/g, "&")
    .replace(/\s+/g, " ")
    .trim()
    .slice(0, limit);
}
