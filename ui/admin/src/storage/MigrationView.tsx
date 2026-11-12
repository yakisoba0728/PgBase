import { useEffect, useState, type FormEvent } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { RiErrorWarningLine } from "@remixicon/react";
import { cancelStorageMigration, previewStorageMigration, resumeStorageMigration, startStorageMigration, type FileStorageSettings, type StorageMigrationStatus } from "../api/storage";
import { errorText } from "../app/queries";
import { Button } from "../components/Button";
import { ConfirmDialog } from "../components/Dialog";
import { EmptyState, Loader } from "../components/Feedback";
import { toast } from "../components/Toast";
import { useT, type Translate } from "../i18n";
import { useParams, useTimeFormat } from "../ops/queries";
import { newIdentity } from "../schema/apply";
import { destinationOf, draftFrom, storageErrorKey, validateDraft, type StorageDraft } from "./model";
import { fileStorageQuery, migrationQuery, migrationRunning } from "./queries";
import { StorageFields } from "./StorageForm";

const stateTone: Partial<Record<StorageMigrationStatus["status"], string>> = { failed: "danger", committed: "success", copying: "accent", canceling: "warning", ready: "accent", activating: "accent" };

function describeSettings(t: Translate, settings: Pick<FileStorageSettings, "driver" | "bucket" | "region" | "endpoint">): string {
  if (settings.driver === "local") return t("storage.local");
  return [`s3://${settings.bucket}`, settings.region, settings.endpoint].filter(Boolean).join(" · ");
}

/** Copies every stored file to a new destination, then switches over; the migration id stays in the URL. */
export function MigrationView() {
  const t = useT();
  const files = useQuery(fileStorageQuery);
  const { params } = useParams();
  const migration = useQuery(migrationQuery(params.get("migration")));
  if (files.isPending || migration.isPending) return <Loader />;
  if (files.isError || migration.isError) return <EmptyState icon={RiErrorWarningLine}>{errorText(t, files.error ?? migration.error)}</EmptyState>;
  return (
    <div className="storage-form">
      <section className="storage-block" aria-label={t("storage.migration.source")}>
        <h2 className="ops-subtitle">{t("storage.migration.source")}</h2>
        <p className="mono">{describeSettings(t, files.data)}</p>
      </section>
      <MigrationStatus migration={migration.data} />
      <DestinationForm key={files.data.revision} current={files.data} locked={migrationRunning(migration.data?.status)} />
    </div>
  );
}

function MigrationStatus({ migration }: { migration: StorageMigrationStatus | null }) {
  const t = useT();
  const time = useTimeFormat();
  const queryClient = useQueryClient();
  const [confirm, setConfirm] = useState(false);
  const refresh = () => queryClient.invalidateQueries({ queryKey: ["storage"] });
  const onError = (error: unknown) => toast(t(storageErrorKey(error, "migration")), "danger");
  const cancel = useMutation({ mutationFn: (id: string) => cancelStorageMigration(id), onSuccess: () => setConfirm(false), onError, onSettled: refresh });
  const resume = useMutation({ mutationFn: (id: string) => resumeStorageMigration(id), onError, onSettled: refresh });
  const committed = migration?.status === "committed";
  // The switch-over changes the active file storage.
  useEffect(() => {
    if (committed) void queryClient.invalidateQueries({ queryKey: fileStorageQuery.queryKey });
  }, [committed, queryClient]);
  if (!migration) return <p className="storage-note">{t("storage.migration.none")}</p>;
  const { status } = migration;
  const canCancel = status === "copying" || status === "canceling" || status === "ready" || status === "failed";
  const canResume = status === "failed" || status === "ready";

  return (
    <section className="storage-block" aria-label={t("storage.migration.latest")}>
      <h2 className="ops-subtitle">{t("storage.migration.latest")}</h2>
      <dl className="ops-props">
        <div><dt>{t("storage.migration.status")}</dt><dd><span className="ops-dot" data-tone={stateTone[status]}>{t(`storage.migration.state.${status}`)}</span></dd></div>
        <div>
          <dt>{t("storage.migration.copied")}</dt>
          <dd>
            <span className="mono">{t("storage.migration.progress", { copied: migration.copiedObjects, total: migration.totalObjects })}</span>
            <progress className="storage-progress" value={migration.copiedObjects} max={Math.max(migration.totalObjects, 1)} />
          </dd>
        </div>
        {migration.failure && <div><dt>{t("storage.migration.failure")}</dt><dd data-tone="danger">{migration.failure}</dd></div>}
        <div><dt>{t("storage.migration.started")}</dt><dd>{time(migration.createdAt, true)}</dd></div>
        <div><dt>{t("storage.migration.updated")}</dt><dd>{time(migration.updatedAt, true)}</dd></div>
        <div><dt>{t("storage.migration.id")}</dt><dd className="mono">{migration.id}</dd></div>
      </dl>
      {(canCancel || canResume) && (
        <div className="storage-actions">
          {canResume && <Button variant="secondary" loading={resume.isPending} onClick={() => resume.mutate(migration.id)}>{t("storage.migration.resume")}</Button>}
          {canCancel && <Button variant="danger" disabled={status === "canceling"} onClick={() => setConfirm(true)}>{t("storage.migration.cancel")}</Button>}
        </div>
      )}
      <ConfirmDialog open={confirm} danger title={t("storage.migration.cancelTitle")} message={t("storage.migration.cancelText")} confirmLabel={t("storage.migration.cancel")}
        busy={cancel.isPending} onConfirm={() => cancel.mutate(migration.id)} onCancel={() => setConfirm(false)} />
    </section>
  );
}

function DestinationForm({ current, locked }: { current: FileStorageSettings; locked: boolean }) {
  const t = useT();
  const queryClient = useQueryClient();
  const { update } = useParams();
  // Starts from the other driver: a migration to the same location is rejected.
  const [draft, setDraft] = useState<StorageDraft>(() => ({ ...draftFrom(current), driver: current.driver === "local" ? "s3" : "local", accessKey: "", secretKey: "" }));
  const [touched, setTouched] = useState(false);
  const errors = touched ? validateDraft(draft, current.secretConfigured, false) : {};
  const change = (patch: Partial<StorageDraft>) => {
    setDraft((value) => ({ ...value, ...patch }));
    preview.reset();
  };

  const preview = useMutation({ mutationFn: () => previewStorageMigration(destinationOf(draft)) });
  const start = useMutation({
    mutationFn: () => {
      const id = newIdentity().operationId;
      // In the URL before the request: a lost response is recovered by id after a reload.
      update({ migration: id });
      return startStorageMigration(id, destinationOf(draft));
    },
    onSuccess: (started) => {
      queryClient.setQueryData(migrationQuery(started.id).queryKey, started);
      preview.reset();
    },
    onError: () => update({ migration: null }),
  });
  const onSubmit = (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault();
    setTouched(true);
    if (Object.keys(validateDraft(draft, current.secretConfigured, false)).length === 0) preview.mutate();
  };
  const failed = preview.error ?? start.error;

  return (
    <form className="storage-block" onSubmit={onSubmit} noValidate aria-label={t("storage.migration.target")}>
      <h2 className="ops-subtitle">{t("storage.migration.target")}</h2>
      {locked && <p className="ops-note">{t("storage.migration.running")}</p>}
      {failed && (
        <div className="pane-notice" data-tone="danger" role="alert">
          <p>{t(storageErrorKey(failed, "migration"))}</p>
        </div>
      )}
      <fieldset className="form" disabled={locked || start.isPending}>
        <StorageFields draft={draft} errors={errors} secretConfigured={false} secretHint={t("storage.migration.secretHint")} onChange={change} />
      </fieldset>
      <footer className="storage-actions">
        {preview.data && <span className="storage-preview mono">{t("storage.migration.previewResult", { count: preview.data.totalObjects })}</span>}
        <Button type="submit" variant="secondary" disabled={locked} loading={preview.isPending}>{t("storage.migration.preview")}</Button>
        <Button disabled={locked || !preview.data} loading={start.isPending} onClick={() => start.mutate()}>{t("storage.migration.start")}</Button>
      </footer>
    </form>
  );
}
