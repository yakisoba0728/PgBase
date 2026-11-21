import { useCallback } from "react";
import { createStore, readStorage, writeStorage } from "../app/store";
import type { en } from "./en";

type Language = "ko" | "en";
export type MessageKey = keyof typeof en;
export type Translate = (key: MessageKey, vars?: Record<string, string | number>) => string;
type Dictionary = Record<MessageKey, string>;

// Each language is its own chunk, so the first load carries only the one on screen.
const loaders: Record<Language, () => Promise<Dictionary>> = {
  en: () => import("./en").then((module) => module.en),
  ko: () => import("./ko").then((module) => module.ko),
};
const dictionaries: Partial<Record<Language, Dictionary>> = {};
const loading: Partial<Record<Language, Promise<Dictionary>>> = {};
export const languages: Language[] = ["ko", "en"];
const storageKey = "pgbase.language";

function initialLanguage(): Language {
  const stored = readStorage(storageKey);
  if (stored === "ko" || stored === "en") return stored;
  return navigator.language.toLowerCase().startsWith("ko") ? "ko" : "en";
}

const store = createStore<Language>(initialLanguage());
document.documentElement.lang = store.get();

/** Fetches a language's dictionary once; a failed fetch is retried once here and again on the next call. */
export function loadLanguage(language: Language): Promise<void> {
  loading[language] ??= loaders[language]()
    .catch(() => loaders[language]())
    .then(
      (dictionary) => (dictionaries[language] = dictionary),
      (error: unknown) => {
        delete loading[language];
        throw error;
      },
    );
  return loading[language].then(() => undefined);
}

function apply(language: Language): void {
  document.documentElement.lang = language;
  store.set(language);
}

/**
 * The dictionary the first render needs, started by main.tsx alongside the session check. A language whose chunk
 * cannot be fetched falls back to English for this page load without changing the stored choice.
 */
export function loadInitialLanguage(): Promise<void> {
  const language = store.get();
  return loadLanguage(language).catch((error: unknown) => {
    if (language === "en") throw error;
    return loadLanguage("en").then(() => apply("en"));
  });
}

let requested: Language | undefined;

/** Switches at once when the dictionary is loaded, otherwise once it arrives; a failed fetch keeps the current language. */
export function setLanguage(language: Language): Promise<void> {
  requested = language;
  const commit = () => {
    if (requested !== language) return;
    writeStorage(storageKey, language);
    apply(language);
  };
  if (dictionaries[language]) {
    commit();
    return Promise.resolve();
  }
  return loadLanguage(language).then(commit, () => undefined);
}

const current = (): Dictionary | undefined => dictionaries[store.get()] ?? dictionaries.en;

/** Whether a computed key (an error or reason code) has a message; every language has the same keys. */
export function hasMessage(key: string): key is MessageKey {
  const dictionary = current();
  return dictionary !== undefined && key in dictionary;
}

export function translate(language: Language, key: MessageKey, vars?: Record<string, string | number>): string {
  // Only reached before a dictionary has arrived if both failed to load; the key is shown rather than nothing.
  const text = dictionaries[language]?.[key] ?? dictionaries.en?.[key] ?? key;
  if (!vars) return text;
  // {name|one|other} picks the English singular or plural word by the variable.
  return text
    .replace(/\{(\w+)\|([^|}]*)\|([^}]*)\}/g, (match, name: string, one: string, other: string) => (name in vars ? (Number(vars[name]) === 1 ? one : other) : match))
    .replace(/\{(\w+)\}/g, (match, name: string) => (name in vars ? String(vars[name]) : match));
}

export const useLanguage = store.use;

export function useT(): Translate {
  const language = store.use();
  return useCallback((key, vars) => translate(language, key, vars), [language]);
}

/** Translation in the current language outside React (toasts fired from shared helpers). */
export const tNow: Translate = (key, vars) => translate(store.get(), key, vars);
