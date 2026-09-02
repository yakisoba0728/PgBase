import type { AdminHistory } from "../routing/admin-location";

export interface MemoryHistory extends AdminHistory {
  entries: readonly string[];
  back(): void;
  forward(): void;
}

export function memoryHistory(initial = "/admin/collections"): MemoryHistory {
  const entries = [initial];
  let index = 0;
  const listeners = new Set<() => void>();
  const announce = () => { for (const listener of [...listeners]) listener(); };
  const move = (offset: number) => {
    const next = index + offset;
    if (next < 0 || next >= entries.length) return;
    index = next;
    announce();
  };
  return {
    get entries() { return entries; },
    path: () => entries[index],
    push(path) { entries.splice(index + 1, entries.length, path); index = entries.length - 1; announce(); },
    replace(path) { entries[index] = path; announce(); },
    back: () => move(-1),
    forward: () => move(1),
    listen(onChange) {
      listeners.add(onChange);
      return () => { listeners.delete(onChange); };
    },
  };
}
