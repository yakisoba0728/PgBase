import { useState, type FormEvent } from "react";
import { useQuery } from "@tanstack/react-query";
import { RiCloseLine, RiErrorWarningLine, RiFileCopyLine } from "@remixicon/react";
import type { Collection } from "../api/collections";
import type { RecordWithETag } from "../api/records";
import { errorText } from "../app/queries";
import { Button, IconButton } from "../components/Button";
import { ConfirmDialog } from "../components/Dialog";
import { EmptyState, Loader } from "../components/Feedback";
import { Field } from "../components/Field";
import { toast } from "../components/Toast";
import { useT } from "../i18n";
import { FieldInput } from "./FieldInput";
import { FileField } from "./FileField";
import { shortDate } from "./fields";
import { recordQuery } from "./queries";
import { useRecordForm } from "./useRecordForm";

interface RecordEditorProps {
  collection: Collection;
  /** null creates a new record. */
  recordId: string | null;
  onClose: () => void;
  onCreated: (id: string) => void;
}

/** The detail pane: loads the record with its ETag, then hands it to the form. */
export function RecordEditor({ collection, recordId, onClose, onCreated }: RecordEditorProps) {
  const t = useT();
  const query = useQuery({ ...recordQuery(collection.name, recordId ?? ""), enabled: recordId !== null });
  if (recordId === null) return <RecordForm key="new" collection={collection} onClose={onClose} onCreated={onCreated} />;
  if (query.data) return <RecordForm key={`${recordId}:${query.data.etag}`} collection={collection} loaded={query.data} onClose={onClose} onCreated={onCreated} />;
  return (
    <>
      <PaneHeader title={recordId} onClose={onClose} />
      {query.isError ? <EmptyState icon={RiErrorWarningLine}>{errorText(t, query.error)}</EmptyState> : <Loader />}
    </>
  );
}

function PaneHeader({ title, onClose, copy }: { title: string; onClose: () => void; copy?: string }) {
  const t = useT();
  const onCopy = () => {
    navigator.clipboard.writeText(copy!).then(
      () => toast(t("records.copied")),
      () => toast(t("records.copyFailed"), "danger"),
    );
  };
  return (
    <header className="pane-header">
      <h2 className={copy ? "pane-title mono" : "pane-title"}>{title}</h2>
      {copy && <IconButton label={t("records.copyId")} icon={RiFileCopyLine} onClick={onCopy} />}
      <IconButton label={t("common.close")} icon={RiCloseLine} onClick={onClose} />
    </header>
  );
}

function RecordForm({ collection, loaded, onClose, onCreated }: Omit<RecordEditorProps, "recordId"> & { loaded?: RecordWithETag }) {
  const t = useT();
  const readOnly = collection.type === "view";
  const form = useRecordForm({ collection, loaded, onCreated, onDeleted: onClose });
  const [confirmDelete, setConfirmDelete] = useState(false);
  const formId = `record-form-${collection.id}`;
  const onSubmit = (event: FormEvent) => {
    event.preventDefault();
    if (!readOnly) form.save.mutate();
  };

  return (
    <>
      <PaneHeader title={form.record ? form.record.id : t("records.new")} copy={form.record?.id} onClose={onClose} />
      <div className="pane-body">
        {form.conflict && (
          <div className="pane-notice" role="alert">
            <strong>{t("records.conflictTitle")}</strong>
            <p>{t("records.conflictText")}</p>
            <Button size="sm" variant="secondary" onClick={form.reload}>{t("records.reload")}</Button>
          </div>
        )}
        {readOnly && <p className="field-hint">{t("records.viewReadOnly")}</p>}
        <form id={formId} className="form" onSubmit={onSubmit} noValidate>
          {collection.fields.map((field) => {
            if (field.type === "file") return <FileField key={field.id} collection={collection.name} recordId={form.record?.id ?? null} field={field} readOnly={readOnly} />;
            if (field.type === "autodate") {
              const value = form.record?.[field.name];
              return <Field key={field.id} label={field.name} aside={field.type} value={typeof value === "string" ? shortDate(value) : ""} placeholder={t("records.autodateHint")} readOnly />;
            }
            return (
              <FieldInput key={field.id} field={field} value={form.drafts[field.name]} onChange={(value) => form.setDraft(field.name, value)} error={form.errors[field.name]} readOnly={readOnly} />
            );
          })}
        </form>
      </div>
      {!readOnly && (
        <footer className="pane-footer">
          {form.record && (
            <Button variant="transparent" className="txt-danger pane-footer-start" onClick={() => setConfirmDelete(true)}>{t("common.delete")}</Button>
          )}
          <Button variant="secondary" onClick={onClose}>{t("common.cancel")}</Button>
          <Button type="submit" form={formId} loading={form.save.isPending} disabled={!form.dirty && !form.isNew}>
            {form.isNew ? t("records.create") : t("records.save")}
          </Button>
        </footer>
      )}
      <ConfirmDialog
        open={confirmDelete}
        title={t("records.deleteOneTitle")}
        message={form.record?.id}
        confirmLabel={t("common.delete")}
        danger
        busy={form.remove.isPending}
        onConfirm={() => form.remove.mutate(undefined, { onSettled: () => setConfirmDelete(false) })}
        onCancel={() => setConfirmDelete(false)}
      />
      <ConfirmDialog
        open={form.blocker.state === "blocked"}
        title={t("records.discardTitle")}
        message={t("records.discardText")}
        confirmLabel={t("records.discard")}
        danger
        onConfirm={() => form.blocker.proceed?.()}
        onCancel={() => form.blocker.reset?.()}
      />
    </>
  );
}
