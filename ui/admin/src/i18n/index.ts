import { useSyncExternalStore } from "react";
import { commonMessages } from "./common";
import { shellMessages } from "./shell";
import { accessMessages } from "./access";
import { collectionMessages } from "./collections";
import { recordMessages } from "./records";
import { authMessages } from "./auth";
import { settingsMessages } from "./settings";
import { operationMessages } from "./operations";

export type Locale = "ko" | "en";
export type Message = string | Readonly<{ en: string; ko: string }>;
type Values = Record<string, string | number | bigint | boolean | null | undefined>;
const dictionary = { ...shellMessages, ...accessMessages, ...collectionMessages, ...recordMessages, ...authMessages, ...settingsMessages, ...operationMessages, ...commonMessages };
const storageKey = "pgbase.locale";
const validLocale = (value: unknown): value is Locale => value === "ko" || value === "en";
const browserLocale = (): Locale => typeof navigator !== "undefined" && navigator.language.toLowerCase().startsWith("ko") ? "ko" : "en";
function initialLocale(): Locale {
  try { const saved = localStorage.getItem(storageKey); if (validLocale(saved)) return saved; } catch { /* browser language remains available without storage */ }
  return browserLocale();
}
let locale = initialLocale();
const listeners = new Set<() => void>();
export const getLocale = (): Locale => locale;
export function message(en: string, ko: string): Message { return { en, ko }; }
export function t(value: Message | null | undefined, values?: Values): string {
  if (value == null) return "";
  const text = typeof value === "string" ? (locale === "ko" ? dictionary[value] ?? value : value) : value[locale];
  return values ? text.replace(/\{(\w+)\}/g, (token, key: string) => Object.hasOwn(values, key) ? String(values[key] ?? "") : token) : text;
}
function updateDocument() {
  if (typeof document === "undefined") return;
  document.documentElement.lang = locale;
  if (["pgBase administration", "pgBase 관리"].includes(document.title)) document.title = t("pgBase administration");
}
function changeLocale(next: Locale) {
  if (next === locale) { updateDocument(); return; }
  locale = next; updateDocument(); listeners.forEach(listener => listener());
}
export function setLocale(next: Locale) {
  if (!validLocale(next)) return;
  try { localStorage.setItem(storageKey, next); } catch { /* language selection still works for this tab */ }
  changeLocale(next);
}
function storageChanged(event: StorageEvent) {
  if (event.key === storageKey) changeLocale(validLocale(event.newValue) ? event.newValue : browserLocale());
}
function subscribe(listener: () => void) {
  if (!listeners.size && typeof window !== "undefined") window.addEventListener("storage", storageChanged);
  listeners.add(listener);
  return () => { listeners.delete(listener); if (!listeners.size && typeof window !== "undefined") window.removeEventListener("storage", storageChanged); };
}
export function useLocale() {
  const current = useSyncExternalStore(subscribe, getLocale, () => "en" as Locale);
  return { locale: current, setLocale, t };
}
export function formatDate(value: Date | string | number, options?: Intl.DateTimeFormatOptions): string {
  const date = value instanceof Date ? value : new Date(value);
  return Number.isNaN(date.getTime()) ? String(value) : new Intl.DateTimeFormat(locale === "ko" ? "ko-KR" : "en-US", options ?? { dateStyle: "medium", timeStyle: "short" }).format(date);
}
updateDocument();
