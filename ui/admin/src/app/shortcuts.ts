import { useEffect } from "react";
import { useNavigate } from "react-router";
import type { MessageKey } from "../i18n";
import { registerShortcut, useShortcut } from "./keys";
import { navGroups } from "./navigation";
import { setHelp, togglePalette } from "./shell";

// The shell's shortcuts; also the list the help overlay shows.
export const general: { keys: string; label: MessageKey }[] = [
  { keys: "mod+k", label: "shortcuts.palette" },
  { keys: "?", label: "shortcuts.help" },
  { keys: "/", label: "shortcuts.filter" },
  { keys: "n", label: "shortcuts.new" },
  { keys: "Escape", label: "shortcuts.close" },
  { keys: "mod+s", label: "shortcuts.save" },
];
export const gotoEntries = [{ key: "c", path: "/collections", label: "nav.collections" as MessageKey }, ...navGroups.flatMap((group) => group.items.map((entry) => ({ key: entry.key, path: `/${entry.path}`, label: entry.label })))];

/** Registers the shell shortcuts (palette, help, filter, new, go-to); Esc and save belong to panes and save bars. */
export function useShellShortcuts(): void {
  const navigate = useNavigate();
  useShortcut("mod+k", togglePalette, { inFields: true });
  useShortcut("?", () => setHelp(true));
  useShortcut("/", () => {
    const input = document.querySelector<HTMLInputElement>("main [data-filter]");
    if (!input) return false;
    input.focus();
    input.select();
  });
  useShortcut("n", () => {
    const button = document.querySelector<HTMLElement>("main [data-new]");
    if (!button || (button as HTMLButtonElement).disabled) return false;
    button.click();
  });
  useEffect(() => {
    const cleanups = gotoEntries.map((entry) => registerShortcut({ keys: `g ${entry.key}`, run: () => void navigate(entry.path) }));
    return () => cleanups.forEach((cleanup) => cleanup());
  }, [navigate]);
}

