import { useState, type ChangeEvent } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { RiDeleteBinLine, RiUploadLine } from "@remixicon/react";
import type { CollectionField } from "../api/collections";
import { deleteFile, uploadRecordFile, type StoredFile } from "../api/records";
import { errorText } from "../app/queries";
import { IconButton } from "../components/Button";
import { ConfirmDialog } from "../components/Dialog";
import { toast } from "../components/Toast";
import { useT } from "../i18n";
import { recordFilesQuery } from "./queries";

const sizeText = (bytes: number) => (bytes < 1024 ? `${bytes} B` : bytes < 1048576 ? `${(bytes / 1024).toFixed(1)} KB` : `${(bytes / 1048576).toFixed(1)} MB`);

/** Attachments of one file field: listed, uploaded and deleted directly (no record save involved). */
export function FileField({ collection, recordId, field, readOnly }: { collection: string; recordId: string | null; field: CollectionField; readOnly: boolean }) {
  const t = useT();
  const queryClient = useQueryClient();
  const [removing, setRemoving] = useState<StoredFile | null>(null);
  const files = useQuery({ ...recordFilesQuery(collection, recordId ?? "", field.name), enabled: recordId !== null });
  const refresh = () => queryClient.invalidateQueries({ queryKey: recordFilesQuery(collection, recordId ?? "", field.name).queryKey });
  const upload = useMutation({
    mutationFn: async (list: File[]) => {
      for (const file of list) await uploadRecordFile(collection, recordId!, field.name, file);
    },
    onError: (error) => toast(errorText(t, error), "danger"),
    onSettled: refresh,
  });
  const remove = useMutation({
    mutationFn: (file: StoredFile) => deleteFile(file.id),
    onError: (error) => toast(errorText(t, error), "danger"),
    onSettled: () => {
      setRemoving(null);
      return refresh();
    },
  });
  const onPick = (event: ChangeEvent<HTMLInputElement>) => {
    const list = Array.from(event.target.files ?? []);
    event.target.value = "";
    if (list.length) upload.mutate(list);
  };
  const inputId = `file-${field.id}`;

  return (
    <div className="field-wrap">
      <div className="field">
        <span className="field-label">
          {field.name}
          {field.required && <span className="field-required" aria-hidden>*</span>}
          <span className="mono">{field.type}</span>
        </span>
        {recordId === null ? (
          <p className="field-hint">{t("records.filesAfterSave")}</p>
        ) : (
          <ul className="file-list" aria-label={field.name}>
            {files.data?.map((file) => (
              <li key={file.id}>
                <span title={file.filename}>{file.filename}</span>
                <span className="txt-hint">{sizeText(file.size)}</span>
                {!readOnly && <IconButton label={t("records.deleteFile", { name: file.filename })} icon={RiDeleteBinLine} onClick={() => setRemoving(file)} />}
              </li>
            ))}
            {files.data?.length === 0 && <li className="txt-hint">{t("records.noFiles")}</li>}
            {files.isError && <li className="txt-danger">{errorText(t, files.error)}</li>}
            {!readOnly && (
              <li>
                <input id={inputId} type="file" multiple={(field.options?.maxSelect ?? 1) > 1} className="visually-hidden" disabled={upload.isPending} onChange={onPick} />
                <label htmlFor={inputId} className="link">
                  <RiUploadLine size={14} aria-hidden /> {upload.isPending ? t("records.uploading") : t("records.upload")}
                </label>
              </li>
            )}
          </ul>
        )}
      </div>
      <ConfirmDialog
        open={removing !== null}
        title={t("records.deleteFileTitle")}
        message={removing?.filename}
        confirmLabel={t("common.delete")}
        danger
        busy={remove.isPending}
        onConfirm={() => removing && remove.mutate(removing)}
        onCancel={() => setRemoving(null)}
      />
    </div>
  );
}
