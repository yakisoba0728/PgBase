import { useEffect, useRef, useState, type FormEvent, type ReactNode } from "react";
import { useQuery } from "@tanstack/react-query";
import { stringify } from "lossless-json";
import { RiBracesLine, RiCloseLine, RiErrorWarningLine, RiFileCopy2Line, RiMore2Line } from "@remixicon/react";
import type { Collection } from "../api/collections";
import type { RecordWithETag } from "../api/records";
import { errorText } from "../app/queries";
import { Button, IconButton } from "../components/Button";
import { CopyButton, copyText } from "../components/CopyButton";
import { ConfirmDialog } from "../components/Dialog";
import { Dropdown, DropdownItem } from "../components/Dropdown";
import { EmptyState, Loader } from "../components/Feedback";
import { Field } from "../components/Field";
import { toast } from "../components/Toast";
import { useT } from "../i18n";
import { FieldInput } from "./FieldInput";
import { FileField } from "./FileField";
import { shortDate } from "./fields";
import { recordQuery } from "./queries";
import { expandFor } from "./relation";
import { useRecordForm } from "./useRecordForm";

interface RecordEditorProps {
  collection: Collection;
  /** null creates a new record. */
  recordId: string | null;
  onClose: () => void;
  onCreated: (id: string) => void;
  /** Opens the create form (used by Duplicate). */
  onCreate: () => void;
}

/** The detail pane: loads the record with its ETag, then hands it to the form. */
export function RecordEditor({ collection, recordId, onClose, onCreated, onCreate }: RecordEditorProps) {
  const t = useT();
  const query = useQuery({ ...recordQuery(collection.name, recordId ?? "", expandFor(collection)), enabled: recordId !== null });
  const props = { collection, onClose, onCreated, onCreate };
  if (recordId === null) return <RecordForm key="new" {...props} />;
  if (query.data) return <RecordForm key={`${recordId}:${query.data.etag}`} {...props} loaded={query.data} />;
  return (
    <>
      <PaneHeader title={recordId} onClose={onClose} />
      {query.isError ? (
        <EmptyState icon={RiErrorWarningLine} action={<Button size="sm" variant="secondary" onClick={() => query.refetch()}>{t("common.retry")}</Button>}>
          {errorText(t, query.error)}
        </EmptyState>
      ) : (
        <Loader />
      )}
    </>
  );
}

function PaneHeader({ title, onClose, copy, children }: { title: string; onClose: () => void; copy?: string; children?: ReactNode }) {
  const t = useT();
  return (
    <header className="pane-header">
      <h2 className={copy ? "pane-title mono" : "pane-title"}>{title}</h2>
      {copy && <CopyButton label={t("records.copyId")} value={copy} />}
      {children}
      <IconButton label={t("common.close")} icon={RiCloseLine} onClick={onClose} />
    </header>
  );
}

function RecordForm({ collection, loaded, onClose, onCreated, onCreate }: Omit<RecordEditorProps, "recordId"> & { loaded?: RecordWithETag }) {
  const t = useT();
  const readOnly = collection.type === "view";
  const form = useRecordForm({ collection, loaded, onCreated, onDeleted: onClose });
  const [confirmDelete, setConfirmDelete] = useState(false);
  const formRef = useRef<HTMLFormElement>(null);
  const formId = `record-form-${collection.id}`;
  const onSubmit = (event: FormEvent) => {
    event.preventDefault();
    // Cmd/Ctrl+S submits even while Save is disabled: an untouched record is not re-sent.
    if (!readOnly && !form.save.isPending && (form.dirty || form.isNew)) form.save.mutate();
  };

  // Cmd/Ctrl+S saves the open record from anywhere on the page.
  useEffect(() => {
    if (readOnly) return;
    const onKeyDown = (event: KeyboardEvent) => {
      if ((event.metaKey || event.ctrlKey) && event.key.toLowerCase() === "s") {
        event.preventDefault();
        formRef.current?.requestSubmit();
      }
    };
    document.addEventListener("keydown", onKeyDown);
    return () => document.removeEventListener("keydown", onKeyDown);
  }, [readOnly]);

  const duplicate = () => {
    if (!form.duplicate()) {
      toast(t("records.duplicateFailed"), "danger");
      return;
    }
    onCreate();
  };

  return (
    <>
      <PaneHeader title={form.record ? form.record.id : t("records.new")} copy={form.record?.id} onClose={onClose}>
        {form.record && (
          <Dropdown label={t("records.more")} icon={RiMore2Line}>
            {!readOnly && <DropdownItem icon={RiFileCopy2Line} onSelect={duplicate}>{t("records.duplicate")}</DropdownItem>}
            <DropdownItem icon={RiBracesLine} onSelect={() => copyText(stringify(form.record, null, 2) ?? "", t("copy.done"), t("copy.failed"))}>{t("records.copyJson")}</DropdownItem>
          </Dropdown>
        )}
      </PaneHeader>
      {/* Inert while saving: edits typed now would be lost when the saved version replaces the form. */}
      <div className="pane-body" inert={form.save.isPending}>
        {form.conflict && (
          <div className="pane-notice" role="alert">
            <strong>{t("records.conflictTitle")}</strong>
            <p>{t("records.conflictText")}</p>
            <div className="pane-notice-actions">
              <Button size="sm" onClick={() => form.reload(true)}>{t("records.reloadKeep")}</Button>
              <Button size="sm" variant="transparent" onClick={() => form.reload(false)}>{t("records.reloadDiscard")}</Button>
            </div>
          </div>
        )}
        {form.restoredAt !== null && !form.conflict && (
          <div className="pane-notice" role="status">
            <p>{t("records.restored", { time: new Date(form.restoredAt).toLocaleString() })}</p>
            <Button size="sm" variant="transparent" onClick={form.dropRestored}>{t("records.restoredDiscard")}</Button>
          </div>
        )}
        {readOnly && <p className="field-hint">{t("records.viewReadOnly")}</p>}
        <form ref={formRef} id={formId} className="form" onSubmit={onSubmit} noValidate>
          {form.isNew && collection.recordId?.kind === "text" && (
            <Field
              label="id"
              aside="text"
              mono
              value={form.newId}
              onChange={(event) => form.setNewId(event.target.value)}
              required={!collection.recordId.autogeneratePattern}
              hint={collection.recordId.autogeneratePattern ? t("records.idAuto") : t("records.idRequired")}
              error={form.errors.id}
              autoComplete="off"
              spellCheck={false}
            />
          )}
          {collection.fields.map((field) => {
            if (field.type === "file")
              return <FileField key={field.id} collection={collection.name} recordId={form.record?.id ?? null} field={field} readOnly={readOnly} staged={form.files[field.name] ?? []} onStage={(list) => form.setFiles(field.name, list)} />;
            if (field.type === "autodate") {
              const value = form.record?.[field.name];
              return <Field key={field.id} label={field.name} aside={field.type} value={typeof value === "string" ? shortDate(value) : ""} placeholder={t("records.autodateHint")} readOnly />;
            }
            return (
              <FieldInput
                key={field.id}
                field={field}
                value={form.drafts[field.name]}
                onChange={(value) => form.setDraft(field.name, value)}
                error={form.errors[field.name]}
                readOnly={readOnly}
                expand={form.record?.expand}
              />
            );
          })}
        </form>
      </div>
      {!readOnly && (
        <footer className="pane-footer">
          {form.record && (
            <Button variant="transparent" className="txt-danger pane-footer-start" onClick={() => setConfirmDelete(true)}>{t("common.delete")}</Button>
          )}
          {form.progress !== null && <progress className="pane-progress" max={1} value={form.progress} aria-label={t("file.uploadingAll")} />}
          <span className="pane-shortcut txt-hint" aria-hidden>{navigator.platform.startsWith("Mac") ? "⌘S" : "Ctrl+S"}</span>
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
        onConfirm={() => {
          form.discard();
          form.blocker.proceed?.();
        }}
        onCancel={() => form.blocker.reset?.()}
      />
    </>
  );
}
