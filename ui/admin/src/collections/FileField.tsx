import { useState, type ChangeEvent } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { RiCloseLine, RiDeleteBinLine, RiUploadLine } from "@remixicon/react";
import type { CollectionField } from "../api/collections";
import { deleteFile, getRecord, replaceRecordFiles, uploadRecordFile, type StoredFile } from "../api/records";
import { errorText } from "../app/queries";
import { IconButton } from "../components/Button";
import { ConfirmDialog } from "../components/Dialog";
import { FilePreview } from "../components/FilePreview";
import { formatBytes } from "../app/format";
import { toast } from "../components/Toast";
import { useT, type Translate } from "../i18n";
import { FileThumb } from "./FileThumb";
import { fileUrlQuery, recordFilesQuery } from "./queries";

interface FileFieldProps {
  collection: string;
  /** null while creating: files are staged and sent with the create. */
  recordId: string | null;
  field: CollectionField;
  readOnly: boolean;
  staged: File[];
  onStage: (files: File[]) => void;
}

const accepts = (types: string[] | undefined, type: string) =>
  !type || !types?.length || types.some((pattern) => (pattern.endsWith("/*") ? type.startsWith(pattern.slice(0, -1)) : pattern === type));

/** Picked files that fit the field's limits, and a message for the ones that do not. */
function checkPicked(t: Translate, field: CollectionField, picked: File[], room: number): { ok: File[]; problem?: string } {
  const options = field.options ?? {};
  const wrongType = picked.filter((file) => !accepts(options.mimeTypes, file.type));
  const tooBig = picked.filter((file) => options.maxFileSize && file.size > options.maxFileSize);
  let ok = picked.filter((file) => !wrongType.includes(file) && !tooBig.includes(file));
  const problems: string[] = [];
  if (wrongType.length) problems.push(t("file.wrongType", { names: wrongType.map((file) => file.name).join(", ") }));
  if (tooBig.length) problems.push(t("file.tooBig", { names: tooBig.map((file) => file.name).join(", "), max: formatBytes(options.maxFileSize ?? 0) }));
  if (ok.length > room) {
    problems.push(t("file.tooMany", { max: options.maxSelect ?? 1 }));
    ok = ok.slice(0, Math.max(0, room));
  }
  return { ok, problem: problems.join(" ") || undefined };
}

/** Attachments of one file field: thumbnails, preview, upload with progress and removal. */
export function FileField({ collection, recordId, field, readOnly, staged, onStage }: FileFieldProps) {
  const t = useT();
  const queryClient = useQueryClient();
  const [removing, setRemoving] = useState<StoredFile | null>(null);
  const [previewing, setPreviewing] = useState<StoredFile | null>(null);
  const [uploads, setUploads] = useState<{ name: string; progress: number }[]>([]);
  const files = useQuery({ ...recordFilesQuery(collection, recordId ?? "", field.name), enabled: recordId !== null });
  const preview = useQuery({ ...fileUrlQuery(previewing ?? ({} as StoredFile), "original"), enabled: previewing !== null });
  const max = field.options?.maxSelect ?? 1;
  const current = recordId === null ? staged.length : (files.data?.length ?? 0);
  const replacing = recordId !== null && max === 1 && current === 1;
  const refresh = () => queryClient.invalidateQueries({ queryKey: recordFilesQuery(collection, recordId ?? "", field.name).queryKey });

  const upload = useMutation({
    mutationFn: async (list: File[]) => {
      if (replacing) {
        const latest = await getRecord(collection, recordId!);
        await replaceRecordFiles(collection, recordId!, latest.etag, field.name, list);
        return;
      }
      setUploads(list.map((file) => ({ name: file.name, progress: 0 })));
      for (const [index, file] of list.entries()) {
        await uploadRecordFile(collection, recordId!, field.name, file, {
          onProgress: (progress) => setUploads((previous) => previous.map((item, at) => (at === index ? { ...item, progress } : item))),
        });
        setUploads((previous) => previous.map((item, at) => (at === index ? { ...item, progress: 1 } : item)));
      }
    },
    onError: (error) => toast(errorText(t, error), "danger"),
    onSettled: () => {
      setUploads([]);
      return refresh();
    },
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
    const picked = Array.from(event.target.files ?? []);
    event.target.value = "";
    const { ok, problem } = checkPicked(t, field, picked, replacing ? 1 : max - current);
    if (problem) toast(problem, "danger");
    if (!ok.length) return;
    if (recordId === null) onStage([...staged, ...ok]);
    else upload.mutate(ok);
  };
  const inputId = `file-${field.id}`;
  // The server keeps a required field from losing its last file.
  const lastRequired = field.required && current <= 1;

  return (
    <div className="field-wrap">
      <div className="field">
        <span className="field-label">
          {field.name}
          {field.required && <span className="field-required" aria-hidden>*</span>}
          <span className="mono">
            {field.type} · {t("file.count", { count: current, max })}
          </span>
        </span>
        <ul className="file-list" aria-label={field.name}>
          {recordId === null
            ? staged.map((file, index) => (
                <li key={`${file.name}:${index}`}>
                  <span className="file-thumb"><RiUploadLine size={14} aria-hidden /></span>
                  <span title={file.name}>{file.name}</span>
                  <span className="txt-hint">{formatBytes(file.size)}</span>
                  <IconButton label={t("file.unstage", { name: file.name })} icon={RiCloseLine} onClick={() => onStage(staged.filter((_, at) => at !== index))} />
                </li>
              ))
            : files.data?.map((file) => (
                <li key={file.id}>
                  <button type="button" className="file-open" onClick={() => setPreviewing(file)} aria-label={t("file.preview", { name: file.filename })}>
                    <FileThumb file={file} />
                    <span title={file.filename}>{file.filename}</span>
                  </button>
                  <span className="txt-hint">{formatBytes(file.size)}</span>
                  {!readOnly && (
                    <IconButton label={lastRequired ? t("file.lastRequired") : t("records.deleteFile", { name: file.filename })} icon={RiDeleteBinLine} disabled={lastRequired} onClick={() => setRemoving(file)} />
                  )}
                </li>
              ))}
          {uploads.map((item) => (
            <li key={`up:${item.name}`} className="file-uploading">
              <span title={item.name}>{item.name}</span>
              <progress max={1} value={item.progress} aria-label={t("file.progress", { name: item.name })} />
            </li>
          ))}
          {recordId !== null && files.data?.length === 0 && !uploads.length && <li className="txt-hint">{t("records.noFiles")}</li>}
          {files.isError && <li className="txt-danger">{errorText(t, files.error)}</li>}
          {!readOnly && (current < max || replacing) && !upload.isPending && (
            <li>
              <input id={inputId} type="file" multiple={max - current > 1} accept={field.options?.mimeTypes?.join(",")} className="visually-hidden" onChange={onPick} />
              <label htmlFor={inputId} className="link">
                <RiUploadLine size={14} aria-hidden /> {replacing ? t("file.replace") : recordId === null ? t("file.choose") : t("records.upload")}
              </label>
              {field.options?.maxFileSize ? <span className="txt-hint">{t("file.maxSize", { max: formatBytes(field.options.maxFileSize) })}</span> : null}
            </li>
          )}
        </ul>
        {recordId === null && staged.length > 0 && <p className="field-hint file-staged-note">{t("file.sentOnCreate")}</p>}
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
      <FilePreview
        file={previewing && { name: previewing.filename, size: previewing.size, contentType: previewing.contentType }}
        url={preview.data}
        error={preview.isError ? errorText(t, preview.error) : undefined}
        onClose={() => setPreviewing(null)}
      />
    </div>
  );
}
