import { parse, stringify } from "lossless-json";
import type { FieldDefinition } from "../backend/ports";
import type { Draft } from "./record-codec";

const namespace = "pgbase.admin.records.v1";

export interface RecordScope { administrator: string; collection: string; record?: string }

function key(kind: "columns" | "draft", scope: RecordScope): string {
  const server = typeof location === "undefined" ? "local" : location.origin;
  return [namespace, kind, server, scope.administrator, scope.collection, scope.record ?? "list"].map(encodeURIComponent).join(":");
}

export function readColumns(scope: RecordScope, available: readonly string[]): string[] {
  try {
    const value = JSON.parse(localStorage.getItem(key("columns", scope)) ?? "null");
    if (!Array.isArray(value)) return [...available];
    const selected = available.filter((name) => value.includes(name));
    return selected.includes("id") ? selected : ["id", ...selected];
  } catch { return [...available]; }
}

export function writeColumns(scope: RecordScope, columns: readonly string[]): void {
  try { localStorage.setItem(key("columns", scope), JSON.stringify(columns)); } catch { /* Storage is optional; keep the current in-memory selection. */ }
}

export function readDraft(scope: RecordScope, fields?: readonly FieldDefinition[]): Draft | null {
  try {
    const value = parse(localStorage.getItem(key("draft", scope)) ?? "null");
    if (value === null || typeof value !== "object" || Array.isArray(value)) return null;
    const safe = persistentDraft(value as Draft, fields);
    if (fields) writeDraft(scope, safe, fields);
    return safe;
  } catch { return null; }
}

function persistentDraft(draft: Draft, fields?: readonly FieldDefinition[]): Draft {
  const allowed = fields && new Set(fields.filter(field => !["password", "autodate", "file"].includes(field.type)).map(field => field.name));
  return Object.fromEntries(Object.entries(draft).filter(([name]) => (!allowed || allowed.has(name)) && !/(password|passwd|secret|token|api_?key|credential)/i.test(name)));
}

export function writeDraft(scope: RecordScope, draft: Draft, fields?: readonly FieldDefinition[]): boolean {
  const safe = persistentDraft(draft, fields);
  try {
    localStorage.setItem(key("draft", scope), stringify(safe) ?? "{}");
    return true;
  } catch {
    // Draft autosave is best effort, but callers that navigate to a copied
    // draft use the return value to avoid claiming a duplicate was prepared.
    return false;
  }
}

export function clearDraft(scope: RecordScope): void { try { localStorage.removeItem(key("draft", scope)); } catch { /* Unavailable storage must not block a successful save. */ } }

export function clearAdministratorRecordStorage(administrator: string): void {
  try {
  const server = encodeURIComponent(typeof location === "undefined" ? "local" : location.origin);
  const marker = `${namespace}:`;
  const admin = `:${server}:${encodeURIComponent(administrator)}:`;
  for (let index = localStorage.length - 1; index >= 0; index -= 1) {
    const candidate = localStorage.key(index);
    if (candidate?.startsWith(marker) && candidate.includes(admin)) localStorage.removeItem(candidate);
  }
  } catch { /* Session expiry and logout must still complete if browser storage is unavailable. */ }
}

export function downloadJSON(filename: string, value: unknown): void {
  const url = URL.createObjectURL(new Blob([stringify(value, null, 2) ?? "null"], { type: "application/json" }));
  const link = document.createElement("a");
  link.href = url; link.download = filename; link.click(); URL.revokeObjectURL(url);
}
