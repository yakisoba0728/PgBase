import { createStore, readStorage, writeStorage } from "./store";

type ThemeMode = "light" | "dark" | "auto";
export const themeModes: ThemeMode[] = ["light", "dark", "auto"];
const storageKey = "pgbase.theme";

function stored(): ThemeMode {
  const value = readStorage(storageKey);
  return value === "light" || value === "dark" ? value : "auto";
}

const store = createStore<ThemeMode>(stored());

/** "auto" leaves data-theme unset so tokens.css follows prefers-color-scheme. */
function apply(mode: ThemeMode): void {
  if (mode === "auto") delete document.documentElement.dataset.theme;
  else document.documentElement.dataset.theme = mode;
}

apply(store.get());

export function setTheme(mode: ThemeMode): void {
  writeStorage(storageKey, mode);
  apply(mode);
  store.set(mode);
}

export const useTheme = store.use;
