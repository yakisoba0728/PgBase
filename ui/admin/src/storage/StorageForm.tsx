import { useState, type FormEvent } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { RiPlugLine } from "@remixicon/react";
import type { BackupStorageSettings, FileStorageSettings, StorageDriver } from "../api/storage";
import { testBackupStorage, testFileStorage, updateBackupStorageSettings, updateFileStorageSettings } from "../api/storage";
import { Button } from "../components/Button";
import { UnsavedGuard } from "../components/UnsavedGuard";
import { SaveBar } from "../components/SaveBar";
import { ErrorState, Skeleton, Notice } from "../components/Feedback";
import { Checkbox, Field } from "../components/Field";
import { toast } from "../components/Toast";
import { useT } from "../i18n";
import { backupUpdate, draftFrom, fileUpdate, isDirty, storageErrorKey, validateDraft, type DraftErrors, type StorageDraft } from "./model";
import { storageSettingsQuery } from "./queries";

interface FieldsProps {
  draft: StorageDraft;
  onChange: (patch: Partial<StorageDraft>) => void;
  errors: DraftErrors;
  secretConfigured: boolean;
  /** Backup storage adds key prefix and retention. */
  backups?: boolean;
  /** Migration destinations reuse the current secret when left empty. */
  secretHint?: string;
}

/** Driver, S3 location and credentials; S3 fields only show for the S3 driver. */
export function StorageFields({ draft, onChange, errors, secretConfigured, backups, secretHint }: FieldsProps) {
  const t = useT();
  const err = (key: keyof StorageDraft) => (errors[key] ? t(errors[key]) : undefined);
  const s3 = draft.driver === "s3";
  return (
    <>
      <Field kind="select" label={t("storage.form.driver")} value={draft.driver} onChange={(event) => onChange({ driver: event.target.value as StorageDriver })}
        options={[{ value: "local", label: t("storage.local") }, { value: "s3", label: t("storage.s3") }]} />
      {s3 && (
        <>
          <div className="storage-pair">
            <Field label={t("storage.form.bucket")} mono required spellCheck={false} autoComplete="off" value={draft.bucket} error={err("bucket")} onChange={(event) => onChange({ bucket: event.target.value })} />
            <Field label={t("storage.form.region")} mono required spellCheck={false} autoComplete="off" placeholder="us-east-1" value={draft.region} error={err("region")} onChange={(event) => onChange({ region: event.target.value })} />
          </div>
          {backups && <Field label={t("storage.form.prefix")} mono spellCheck={false} autoComplete="off" value={draft.prefix} error={err("prefix")} onChange={(event) => onChange({ prefix: event.target.value })} />}
          <Field label={t("storage.form.endpoint")} mono spellCheck={false} autoComplete="off" placeholder="https://" value={draft.endpoint} error={err("endpoint")} hint={t(backups ? "storage.form.backupEndpointHint" : "storage.form.endpointHint")} onChange={(event) => onChange({ endpoint: event.target.value })} />
          <div className="storage-pair">
            <Field label={t("storage.form.accessKey")} mono spellCheck={false} autoComplete="off" value={draft.accessKey} error={err("accessKey")} onChange={(event) => onChange({ accessKey: event.target.value })} />
            <Field label={t("storage.form.secretKey")} mono type="password" autoComplete="new-password" disabled={draft.clearSecret} value={draft.secretKey} error={err("secretKey")}
              hint={secretHint ?? (secretConfigured ? t("storage.form.secretStored") : t("storage.form.secretNone"))} onChange={(event) => onChange({ secretKey: event.target.value })} />
          </div>
          {secretConfigured && <Checkbox label={t("storage.form.clearSecret")} checked={draft.clearSecret} onChange={(event) => onChange({ clearSecret: event.target.checked, secretKey: "" })} />}
          <Checkbox label={t("storage.form.pathStyle")} checked={draft.usePathStyle} onChange={(event) => onChange({ usePathStyle: event.target.checked })} />
        </>
      )}
      {backups && <Field label={t("storage.form.retention")} mono inputMode="numeric" value={draft.retention} error={err("retention")} hint={t("storage.form.retentionHint")} onChange={(event) => onChange({ retention: event.target.value })} />}
    </>
  );
}

/** Settings for uploaded files or backup archives: Save/Discard, then a connection test of what is saved. */
export function StorageSettingsForm({ kind }: { kind: "files" | "backups" }) {
  const query = useQuery({ ...storageSettingsQuery(kind), refetchOnReconnect: false });
  if (query.isPending) return <Skeleton variant="form" />;
  if (query.isError) return <ErrorState error={query.error} onRetry={query.refetch} />;
  // Keyed by revision: a save or reload starts a clean draft from the server copy.
  return <SettingsEditor key={query.data.revision} kind={kind} saved={query.data} />;
}

function SettingsEditor({ kind, saved }: { kind: "files" | "backups"; saved: FileStorageSettings | BackupStorageSettings }) {
  const t = useT();
  const queryClient = useQueryClient();
  const queryKey = storageSettingsQuery(kind).queryKey;
  const [draft, setDraft] = useState(() => draftFrom(saved));
  const [touched, setTouched] = useState(false);
  const backups = kind === "backups";
  const errors = touched ? validateDraft(draft, saved.secretConfigured, backups) : {};
  const dirty = isDirty(draft, saved);

  const save = useMutation({
    mutationFn: () => (backups ? updateBackupStorageSettings(backupUpdate(draft, saved.revision)) : updateFileStorageSettings(fileUpdate(draft, saved.revision))),
    onSuccess: (next) => {
      queryClient.setQueryData(queryKey, next);
      toast(t("storage.form.saved"));
    },
  });
  const test = useMutation({
    mutationFn: () => (backups ? testBackupStorage() : testFileStorage()),
    onSuccess: () => toast(t("storage.form.testOk")),
    onError: (error) => toast(t(storageErrorKey(error, kind)), "danger"),
  });

  const onSubmit = (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault();
    setTouched(true);
    if (Object.keys(validateDraft(draft, saved.secretConfigured, backups)).length === 0) save.mutate();
  };
  const conflict = save.error && storageErrorKey(save.error, kind).startsWith("storage.error.conflict");

  return (
    <form className="page-form" onSubmit={onSubmit} noValidate>
      <div className="toolbar">
        <SaveBar dirty={dirty} busy={save.isPending} onDiscard={() => { setDraft(draftFrom(saved)); setTouched(false); save.reset(); }}>
          <span className="txt-hint">{t("storage.form.revision", { revision: saved.revision })}</span>
          <Button size="sm" variant="secondary" icon={RiPlugLine} disabled={dirty} title={dirty ? t("storage.form.testDirty") : undefined} loading={test.isPending} onClick={() => test.mutate()}>{t("storage.form.test")}</Button>
        </SaveBar>
      </div>
      <div className="page-content">
        <div className="storage-form">
      <p className="storage-note">{t(backups ? "storage.backups.note" : "storage.files.note")}</p>
      {save.isError && (
        <Notice tone="danger" actions={conflict && <Button variant="secondary" size="sm" onClick={() => queryClient.invalidateQueries({ queryKey })}>{t("storage.reload")}</Button>}>
          {t(storageErrorKey(save.error, kind))}
        </Notice>
      )}
      <fieldset className="form" disabled={save.isPending}>
        <StorageFields draft={draft} errors={errors} secretConfigured={saved.secretConfigured} backups={backups} onChange={(patch) => setDraft((current) => ({ ...current, ...patch }))} />
      </fieldset>
        </div>
      </div>
      <UnsavedGuard dirty={dirty} busy={save.isPending} />
    </form>
  );
}
