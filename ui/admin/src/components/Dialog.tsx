import { useEffect, useId, useRef, useState, type MouseEvent, type ReactNode } from "react";
import { useT } from "../i18n";
import { Button } from "./Button";
import { CopyButton } from "./CopyButton";
import { Field } from "./Field";
import "../styles/kit.css";

/** Keeps a modal <dialog> in step with `open`; Escape and backdrop clicks call onClose. */
export function useModal(open: boolean, onClose: () => void) {
  const ref = useRef<HTMLDialogElement>(null);
  useEffect(() => {
    const dialog = ref.current;
    if (!dialog) return;
    if (open && !dialog.open) dialog.showModal();
    if (!open && dialog.open) dialog.close();
  }, [open]);
  return {
    ref,
    onCancel: (event: { preventDefault(): void }) => {
      event.preventDefault();
      onClose();
    },
    onClick: (event: MouseEvent<HTMLDialogElement>) => {
      if (event.target === event.currentTarget) onClose();
    },
  };
}

/** A titled modal for a short task; its content mounts only while open. */
export function TaskDialog({ open, title, onClose, children }: { open: boolean; title: string; onClose: () => void; children: ReactNode }) {
  const titleId = useId();
  const modal = useModal(open, onClose);
  return (
    <dialog className="confirm task-dialog" aria-labelledby={titleId} {...modal}>
      {open && (
        <>
          <h2 className="confirm-title" id={titleId}>{title}</h2>
          {children}
        </>
      )}
    </dialog>
  );
}

interface ConfirmDialogProps {
  open: boolean;
  title: string;
  message?: ReactNode;
  confirmLabel?: string;
  danger?: boolean;
  busy?: boolean;
  /** Keeps the confirm button off until a condition holds (e.g. an acknowledgement). */
  confirmDisabled?: boolean;
  /** High-risk actions: the confirm button stays off until this exact text (a name, an email) is typed. */
  typed?: { phrase: string; label: string };
  /** Extra content below the message. */
  children?: ReactNode;
  onConfirm: () => void;
  onCancel: () => void;
}

/** The one confirmation: Cancel is focused first; dangerous actions use the danger button and, at the highest risk, a typed phrase. */
export function ConfirmDialog({ open, title, message, confirmLabel, danger, busy, confirmDisabled, typed, children, onConfirm, onCancel }: ConfirmDialogProps) {
  const t = useT();
  const titleId = useId();
  const modal = useModal(open, onCancel);
  const [text, setText] = useState("");
  useEffect(() => {
    if (!open) setText("");
  }, [open]);
  const locked = Boolean(confirmDisabled) || (typed !== undefined && text.trim() !== typed.phrase);
  return (
    <dialog className="confirm" aria-labelledby={titleId} {...modal}>
      {open && (
        <>
          <h2 className="confirm-title" id={titleId}>{title}</h2>
          {message && <p>{message}</p>}
          {children}
          {typed && (
            <Field
              label={typed.label}
              mono
              autoComplete="off"
              spellCheck={false}
              value={text}
              onChange={(event) => setText(event.target.value)}
              onKeyDown={(event) => {
                if (event.key === "Enter" && !event.nativeEvent.isComposing && !locked && !busy) onConfirm();
              }}
            />
          )}
          <div className="confirm-actions">
            <Button variant="secondary" onClick={onCancel} autoFocus={!typed}>{t("common.cancel")}</Button>
            <Button variant={danger ? "danger" : "primary"} loading={busy} disabled={locked} onClick={onConfirm}>
              {confirmLabel ?? t("common.confirm")}
            </Button>
          </div>
        </>
      )}
    </dialog>
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
        <CopyButton text value={value} />
        <Button onClick={onDone}>{t("common.done")}</Button>
      </div>
    </>
  );
}
