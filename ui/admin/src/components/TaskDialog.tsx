import { useEffect, useId, useRef, useState, type ReactNode } from "react";
import { RiFileCopyLine } from "@remixicon/react";
import { useT } from "../i18n";
import { Button } from "./Button";
import { ConfirmDialog } from "./Dialog";
import { Field } from "./Field";
import { toast } from "./Toast";
import "../styles/kit.css";

/** A modal <dialog> for short tasks; Escape and backdrop clicks call onClose. */
export function TaskDialog({ open, title, onClose, children }: { open: boolean; title: string; onClose: () => void; children: ReactNode }) {
  const ref = useRef<HTMLDialogElement>(null);
  const titleId = useId();
  useEffect(() => {
    const dialog = ref.current;
    if (open && dialog && !dialog.open) dialog.showModal();
    if (!open && dialog?.open) dialog.close();
  }, [open]);
  return (
    <dialog
      ref={ref}
      className="confirm task-dialog"
      aria-labelledby={titleId}
      onCancel={(event) => {
        event.preventDefault();
        onClose();
      }}
      onClick={(event) => {
        if (event.target === event.currentTarget) onClose();
      }}
    >
      {open && (
        <>
          <h2 className="confirm-title" id={titleId}>{title}</h2>
          {children}
        </>
      )}
    </dialog>
  );
}

/** Copies text, with a toast either way. */
export function copyText(t: ReturnType<typeof useT>, value: string) {
  return navigator.clipboard.writeText(value).then(
    () => toast(t("records.copied")),
    () => toast(t("records.copyFailed"), "danger"),
  );
}

/** Shows a secret once, with copy; the caller drops the value on close. */
export function SecretView({ label, value, warning, details, onDone }: { label: string; value: string; warning: string; details?: ReactNode; onDone: () => void }) {
  const t = useT();
  return (
    <>
      <p className="warning-note" role="note">{warning}</p>
      <label className="secret-box">
        <span className="visually-hidden">{label}</span>
        <textarea className="field-control mono" readOnly rows={4} value={value} spellCheck={false} autoComplete="off" onFocus={(event) => event.currentTarget.select()} />
      </label>
      {details}
      <div className="confirm-actions">
        <Button variant="secondary" icon={RiFileCopyLine} onClick={() => void copyText(t, value)}>{t("system.copy")}</Button>
        <Button onClick={onDone}>{t("system.done")}</Button>
      </div>
    </>
  );
}

interface TypedConfirmProps {
  open: boolean;
  title: string;
  message: ReactNode;
  /** What the administrator must type, e.g. a name or email. */
  phrase: string;
  /** The input label, e.g. "Type users to confirm". */
  label: string;
  confirmLabel: string;
  busy?: boolean;
  /** Extra content between the message and the input. */
  children?: ReactNode;
  onConfirm: () => void;
  onCancel: () => void;
}

/** A destructive confirmation that stays locked until the exact phrase is typed. */
export function TypedConfirmDialog({ open, title, message, phrase, label, confirmLabel, busy, children, onConfirm, onCancel }: TypedConfirmProps) {
  const [typed, setTyped] = useState("");
  useEffect(() => {
    if (!open) setTyped("");
  }, [open]);
  return (
    <ConfirmDialog open={open} title={title} message={message} confirmLabel={confirmLabel} danger busy={busy} confirmDisabled={typed !== phrase} onConfirm={onConfirm} onCancel={onCancel}>
      {children}
      <Field label={label} mono autoComplete="off" spellCheck={false} value={typed} onChange={(event) => setTyped(event.target.value)} />
    </ConfirmDialog>
  );
}
