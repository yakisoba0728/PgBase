import type { ApplyIdentity, Collection } from "../api/collections";
import { ApiError, parseJson, stringifyJson } from "../api/http";
import type { SchemaDocument, SchemaImportPreview } from "../api/schema";
import type { SqlExecutionStatus } from "../api/sql";
import { dangerousStatement, diffPlan, type ChangeItem } from "../schema/draft";

// ---------------------------------------------------------------------------
// Schema transfer
// ---------------------------------------------------------------------------

/** The export without the skipped collections; auth realms stay, since import refuses to drop them. */
export function pickCollections(document: SchemaDocument, skip: string[]): SchemaDocument {
  if (skip.length === 0) return document;
  return { ...document, collections: document.collections.filter((collection) => !skip.includes(collection.name)) };
}

interface ImportSection {
  name: string;
  items: ChangeItem[];
  sql: string[];
  danger: boolean;
}

/** One section per collection; an auth registration step repeats its collection and is folded in.
 * A collection absent from the normalized document is being deleted. */
export function importSections(preview: SchemaImportPreview, catalog: Collection[]): ImportSection[] {
  const kept = new Set([...preview.document.collections, ...(preview.document.authCollections ?? [])].map((entry) => entry.name));
  const sections = new Map<string, ImportSection>();
  for (const change of preview.changes) {
    const name = change.definition.name;
    const sql = change.sql ?? [];
    const known = sections.get(name);
    if (known) {
      known.sql.push(...sql);
      known.danger ||= sql.some(dangerousStatement);
      continue;
    }
    const items = diffPlan(catalog.find((collection) => collection.name === name), kept.has(name) ? change.definition : undefined);
    sections.set(name, { name, items, sql: [...sql], danger: items.some((item) => item.danger) || sql.some(dangerousStatement) });
  }
  return [...sections.values()];
}

/** validation_failed details: { "path": ["code", ...] }. */
export function problemList(details: unknown): { path: string; codes: string[] }[] {
  if (!details || typeof details !== "object") return [];
  return Object.entries(details).map(([path, codes]) => ({ path, codes: Array.isArray(codes) ? codes.map(String) : [String(codes)] }));
}

/** An apply whose outcome this tab has not seen yet; retrying it with the same identity is safe. */
export interface PendingImport {
  identity: ApplyIdentity;
  document: SchemaDocument;
  baseVersion: number;
  checksum: string;
}

const pendingKey = "pgbase.schemaImport.pending";

export function readPending(): PendingImport | null {
  try {
    const raw = sessionStorage.getItem(pendingKey);
    return raw ? (parseJson(raw) as PendingImport) : null;
  } catch {
    return null;
  }
}

export function writePending(pending: PendingImport | null): void {
  try {
    if (pending) sessionStorage.setItem(pendingKey, stringifyJson(pending));
    else sessionStorage.removeItem(pendingKey);
  } catch {
    // Blocked storage only loses recovery after a reload.
  }
}

// ---------------------------------------------------------------------------
// SQL console
// ---------------------------------------------------------------------------

export const sqlActive = (status: SqlExecutionStatus) => status === "pending" || status === "running";

/** Trailing semicolons and space go; leading text stays so server positions still match. */
export const statementText = (sql: string) => sql.replace(/[\s;]+$/, "");

const encoder = new TextEncoder();

/** Line, column and the line's text for a 1-based position; null when out of range. The console's own
 * grammar check reports UTF-8 byte offsets, PostgreSQL reports characters. */
export function errorLocation(sql: string, at: number | undefined, unit: "byte" | "char" = "char"): { line: number; column: number; text: string } | null {
  let position = at ?? 0;
  if (unit === "byte" && position > 0) {
    let bytes = 0;
    let index = 0;
    for (const character of sql) {
      if (bytes >= position - 1) break;
      bytes += encoder.encode(character).length;
      index += character.length;
    }
    position = index + 1;
  }
  if (position < 1 || position > sql.length + 1) return null;
  const before = sql.slice(0, position - 1);
  const start = before.lastIndexOf("\n") + 1;
  const end = sql.indexOf("\n", start);
  return { line: before.split("\n").length, column: position - start, text: sql.slice(start, end < 0 ? undefined : end) };
}

// The server keeps no statement text, so the tab remembers what it sent, scoped to the session.
const historyLimit = 100;
const historyKey = (scope: string) => `pgbase.sql.${scope}`;

export function recallStatements(scope: string): Record<string, string> {
  try {
    const raw = sessionStorage.getItem(historyKey(scope));
    return raw ? (JSON.parse(raw) as Record<string, string>) : {};
  } catch {
    return {};
  }
}

export function rememberStatement(scope: string, id: string, sql: string): void {
  const entries = Object.entries(recallStatements(scope)).filter(([key]) => key !== id);
  entries.push([id, sql]);
  try {
    sessionStorage.setItem(historyKey(scope), JSON.stringify(Object.fromEntries(entries.slice(-historyLimit))));
  } catch {
    // Without storage, history rows just cannot reload their text.
  }
}

/**
 * Whether a failed submit may still have run: only a 4xx answer (the server or a proxy refused it before it started)
 * proves it did not. A dropped connection, a timeout or any 5xx from the server or a proxy (502/503/504) proves nothing.
 * A busy (429) answer to a resend proves nothing about the first attempt either, which is why only that write's own
 * result clears the lost-write state.
 */
export function mayHaveRun(error: unknown): boolean {
  // Below 400: no answer (0) or an accepted request whose body could not be read.
  return !(error instanceof ApiError) || error.status < 400 || error.status === 408 || error.status >= 500;
}
