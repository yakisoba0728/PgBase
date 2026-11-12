import { useCallback, useMemo, useSyncExternalStore } from "react";
import type { Collection } from "../api/collections";
import { readStorage, writeStorage } from "../app/store";

/** Column layout of one collection's grid on this device: display order and hidden columns. */
export interface ColumnPrefs {
  order: string[];
  hidden: string[];
}

const empty: ColumnPrefs = { order: [], hidden: [] };

/** Every column the grid can show, in schema order: fields (no passwords), then the system dates. */
export function availableColumns(collection: Collection): string[] {
  const fields = collection.fields.filter((field) => field.type !== "password").map((field) => field.name);
  return [...fields, ...["created_at", "updated_at"].filter((name) => !fields.includes(name))];
}

/** Visible columns in display order: the saved order first (dropping removed fields), new fields appended. */
export function visibleColumns(available: string[], prefs: ColumnPrefs): string[] {
  const known = prefs.order.filter((name) => available.includes(name));
  const ordered = [...known, ...available.filter((name) => !known.includes(name))];
  return ordered.filter((name) => !prefs.hidden.includes(name));
}

/** All columns in display order (hidden ones included), for the settings list. */
export function orderedColumns(available: string[], prefs: ColumnPrefs): string[] {
  return visibleColumns(available, { ...prefs, hidden: [] });
}

/** Moves `name` one step up (-1) or down (+1) in the full order. */
export function moveColumn(available: string[], prefs: ColumnPrefs, name: string, step: -1 | 1): ColumnPrefs {
  const order = orderedColumns(available, prefs);
  const from = order.indexOf(name);
  const to = from + step;
  if (from < 0 || to < 0 || to >= order.length) return prefs;
  [order[from], order[to]] = [order[to], order[from]];
  return { ...prefs, order };
}

export function toggleColumn(prefs: ColumnPrefs, name: string, show: boolean): ColumnPrefs {
  const hidden = prefs.hidden.filter((item) => item !== name);
  return { ...prefs, hidden: show ? hidden : [...hidden, name] };
}

function parse(text: string | null): ColumnPrefs {
  try {
    const value = JSON.parse(text ?? "null") as Partial<ColumnPrefs> | null;
    const strings = (list: unknown) => (Array.isArray(list) ? list.filter((item): item is string => typeof item === "string") : []);
    return value && typeof value === "object" ? { order: strings(value.order), hidden: strings(value.hidden) } : empty;
  } catch {
    return empty;
  }
}

// Keyed by collection id so a rename keeps the layout.
const key = (collectionId: string) => `pgbase.records.columns:${collectionId}`;
// Also kept in memory, so the layout still works for this page when storage is blocked.
const memory = new Map<string, string>();
const listeners = new Set<() => void>();
const subscribe = (listener: () => void) => {
  listeners.add(listener);
  return () => listeners.delete(listener);
};

/** The collection's column layout, remembered in this browser. */
export function useColumnPrefs(collection: Collection) {
  const raw = useSyncExternalStore(subscribe, () => memory.get(key(collection.id)) ?? readStorage(key(collection.id)));
  const prefs = useMemo(() => parse(raw), [raw]);
  const available = useMemo(() => availableColumns(collection), [collection]);
  const save = useCallback(
    (next: ColumnPrefs | null) => {
      const text = next ? JSON.stringify(next) : "";
      memory.set(key(collection.id), text);
      writeStorage(key(collection.id), text);
      listeners.forEach((listener) => listener());
    },
    [collection.id],
  );
  return { prefs, available, visible: useMemo(() => visibleColumns(available, prefs), [available, prefs]), save };
}
