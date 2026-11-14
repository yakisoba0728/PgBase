import { useEffect, useId, useRef, useState, type MouseEvent, type PointerEvent, type ReactNode } from "react";
import { useT } from "../i18n";
import { Button } from "./Button";
import { CopyButton } from "./CopyButton";
import { Field, Textarea } from "./Field";
import { Notice } from "./Feedback";
import { raiseToasts } from "./Toast";

/**
 * Keeps a modal <dialog> in step with `open`; Escape and backdrop clicks call onClose. Every modal uses the `.modal`
 * shell (radius, shadow, surface) with `.modal-head` / `.modal-body` / `.modal-actions` inside; only content layout varies.
 */
export function useModal(open: boolean, onClose: () => void) {
  const ref = useRef<HTMLDialogElement>(null);
  // A drag that starts inside (selecting text) and ends on the backdrop must not close.
  const downOnBackdrop = useRef(false);
  const onBackdrop = (event: { target: EventTarget; currentTarget: HTMLDialogElement; clientX: number; clientY: number }) => {
    const bounds = event.currentTarget.getBoundingClientRect();
    return event.target === event.currentTarget && (event.clientX < bounds.left || event.clientX > bounds.right || event.clientY < bounds.top || event.clientY > bounds.bottom);
  };
  // Bumped when the browser closed the dialog on its own, so the effect re-shows it if `open` still says so.
  const [closedNatively, setClosedNatively] = useState(0);
  useEffect(() => {
    const dialog = ref.current;
    if (!dialog) return;
    if (open && !dialog.open) dialog.showModal();
    if (!open && dialog.open) dialog.close();
    // Closing or unmounting runs the cleanup, which hands toasts back to the page.
    if (open) return raiseToasts(dialog);
  }, [open, closedNatively]);
  return {
    ref,
    // Chrome closes a modal on repeated Escape without a cancelable cancel; a parent that refuses (busy) gets it back.
    onClose: () => {
      if (!open) return;
      onClose();
      setClosedNatively((count) => count + 1);
    },
    onCancel: (event: { preventDefault(): void }) => {
      event.preventDefault();
      onClose();
    },
    onPointerDown: (event: PointerEvent<HTMLDialogElement>) => {
      downOnBackdrop.current = onBackdrop(event);
    },
    onClick: (event: MouseEvent<HTMLDialogElement>) => {
      const down = downOnBackdrop.current;
      downOnBackdrop.current = false;
      if (down && onBackdrop(event)) onClose();
    },
  };
}

/** A titled modal for a short task; its content mounts only while open. */
export function TaskDialog({ open, title, onClose, children }: { open: boolean; title: string; onClose: () => void; children: ReactNode }) {
  const titleId = useId();
  const modal = useModal(open, onClose);
  return (
    <dialog className="modal task-dialog" aria-labelledby={titleId} {...modal}>
      {open && (
        <div className="modal-body">
          <h2 className="modal-title" id={titleId}>{title}</h2>
          {children}
        </div>
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
    <dialog className="modal" aria-labelledby={titleId} {...modal}>
      {open && (
        <div className="modal-body">
          <h2 className="modal-title" id={titleId}>{title}</h2>
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
          <div className="modal-actions">
            <Button variant="secondary" onClick={onCancel} autoFocus={!typed}>{t("common.cancel")}</Button>
            <Button variant={danger ? "danger" : "primary"} loading={busy} disabled={locked} onClick={onConfirm}>
              {confirmLabel ?? t("common.confirm")}
            </Button>
          </div>
        </div>
      )}
    </dialog>
  );
}

/** Shows a secret once, with copy; the caller drops the value on close. */
export function SecretView({ label, value, warning, details, onDone }: { label: string; value: string; warning: string; details?: ReactNode; onDone: () => void }) {
  const t = useT();
  return (
    <>
      <Notice role="note">{warning}</Notice>
      <label className="secret-box">
        <span className="visually-hidden">{label}</span>
        <Textarea mono readOnly rows={4} value={value} spellCheck={false} autoComplete="off" onFocus={(event) => event.currentTarget.select()} />
      </label>
      {details}
      <div className="modal-actions">
        <CopyButton text value={value} />
        <Button onClick={onDone}>{t("common.done")}</Button>
      </div>
    </>
  );
}
