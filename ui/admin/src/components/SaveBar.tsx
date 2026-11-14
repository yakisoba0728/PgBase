import { useId, type ReactNode } from "react";
import { useShortcut } from "../app/keys";
import { useT } from "../i18n";
import { Button } from "./Button";

interface SaveBarProps {
  dirty: boolean;
  busy?: boolean;
  onDiscard: () => void;
  /** Submits this form id; without it the save button calls onSave. */
  form?: string;
  onSave?: () => void;
  saveLabel?: string;
  saveDisabled?: boolean;
  /** Shown while nothing is unsaved (default "Saved"). */
  clean?: string;
  /** Extra actions placed before Discard. */
  children?: ReactNode;
}

/**
 * Cmd/Ctrl+S presses the save button the returned id is given to; the most recently mounted one wins.
 * It works over menus and popovers too; over a modal dialog it only keeps the browser's own Save dialog away.
 */
function useSaveShortcut(): string {
  const id = useId();
  useShortcut(
    "mod+s",
    () => {
      const button = document.getElementById(id) as HTMLButtonElement | null;
      if (!button) return false;
      // Every dialog here opens with showModal().
      const modal = document.querySelector("dialog[open]");
      if (!button.disabled && (!modal || modal.contains(button))) button.click();
    },
    { inFields: true, inOverlays: true },
  );
  return id;
}

/** The editing state and Discard / Save, laid out by the toolbar or pane footer around it. */
export function SaveBar({ dirty, busy = false, onDiscard, form, onSave, saveLabel, saveDisabled, clean, children }: SaveBarProps) {
  const t = useT();
  const id = useSaveShortcut();
  return (
    <>
      <span className="save-state" data-dirty={dirty ? "" : undefined}>
        {/* Keyed by the state, so "Unsaved" and "Saved" settle in as the dot changes colour. */}
        <span key={dirty ? "dirty" : "clean"} className="save-state-text">{dirty ? t("common.unsaved") : (clean ?? t("common.saved"))}</span>
      </span>
      {children}
      <Button variant="secondary" size="sm" disabled={!dirty || busy} onClick={onDiscard}>{t("common.discard")}</Button>
      <Button id={id} aria-keyshortcuts="Meta+S Control+S" type={form || !onSave ? "submit" : "button"} size="sm" form={form} loading={busy} disabled={saveDisabled ?? !dirty} onClick={onSave}>{saveLabel ?? t("common.save")}</Button>
    </>
  );
}
