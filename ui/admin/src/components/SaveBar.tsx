import { useEffect, useId, type ReactNode } from "react";
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

/** The editing state and Discard / Save, laid out by the toolbar or pane footer around it. */
export function SaveBar({ dirty, busy = false, onDiscard, form, onSave, saveLabel, saveDisabled, clean, children }: SaveBarProps) {
  const t = useT();
  const id = useId();
  // Cmd/Ctrl+S presses Save; the first mounted bar that handles the key wins.
  useEffect(() => {
    const onKeyDown = (event: KeyboardEvent) => {
      if (event.key.toLowerCase() !== "s" || !(event.metaKey || event.ctrlKey) || event.shiftKey || event.altKey || event.defaultPrevented) return;
      event.preventDefault();
      const button = document.getElementById(id) as HTMLButtonElement | null;
      if (button && !button.disabled) button.click();
    };
    document.addEventListener("keydown", onKeyDown);
    return () => document.removeEventListener("keydown", onKeyDown);
  }, [id]);
  return (
    <>
      <span className="save-state" data-dirty={dirty ? "" : undefined}>{dirty ? t("common.unsaved") : (clean ?? t("common.saved"))}</span>
      {children}
      <Button variant="secondary" size="sm" disabled={!dirty || busy} onClick={onDiscard}>{t("common.discard")}</Button>
      <Button id={id} type={form || !onSave ? "submit" : "button"} size="sm" form={form} loading={busy} disabled={saveDisabled ?? !dirty} onClick={onSave}>{saveLabel ?? t("common.save")}</Button>
    </>
  );
}
