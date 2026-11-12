import { useEffect, useId, useRef, type MouseEvent, type ReactNode } from "react";
import { useT } from "../i18n";
import { Button } from "./Button";

/** Keeps a modal <dialog> in step with `open`; Escape and backdrop clicks call onClose. */
function useModal(open: boolean, onClose: () => void) {
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

interface ConfirmDialogProps {
  open: boolean;
  title: string;
  message?: ReactNode;
  confirmLabel?: string;
  danger?: boolean;
  busy?: boolean;
  /** Keeps the confirm button off until a condition holds (e.g. a typed name). */
  confirmDisabled?: boolean;
  /** Extra content below the message, such as a confirmation input. */
  children?: ReactNode;
  onConfirm: () => void;
  onCancel: () => void;
}

export function ConfirmDialog({ open, title, message, confirmLabel, danger, busy, confirmDisabled, children, onConfirm, onCancel }: ConfirmDialogProps) {
  const t = useT();
  const titleId = useId();
  const modal = useModal(open, onCancel);
  return (
    <dialog className="confirm" aria-labelledby={titleId} {...modal}>
      {open && (
        <>
          <h2 className="confirm-title" id={titleId}>{title}</h2>
          {message && <p>{message}</p>}
          {children}
          <div className="confirm-actions">
            <Button variant="secondary" onClick={onCancel} autoFocus>{t("common.cancel")}</Button>
            <Button variant={danger ? "danger" : "primary"} loading={busy} disabled={confirmDisabled} onClick={onConfirm}>
              {confirmLabel ?? t("common.confirm")}
            </Button>
          </div>
        </>
      )}
    </dialog>
  );
}
