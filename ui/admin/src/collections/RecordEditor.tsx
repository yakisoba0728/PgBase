import { useRef, useState, type FormEvent } from "react";
import { useQuery } from "@tanstack/react-query";
import { stringify } from "lossless-json";
import { RiBracesLine, RiFileCopy2Line, RiMore2Line } from "@remixicon/react";
import type { Collection } from "../api/collections";
import type { RecordWithETag } from "../api/records";
import { useTimeFormat } from "../app/format";
import { Button } from "../components/Button";
import { copyText } from "../components/CopyButton";
import { ConfirmDialog } from "../components/Dialog";
import { Dropdown, DropdownItem } from "../components/Popover";
import { ErrorState, Skeleton, Notice } from "../components/Feedback";
import { SaveBar } from "../components/SaveBar";
import { PaneHeader } from "../components/SplitView";
import { UnsavedGuard } from "../components/UnsavedGuard";
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
  const query = useQuery({ ...recordQuery(collection.name, recordId ?? "", expandFor(collection)), enabled: recordId !== null });
  const props = { collection, onClose, onCreated, onCreate };
  if (recordId === null) return <RecordForm key="new" {...props} />;
  if (query.data) return <RecordForm key={`${recordId}:${query.data.etag}`} {...props} loaded={query.data} />;
  return (
    <>
      <PaneHeader title={recordId} onClose={onClose} />
      {query.isError ? (
        <ErrorState error={query.error} busy={query.isFetching} onRetry={query.refetch} />
      ) : (
        <Skeleton variant="form" />
      )}
    </>
  );
}

function RecordForm({ collection, loaded, onClose, onCreated, onCreate }: Omit<RecordEditorProps, "recordId"> & { loaded?: RecordWithETag }) {
  const t = useT();
  const readOnly = collection.type === "view";
  const time = useTimeFormat();
  const form = useRecordForm({ collection, loaded, onCreated, onDeleted: onClose });
  const [pendingRelations, setPendingRelations] = useState<Record<string, boolean>>({});
  const relationPending = Object.values(pendingRelations).some(Boolean);
  const [confirmDelete, setConfirmDelete] = useState(false);
  const formRef = useRef<HTMLFormElement>(null);
  const formId = `record-form-${collection.id}`;
  const onSubmit = (event: FormEvent) => {
    event.preventDefault();
    // Cmd/Ctrl+S submits even while Save is disabled: an untouched record is not re-sent.
    if (!readOnly && !relationPending && !form.save.isPending && (form.dirty || form.isNew)) form.save.mutate();
  };

  const duplicate = () => {
    if (!form.duplicate()) {
      toast(t("records.duplicateFailed"), "danger");
      return;
    }
    onCreate();
  };

  return (
    <>
      <PaneHeader title={form.record ? form.record.id : t("records.new")} mono={Boolean(form.record)} copy={form.record && { label: t("common.copyId"), value: form.record.id }} onClose={onClose}>
        {form.record && (
          <Dropdown label={t("records.more")} icon={RiMore2Line}>
            {!readOnly && <DropdownItem icon={RiFileCopy2Line} onSelect={duplicate}>{t("records.duplicate")}</DropdownItem>}
            <DropdownItem icon={RiBracesLine} onSelect={() => void copyText(stringify(form.record, null, 2) ?? "")}>{t("records.copyJson")}</DropdownItem>
          </Dropdown>
        )}
      </PaneHeader>
      {/* Inert while saving: edits typed now would be lost when the saved version replaces the form. */}
      <div className="pane-body" inert={form.save.isPending}>
        {form.conflict && (
          <Notice title={t("records.conflictTitle")} actions={<>
            <Button size="sm" onClick={() => form.reload(true)}>{t("records.reloadKeep")}</Button>
            <Button size="sm" variant="transparent" onClick={() => form.reload(false)}>{t("records.reloadDiscard")}</Button>
          </>}>{t("records.conflictText")}</Notice>
        )}
        {form.restoredAt !== null && !form.conflict && (
          <Notice role="status" actions={<Button size="sm" variant="transparent" onClick={form.dropRestored}>{t("records.restoredDiscard")}</Button>}>
            {t("records.restored", { time: time(form.restoredAt, true) })}
          </Notice>
        )}
        {readOnly && <p className="field-hint">{t("records.viewReadOnly")}</p>}
        <form ref={formRef} id={formId} className="form" onSubmit={onSubmit} noValidate>
          {form.isNew && collection.recordId?.kind === "text" && (
            <Field
              label="id"
              aside="text"
              mono
              disabled={form.save.isPending}
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
              return <FileField key={field.id} collection={collection.name} recordId={form.record?.id ?? null} field={field} readOnly={readOnly || form.save.isPending || form.remove.isPending} staged={form.files[field.name] ?? []} onStage={(list) => form.setFiles(field.name, list)} />;
            if (field.type === "autodate") {
              const value = form.record?.[field.name];
              return <Field key={field.id} label={field.name} aside={field.type} value={typeof value === "string" ? shortDate(value) : ""} placeholder={t("records.autodateHint")} readOnly />;
            }
            return (
              <FieldInput
                key={field.id}
                field={field}
                value={form.drafts[field.name]}
                onPendingChange={(pending) => setPendingRelations((current) => current[field.name] === pending ? current : { ...current, [field.name]: pending })}
                onChange={(value) => form.setDraft(field.name, value)}
                error={form.errors[field.name]}
                readOnly={readOnly || form.save.isPending || form.remove.isPending}
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
          <SaveBar form={formId} dirty={form.dirty} busy={form.save.isPending} onDiscard={form.isNew ? onClose : form.reset}
            saveLabel={form.isNew ? t("records.create") : t("records.save")} saveDisabled={relationPending || (!form.dirty && !form.isNew)}>
            {form.progress !== null && <progress className="pane-progress" max={1} value={form.progress} aria-label={t("file.uploadingAll")} />}
          </SaveBar>
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
      <UnsavedGuard {...form.guard} dirty={form.dirty || relationPending} busy={form.save.isPending || form.remove.isPending || relationPending} />
    </>
  );
}
