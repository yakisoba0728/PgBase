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
