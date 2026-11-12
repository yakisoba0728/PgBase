import { useEffect, useId, useRef, type ReactNode } from "react";
import { RiFileCopyLine } from "@remixicon/react";
import { Button } from "../components/Button";
import { toast } from "../components/Toast";
import { useT } from "../i18n";

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
      className="confirm sys-dialog"
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

/** Shows a secret once, with copy; the caller drops the value on close. */
export function SecretView({ label, value, warning, onDone }: { label: string; value: string; warning: string; onDone: () => void }) {
  const t = useT();
  const copy = () =>
    navigator.clipboard.writeText(value).then(
      () => toast(t("records.copied")),
      () => toast(t("records.copyFailed"), "danger"),
    );
  return (
    <>
      <p className="sys-warning" role="note">{warning}</p>
      <label className="sys-secret">
        <span className="visually-hidden">{label}</span>
        <textarea className="field-control mono" readOnly rows={4} value={value} spellCheck={false} onFocus={(event) => event.currentTarget.select()} />
      </label>
      <div className="confirm-actions">
        <Button variant="secondary" icon={RiFileCopyLine} onClick={copy}>{t("system.copy")}</Button>
        <Button onClick={onDone}>{t("system.done")}</Button>
      </div>
    </>
  );
}
