import { useEffect, useState, type FormEvent } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Facts } from "../components/Facts";
import { cancelStorageMigration, previewStorageMigration, resumeStorageMigration, startStorageMigration, type FileStorageSettings, type StorageMigrationStatus } from "../api/storage";
import { Button } from "../components/Button";
import { ConfirmDialog } from "../components/Dialog";
import { Notice, Skeleton } from "../components/Feedback";
import { combineQueries, QueryView, useHeld } from "../components/QueryView";
import { toast } from "../components/Toast";
import { ApiError, captureSessionScope, isNotFound } from "../api/http";
import { trackOperation, untrackOperation, useOperations, watchOperation } from "../app/operations";
import { JobProgress } from "../components/JobProgress";
import { useT, type Translate } from "../i18n";
import { useTimeFormat } from "../app/format";
import { useSearchState } from "../app/search";
import { newIdentity } from "../schema/apply";
import { destinationOf, draftFrom, storageErrorKey, validateDraft, type StorageDraft } from "./model";
import { awaitingStart, fileStorageQuery, migrationQuery, migrationRunning, migrationStartedAt } from "./queries";
import { StorageFields, StorageOff } from "./StorageForm";


function describeSettings(t: Translate, settings: Pick<FileStorageSettings, "driver" | "bucket" | "region" | "endpoint">): string {
  if (settings.driver === "local") return t("storage.local");
  return [`s3://${settings.bucket}`, settings.region, settings.endpoint].filter(Boolean).join(" · ");
}

/** Copies every stored file to a new destination, then switches over; the migration id stays in the URL. */
export function MigrationView() {
  const t = useT();
  const files = useQuery(fileStorageQuery);
  const { params } = useSearchState();
  const id = params.get("migration");
  const startedAt = migrationStartedAt(useOperations(), id);
  // Starting writes the new id to the URL first; the previous answer stays so the form (and its pending start) stays mounted.
  const migration = useHeld(useQuery(migrationQuery(id)));
  if (files.data === undefined && isNotFound(files.error)) return <StorageOff kind="files" />;
  // null is a real answer (no migration yet); only a missing one replaces the screen.
  return (
    <QueryView query={combineQueries(files, migration)} skeleton="form">
      {([current, latest]) => {
        // Judged at each answer's time (dataUpdatedAt also re-renders on unchanged polls), so the lock ends with the start window.
        const starting = awaitingStart(id, latest, startedAt, migration.dataUpdatedAt);
        return (
          <div className="storage-form">
            <section className="storage-block" aria-label={t("storage.migration.source")}>
              <h2 className="subtitle">{t("storage.migration.source")}</h2>
              <p className={current.driver === "local" ? undefined : "mono"}>{describeSettings(t, current)}</p>
            </section>
            {starting ? (
              <section className="storage-block" aria-label={t("storage.migration.latest")}>
                <h2 className="subtitle">{t("storage.migration.latest")}</h2>
                <JobProgress title={t("storage.migration.starting")} state="running" detail={t("storage.migration.startingText")} meta={<span className="mono">{id}</span>} />
              </section>
            ) : migration.isHeld ? (
              // The held answer is another id's: never shown as this one's status.
              <Skeleton variant="list" />
            ) : (
              <MigrationStatus migration={latest} />
            )}
            <DestinationForm key={current.revision} current={current} locked={starting || migrationRunning(latest?.status)} />
          </div>
        );
      }}
    </QueryView>
  );
}

function MigrationStatus({ migration }: { migration: StorageMigrationStatus | null }) {
  const t = useT();
  const time = useTimeFormat();
  const queryClient = useQueryClient();
  const [confirm, setConfirm] = useState(false);
  const refresh = () => queryClient.invalidateQueries({ queryKey: ["storage"] });
  type Action = { id: string; scope: ReturnType<typeof captureSessionScope> };
  const onError = (error: unknown, { scope }: Action) => { if (scope.isCurrent()) toast(t(storageErrorKey(error, "migration")), "danger"); };
  const onSettled = (_result: unknown, _error: unknown, { scope }: Action) => { if (scope.isCurrent()) return refresh(); };
  const cancel = useMutation({
    mutationFn: ({ id, scope }: Action) => scope.run(() => cancelStorageMigration(id)),
    onSuccess: (_result, { scope }) => { if (scope.isCurrent()) setConfirm(false); },
    onError,
    onSettled,
  });
  const resume = useMutation({
    mutationFn: ({ id, scope }: Action) => scope.run(() => resumeStorageMigration(id)),
    onSuccess: (resumed, { scope }) => { if (scope.isCurrent()) trackOperation({ kind: "migration", id: resumed.id }); },
    onError,
    onSettled,
  });
  const committed = migration?.status === "committed";
  // The switch-over changes the active file storage.
  useEffect(() => {
    if (committed) void queryClient.invalidateQueries({ queryKey: fileStorageQuery.queryKey });
  }, [committed, queryClient]);
  if (!migration) return <p className="storage-note">{t("storage.migration.none")}</p>;
  const { status } = migration;
  const canCancel = status === "copying" || status === "canceling" || status === "ready" || status === "failed";
  const canResume = status === "failed" || status === "ready";
  const running = migrationRunning(status);

  return (
    <section className="storage-block" aria-label={t("storage.migration.latest")}>
      <h2 className="subtitle">{t("storage.migration.latest")}</h2>
      <JobProgress
        title={t(`storage.migration.state.${status}`)}
        state={running ? "running" : status === "committed" ? "done" : status === "failed" ? "failed" : "attention"}
        detail={t("storage.migration.progress", { copied: migration.copiedObjects, total: migration.totalObjects })}
        meta={`${time(migration.updatedAt)}`}
        done={migration.copiedObjects}
        total={migration.totalObjects}
        error={migration.failure}
        action={
          (canCancel || canResume) && (
            <div className="actions">
              {canResume && <Button size="sm" variant="secondary" loading={resume.isPending} onClick={() => resume.mutate({ id: migration.id, scope: captureSessionScope() })}>{t("storage.migration.resume")}</Button>}
              {canCancel && <Button size="sm" variant="danger" disabled={status === "canceling"} onClick={() => setConfirm(true)}>{t("storage.migration.cancel")}</Button>}
            </div>
          )
        }
      />
      <Facts items={[
        { label: t("storage.migration.started"), value: time(migration.createdAt, true) },
        { label: t("storage.migration.updated"), value: time(migration.updatedAt, true) },
        { label: t("storage.migration.id"), value: migration.id, mono: true, copy: true },
      ]} />
      <ConfirmDialog open={confirm} danger title={t("storage.migration.cancelTitle")} message={t("storage.migration.cancelText")} confirmLabel={t("storage.migration.cancel")}
        busy={cancel.isPending} onConfirm={() => cancel.mutate({ id: migration.id, scope: captureSessionScope() })} onCancel={() => setConfirm(false)} />
    </section>
  );
}

function DestinationForm({ current, locked }: { current: FileStorageSettings; locked: boolean }) {
  const t = useT();
  const queryClient = useQueryClient();
  const { update } = useSearchState();
  // Starts from the other driver: a migration to the same location is rejected.
  const [draft, setDraft] = useState<StorageDraft>(() => ({ ...draftFrom(current), driver: current.driver === "local" ? "s3" : "local", accessKey: "", secretKey: "" }));
  const [touched, setTouched] = useState(false);
  const errors = touched ? validateDraft(draft, current.secretConfigured, false) : {};
  const change = (patch: Partial<StorageDraft>) => {
    setDraft((value) => ({ ...value, ...patch }));
    preview.reset();
  };

  const preview = useMutation({ mutationFn: (scope: ReturnType<typeof captureSessionScope>) => scope.run(() => previewStorageMigration(destinationOf(draft))) });
  const [confirm, setConfirm] = useState(false);
  const start = useMutation({
    mutationFn: (scope: ReturnType<typeof captureSessionScope>) => scope.run(() => {
      const id = newIdentity().operationId;
      // Recorded before the request: a lost response is recovered by id after a reload, from any page.
      update({ migration: id });
      trackOperation({ kind: "migration", id });
      // The navigation waits for the answer instead of reading the previous migration as this one.
      const release = watchOperation(id);
      return startStorageMigration(id, destinationOf(draft))
        .catch((error: unknown) => {
          // Only a definite refusal frees the id; a lost response may still have started it.
          if (scope.isCurrent() && !(error instanceof ApiError && error.code === "network_error")) untrackOperation(id);
          throw error;
        })
        .finally(release);
    }),
    onSuccess: (started, scope) => {
      if (!scope.isCurrent()) return;
      queryClient.setQueryData(migrationQuery(started.id).queryKey, started);
      preview.reset();
    },
    onError: (error, scope) => {
      if (!scope.isCurrent()) return;
      if (!(error instanceof ApiError && error.code === "network_error")) update({ migration: null });
    },
    onSettled: (_result, _error, scope) => { if (scope.isCurrent()) setConfirm(false); },
  });
  const onSubmit = (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault();
    setTouched(true);
    if (Object.keys(validateDraft(draft, current.secretConfigured, false)).length === 0) preview.mutate(captureSessionScope());
  };
  const failed = (preview.variables?.isCurrent() ? preview.error : null) ?? (start.variables?.isCurrent() ? start.error : null);
  const prepared = preview.variables?.isCurrent() ? preview.data : undefined;
  // The destination is what gets typed to confirm: its bucket, or the local disk's name.
  const target = draft.driver === "s3" ? draft.bucket.trim() : t("storage.local");

  return (
    <>
      <form className="storage-block" onSubmit={onSubmit} noValidate aria-label={t("storage.migration.target")}>
        <h2 className="subtitle">{t("storage.migration.target")}</h2>
        {locked && <Notice role="note">{t("storage.migration.running")}</Notice>}
        {failed && (
          <Notice tone="danger">{t(storageErrorKey(failed, "migration"))}</Notice>
        )}
        <fieldset className="form" disabled={locked || start.isPending}>
          <StorageFields draft={draft} errors={errors} secretConfigured={current.secretConfigured} secretHint={t("storage.migration.secretHint")} onChange={change} />
        </fieldset>
        <footer className="actions">
          {prepared && <span className="storage-preview mono">{t("storage.migration.previewResult", { count: prepared.totalObjects })}</span>}
          <Button type="submit" variant="secondary" disabled={locked || start.isPending} loading={preview.isPending}>{t("storage.migration.preview")}</Button>
          <Button disabled={locked || !prepared} loading={start.isPending} onClick={() => setConfirm(true)}>{t("storage.migration.start")}</Button>
        </footer>
      </form>
      {/* Outside the form: Enter in the typed field must not also submit a preview. */}
      <ConfirmDialog open={confirm} danger title={t("storage.migration.startTitle")} confirmLabel={t("storage.migration.start")} busy={start.isPending}
        typed={{ phrase: target, label: t("common.typeToConfirm", { name: target }) }} onConfirm={() => start.mutate(captureSessionScope())} onCancel={() => setConfirm(false)}>
        <ul className="storage-steps">
          <li>{t("storage.migration.startStep1", { count: prepared?.totalObjects ?? 0, target: describeSettings(t, destinationOf(draft)) })}</li>
          <li>{t("storage.migration.startStep2")}</li>
          <li>{t("storage.migration.startStep3")}</li>
          <li>{t("storage.migration.startStep4")}</li>
        </ul>
      </ConfirmDialog>
    </>
  );
}
