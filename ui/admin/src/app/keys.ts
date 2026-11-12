import { useEffect, useRef } from "react";

// The one keyboard mechanism: every global shortcut (palette, help, go-to, save, close pane)
// registers here; a single document listener dispatches to the most recently mounted match.

interface ShortcutOptions {
  /** Also fires while typing in a field (combos with Cmd/Ctrl and Escape). */
  inFields?: boolean;
  /** Also fires while a dialog or popover is open. */
  inOverlays?: boolean;
  enabled?: boolean;
}

interface Entry extends ShortcutOptions {
  keys: string;
  /** Returning false passes the key on to the next match. */
  run: (event: KeyboardEvent) => boolean | void;
}

const entries: Entry[] = [];
let pending: { prefix: string; until: number } | null = null;

/** "mod+s", "?", "g", "Escape": modifiers that matter, then the key. Shift only counts with a modifier or a named key. */
export function comboOf(event: Pick<KeyboardEvent, "key" | "metaKey" | "ctrlKey" | "altKey" | "shiftKey">): string {
  const printable = event.key.length === 1;
  const mod = event.metaKey || event.ctrlKey;
  const parts = [mod && "mod", event.altKey && "alt", event.shiftKey && (mod || event.altKey || !printable) && "shift"].filter(Boolean);
  return [...parts, printable ? event.key.toLowerCase() : event.key].join("+");
}

const typing = (target: EventTarget | null) =>
  target instanceof HTMLElement && (target.isContentEditable || target.matches("input:not([type=checkbox]):not([type=radio]):not([type=button]):not([type=submit]), textarea, select"));

function overlayOpen(): boolean {
  try {
    return document.querySelector("dialog[open], :popover-open") !== null;
  } catch {
    // Engines without :popover-open (jsdom) only have dialogs to check.
    return document.querySelector("dialog[open]") !== null;
  }
}

/** Which registered entry a key event runs (exported for tests); also tracks two-key sequences such as "g l". */
export function dispatch(event: KeyboardEvent, now = Date.now()): boolean {
  // Korean and other IMEs report composition keystrokes; they are never shortcuts.
  if (event.defaultPrevented || event.isComposing || event.keyCode === 229) return false;
  const combo = comboOf(event);
  const inField = typing(event.target);
  const overlay = overlayOpen();
  const prefix = pending && pending.until > now ? pending.prefix : null;
  pending = null;
  const candidates = entries.filter((entry) => entry.enabled !== false && (!inField || entry.inFields) && (!overlay || entry.inOverlays));
  const keys = prefix ? `${prefix} ${combo}` : combo;
  for (let index = candidates.length - 1; index >= 0; index--) {
    const entry = candidates[index];
    if (entry.keys !== keys) continue;
    if (entry.run(event) === false) continue;
    event.preventDefault();
    return true;
  }
  if (!prefix && candidates.some((entry) => entry.keys.startsWith(`${combo} `))) {
    pending = { prefix: combo, until: now + 1200 };
    return true;
  }
  return false;
}

let listening = false;

/** Adds a shortcut until the returned function is called; later registrations win. */
export function registerShortcut(entry: Entry): () => void {
  entries.push(entry);
  if (!listening && typeof document !== "undefined") {
    listening = true;
    document.addEventListener("keydown", (event) => void dispatch(event));
  }
  return () => {
    const index = entries.indexOf(entry);
    if (index >= 0) entries.splice(index, 1);
  };
}

/** Registers a shortcut while the component is mounted; `run` may change between renders. */
export function useShortcut(keys: string, run: Entry["run"], options: ShortcutOptions = {}): void {
  const latest = useRef(run);
  latest.current = run;
  const { inFields, inOverlays, enabled = true } = options;
  useEffect(() => {
    if (!enabled) return;
    return registerShortcut({ keys, inFields, inOverlays, run: (event) => latest.current(event) });
  }, [keys, inFields, inOverlays, enabled]);
}

/** Display form of a combo for the current platform ("⌘K" on Apple, "Ctrl+K" elsewhere). */
export function keyLabel(keys: string, apple = typeof navigator !== "undefined" && /Mac|iPhone|iPad/.test(navigator.platform)): string[] {
  return keys.split(" ").map((step) =>
    step
      .split("+")
      .map((part) => (part === "mod" ? (apple ? "⌘" : "Ctrl") : part === "shift" ? (apple ? "⇧" : "Shift") : part === "Escape" ? "Esc" : part.length === 1 ? part.toUpperCase() : part))
      .join(apple ? "" : "+"),
  );
}
