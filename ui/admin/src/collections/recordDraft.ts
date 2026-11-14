import type { CollectionField } from "../api/collections";
import { fieldKind, type Draft } from "./fields";

// Unsaved record edits kept in this browser so a reload, a closed tab or a 412 reload does not lose them.
// Only changed fields are stored, and they are laid over the latest server values when restored, so a
// recovered draft never reverts what another administrator saved to other fields.

const memory = new Map<string, StoredDraft>();
export function keepDraft(scope: DraftScope, fields: Record<string, Draft>) { memory.set(key(scope), { fields, savedAt: Date.now() }); }

const prefix = "pgbase.records.draft:";
const maxAge = 7 * 24 * 3600 * 1000;
// Values that should never sit in browser storage, whatever their field type.
const sensitiveName = /(password|passwd|secret|token|api_?key|credential)/i;

export interface DraftScope {
  admin: string;
  collection: string;
  /** "new" for the create form. */
  record: string;
}

interface StoredDraft {
  savedAt: number;
  fields: Record<string, Draft>;
}

const key = (scope: DraftScope) => `${prefix}${[scope.admin, scope.collection, scope.record].map(encodeURIComponent).join(":")}`;

/** Fields whose edits may be persisted: plain values only, never passwords, files or server-set dates. */
function persistable(field: CollectionField): boolean {
  return !["password", "file", "autodate"].includes(field.type) && !sensitiveName.test(field.name);
}

/** The changed, persistable fields of a form, or null when there is nothing worth keeping. */
export function changedFields(fields: CollectionField[], drafts: Record<string, Draft>, initial: Record<string, Draft>): Record<string, Draft> | null {
  const changed: Record<string, Draft> = {};
  for (const field of fields) {
    if (!persistable(field) || !(field.name in drafts)) continue;
    if (JSON.stringify(drafts[field.name]) !== JSON.stringify(initial[field.name])) changed[field.name] = drafts[field.name];
  }
  return Object.keys(changed).length ? changed : null;
}

export function readDraft(scope: DraftScope, fields: CollectionField[], now = Date.now()): StoredDraft | null {
  const keptInMemory = memory.get(key(scope));
  if (keptInMemory) { memory.delete(key(scope)); return keptInMemory; }
  try {
    const value = JSON.parse(localStorage.getItem(key(scope)) ?? "null") as StoredDraft | null;
    if (!value || typeof value.savedAt !== "number" || typeof value.fields !== "object") return null;
    if (now - value.savedAt > maxAge) {
      clearDraft(scope);
      return null;
    }
    const allowed = new Map(fields.filter(persistable).map((field) => [field.name, field]));
    const kept = Object.entries(value.fields).filter(([name, draft]) => allowed.has(name) && sameShape(draft, allowed.get(name)!));
    return kept.length ? { savedAt: value.savedAt, fields: Object.fromEntries(kept) } : null;
  } catch {
    return null;
  }
}

// A schema change between save and restore (e.g. text -> boolean) must not feed a wrong-typed draft to an input.
function sameShape(draft: unknown, field: CollectionField): boolean {
  if (field.type === "boolean") return typeof draft === "boolean";
  if (["relations", "multiselect", "geo"].includes(fieldKind(field))) return Array.isArray(draft) && draft.every((item) => typeof item === "string");
  return typeof draft === "string";
}

/** Best effort: a full or blocked storage just means no recovery. */
export function writeDraft(scope: DraftScope, fields: Record<string, Draft>, now = Date.now()): boolean {
  try {
    localStorage.setItem(key(scope), JSON.stringify({ savedAt: now, fields } satisfies StoredDraft));
    return true;
  } catch {
    return false;
  }
}

export function clearDraft(scope: DraftScope): void {
  memory.delete(key(scope));
  try {
    localStorage.removeItem(key(scope));
  } catch {
    // Unavailable storage must not block a save or a discard.
  }
}

/** Sign-out: drops every stored record draft on this device, whoever wrote it. */
export function clearAllDrafts(): void {
  memory.clear();
  try {
    for (const name of Object.keys(localStorage)) if (name.startsWith(prefix)) localStorage.removeItem(name);
  } catch {
    // Blocked storage held nothing.
  }
}
