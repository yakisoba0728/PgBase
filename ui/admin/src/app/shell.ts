import { createStore, readStorage, writeStorage } from "./store";

// Navigation state shared by the shell and page headers: the rail choice persists, the drawer does not.
const railKey = "pgbase.nav";
const rail = createStore(readStorage(railKey) === "rail");
const drawer = createStore(false);

export const useRail = rail.use;
export const useDrawer = drawer.use;

export function toggleRail(): void {
  writeStorage(railKey, rail.get() ? "full" : "rail");
  rail.set(!rail.get());
}

export const setDrawer = (open: boolean) => drawer.set(open);

// The command palette and the shortcut list are shell overlays any screen (or key) can open.
const palette = createStore(false);
const help = createStore(false);
export const usePalette = palette.use;
export const useHelp = help.use;
export const setPalette = (open: boolean) => palette.set(open);
export const setHelp = (open: boolean) => help.set(open);
export const togglePalette = () => palette.set(!palette.get());
