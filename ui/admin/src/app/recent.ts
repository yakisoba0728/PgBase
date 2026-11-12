import { useEffect } from "react";
import { createStore, readStorage, writeStorage } from "./store";

// Device-local conveniences for the Data group: recently opened collections and the tab last used in each.
// Both name collections, so sign-out forgets them (forgetRecent).

const recentKey = "pgbase.recent";
const tabsKey = "pgbase.collectionTabs";
const maxRecent = 5;
const collectionTabs = ["schema", "rules", "api"];

function readJson<T>(key: string, fallback: T, valid: (value: unknown) => boolean): T {
  try {
    const value: unknown = JSON.parse(readStorage(key) ?? "null");
    return valid(value) ? (value as T) : fallback;
  } catch {
    return fallback;
  }
}

/** The list after opening `name`: most recent first, no duplicates, at most `max`. */
export function pushRecent(list: string[], name: string, max = maxRecent): string[] {
  return [name, ...list.filter((item) => item !== name)].slice(0, max);
}

const recent = createStore<string[]>(readJson(recentKey, [], (value) => Array.isArray(value) && value.every((item) => typeof item === "string")));
let tabs = readJson<Record<string, string>>(tabsKey, {}, (value) => typeof value === "object" && value !== null && !Array.isArray(value));

/** Recently opened collection names, limited to those that still exist. */
export function useRecentCollections(existing: string[] | undefined): string[] {
  const names = recent.use();
  return existing ? names.filter((name) => existing.includes(name)) : [];
}

/** Records a visit to a collection tab (undefined = Records). */
export function useCollectionVisit(name: string, tab: string | undefined): void {
  useEffect(() => {
    if (!name) return;
    const next = pushRecent(recent.get(), name);
    if (next.join("\n") !== recent.get().join("\n")) {
      writeStorage(recentKey, JSON.stringify(next));
      recent.set(next);
    }
    const value = tab && collectionTabs.includes(tab) ? tab : "";
    if ((tabs[name] ?? "") !== value) {
      tabs = { ...tabs, [name]: value };
      if (!value) delete tabs[name];
      writeStorage(tabsKey, JSON.stringify(tabs));
    }
  }, [name, tab]);
}

/** Where opening a collection from the navigation or palette lands: the tab last used there. Deep links never pass through here. */
export function collectionPath(name: string): string {
  const tab = tabs[name];
  return `/collections/${encodeURIComponent(name)}${tab && collectionTabs.includes(tab) ? `/${tab}` : ""}`;
}

/** Sign-out: these name collections of the administrator who used this device. */
export function forgetRecent(): void {
  try {
    localStorage.removeItem(recentKey);
    localStorage.removeItem(tabsKey);
  } catch {
    // Blocked storage held nothing.
  }
  tabs = {};
  recent.set([]);
}
