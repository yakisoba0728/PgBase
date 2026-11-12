import { useCallback } from "react";
import { createStore, readStorage, writeStorage } from "../app/store";
import { en } from "./en";
import { ko } from "./ko";

type Language = "ko" | "en";
export type MessageKey = keyof typeof en;
export type Translate = (key: MessageKey, vars?: Record<string, string | number>) => string;

const dictionaries: Record<Language, Record<MessageKey, string>> = { en, ko };
export const languages: Language[] = ["ko", "en"];
const storageKey = "pgbase.language";

function initialLanguage(): Language {
  const stored = readStorage(storageKey);
  if (stored === "ko" || stored === "en") return stored;
  return navigator.language.toLowerCase().startsWith("ko") ? "ko" : "en";
}

const store = createStore<Language>(initialLanguage());
document.documentElement.lang = store.get();

export function setLanguage(language: Language): void {
  writeStorage(storageKey, language);
  document.documentElement.lang = language;
  store.set(language);
}

export function translate(language: Language, key: MessageKey, vars?: Record<string, string | number>): string {
  const text = dictionaries[language][key];
  return vars ? text.replace(/\{(\w+)\}/g, (match, name: string) => (name in vars ? String(vars[name]) : match)) : text;
}

export const useLanguage = store.use;

export function useT(): Translate {
  const language = store.use();
  return useCallback((key, vars) => translate(language, key, vars), [language]);
}
