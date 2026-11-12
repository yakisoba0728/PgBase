import { useRef, useState, type ChangeEvent } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { RiAddLine, RiArchive2Line, RiCloseLine, RiDeleteBinLine, RiDownload2Line, RiHistoryLine, RiRefreshLine, RiUpload2Line } from "@remixicon/react";
import { backupDownloadPath, createBackup, deleteBackup, restoreBackup, uploadBackup, type BackupEntry } from "../api/backups";
import type { BackupStorageSettings } from "../api/storage";
import { errorText } from "../app/queries";
import { Button, IconButton } from "../components/Button";
import { ConfirmDialog } from "../components/Dialog";
import { EmptyState, ErrorState, Loader } from "../components/Feedback";
import { Checkbox, Field } from "../components/Field";
import { SplitView } from "../components/SplitView";
import { toast } from "../components/Toast";
import { useT, type Translate } from "../i18n";
import { useTimeFormat } from "../ops/queries";
import { useSearchState } from "../app/search";
import { Link } from "react-router";
import { trackOperation } from "../app/operations";
import { settingsQuery } from "../system/queries";
import { BackupJobStrip, useJobBusy, useShownJob } from "./BackupJob";
import { formatBytes } from "./model";
import { backupJobQuery, backupStorageQuery, backupsQuery } from "./queries";
import "../styles/ops.css";
import "../styles/storage.css";

function locationText(t: Translate, settings: BackupStorageSettings | undefined): string {
  if (!settings) return "–";
  if (settings.driver === "local") return t("storage.local");
  return `s3://${settings.bucket}${settings.prefix ? `/${settings.prefix}` : ""}`;
}

/** Header actions: start a backup or upload an archive; a started job goes into the URL. */
export function BackupsActions() {
  const t = useT();
  const queryClient = useQueryClient();
  const { update } = useSearchState();
  const input = useRef<HTMLInputElement>(null);
  const busy = useJobBusy();

  const create = useMutation({
    mutationFn: createBackup,
    onSuccess: (started) => {
      queryClient.setQueryData(backupJobQuery(started.id).queryKey, started);
      trackOperation({ kind: "backup", id: started.id, subject: started.backupId });
      update({ job: started.id });
    },
    onError: (error) => toast(errorText(t, error), "danger"),
  });
  const upload = useMutation({
    mutationFn: (file: File) => uploadBackup(file),
    onSuccess: (entry) => {
      toast(t("storage.backups.uploaded"));
      void queryClient.invalidateQueries({ queryKey: backupsQuery.queryKey });
      update({ backup: entry.id });
    },
    onError: (error) => toast(errorText(t, error), "danger"),
  });
  const onFile = (event: ChangeEvent<HTMLInputElement>) => {
    const file = event.target.files?.[0];
    event.target.value = "";
    if (!file) return;
    if (!/\.(tar\.gz|tgz)$/i.test(file.name)) toast(t("storage.backups.uploadOnly"), "danger");
    else upload.mutate(file);
  };

  return (
    <>
      <IconButton label={t("storage.refresh")} icon={RiRefreshLine} onClick={() => queryClient.invalidateQueries({ queryKey: backupsQuery.queryKey })} />
      <input ref={input} type="file" accept=".tar.gz,.tgz,application/gzip" hidden onChange={onFile} />
      <Button variant="secondary" icon={RiUpload2Line} loading={upload.isPending} disabled={busy} onClick={() => input.current?.click()}>{t("storage.backups.upload")}</Button>
      <Button icon={RiAddLine} loading={create.isPending} disabled={busy} onClick={() => create.mutate()}>{t("storage.backups.create")}</Button>
    </>
  );
}

/** Archives in the backup repository; the pane downloads, restores or deletes one. */
export function BackupsView() {
  const t = useT();
  const time = useTimeFormat();
  const { params, update } = useSearchState();
  const backups = useQuery(backupsQuery);
  const storage = useQuery(backupStorageQuery);
  const items = backups.data ?? [];
  const openId = params.get("backup");
  const open = items.find((item) => item.id === openId);
  const jobId = useShownJob();
  const busy = useJobBusy();
  const close = () => update({ backup: null });
  const location = locationText(t, storage.data);

  const list = (
    <div className="grid-scroll">
      <BackupSchedule />
      {jobId &&<BackupJobStrip key={jobId} id={jobId} />}
      {backups.isPending ? (
        <Loader />
      ) : backups.isError ? (
        <ErrorState error={backups.error} onRetry={backups.refetch} />
      ) : items.length === 0 ? (
        <EmptyState icon={RiArchive2Line}>{t("storage.backups.empty")}</EmptyState>
      ) : (
        <table className="grid" aria-label={t("nav.backups")}>
          <thead>
            <tr>
              <th>{t("storage.backups.id")}</th>
              <th>{t("storage.backups.created")}</th>
              <th className="num">{t("storage.backups.size")}</th>
              <th>{t("storage.backups.postgres")}</th>
              <th>{t("storage.backups.location")}</th>
            </tr>
          </thead>
          <tbody>
            {items.map((item) => (
              <tr key={item.id} tabIndex={0} aria-current={openId === item.id ? "true" : undefined} onClick={() => update({ backup: item.id })}
                onKeyDown={(event) => { if (event.key === "Enter") update({ backup: item.id }); }}>
                <td className="mono">{item.id}</td>
                <td className="mono">{time(item.createdAt)}</td>
                <td className="num mono">{formatBytes(item.size)}</td>
                <td className="mono">{item.postgresqlMajor}</td>
                <td className={storage.data?.driver === "s3" ? "mono" : undefined}>{location}</td>
              </tr>
            ))}
          </tbody>
        </table>
      )}
    </div>
  );

  return <SplitView list={list} paneLabel={t("storage.backups.detail")} onClose={close} pane={open ? <BackupPane key={open.id} backup={open} location={location} jobRunning={busy} onClose={close} /> : null} />;
}

/** The automatic-backup policy lives in Settings; shown here so the archive count makes sense. */
function BackupSchedule() {
  const t = useT();
  const settings = useQuery({ ...settingsQuery, refetchOnMount: true, staleTime: 60_000 });
  const backups = settings.data?.backups;
  if (!backups) return null;
  const keep = backups.keep === 0 ? t("storage.backups.keepAll") : backups.keep;
  return (
    <p className="storage-schedule">
      <span className="txt-hint">{t("storage.backups.schedule")}</span>
      <span className="dot" data-tone={backups.enabled ? "success" : undefined}>
        {backups.enabled ? t("storage.backups.scheduleOn", { schedule: backups.schedule, timezone: backups.timezone, keep }) : t("storage.backups.scheduleOff")}
      </span>
      <Link to="/settings">{t("storage.backups.scheduleEdit")}</Link>
    </p>
  );
}

function BackupPane({ backup, location, jobRunning, onClose }: { backup: BackupEntry; location: string; jobRunning: boolean; onClose: () => void }) {
  const t = useT();
  const time = useTimeFormat();
  const queryClient = useQueryClient();
  const { update } = useSearchState();
  const [dialog, setDialog] = useState<"delete" | "restore" | null>(null);
  const [typed, setTyped] = useState("");
  const [acknowledged, setAcknowledged] = useState(false);

  const remove = useMutation({
    mutationFn: () => deleteBackup(backup.id),
    onSuccess: () => {
      toast(t("storage.backups.deleted"));
      setDialog(null);
      update({ backup: null });
      void queryClient.invalidateQueries({ queryKey: backupsQuery.queryKey });
    },
    onError: (error) => toast(errorText(t, error), "danger"),
  });
  const restore = useMutation({
    mutationFn: () => restoreBackup(backup.id),
    onSuccess: (started) => {
      queryClient.setQueryData(backupJobQuery(started.id).queryKey, started);
      trackOperation({ kind: "restore", id: started.id, subject: started.backupId });
      setDialog(null);
      update({ job: started.id, backup: null });
    },
    onError: (error) => toast(errorText(t, error), "danger"),
  });
  const openDialog = (kind: "delete" | "restore") => {
    setTyped("");
    setAcknowledged(false);
    setDialog(kind);
  };
  const created = time(backup.createdAt, true);

  return (
    <div className="ops-pane-form">
      <header className="pane-header">
        <h2 className="pane-title mono">{backup.id}</h2>
        <IconButton label={t("common.close")} icon={RiCloseLine} onClick={onClose} />
      </header>
      <div className="pane-body">
        <dl className="ops-props">
          <div><dt>{t("storage.backups.created")}</dt><dd>{created}</dd></div>
          <div><dt>{t("storage.backups.size")}</dt><dd className="mono">{formatBytes(backup.size)} <span className="txt-hint">({backup.size.toLocaleString()} B)</span></dd></div>
          <div><dt>{t("storage.backups.postgres")}</dt><dd className="mono">{backup.postgresqlMajor}</dd></div>
          <div><dt>{t("storage.backups.location")}</dt><dd>{location}</dd></div>
          <div><dt>{t("storage.backups.format")}</dt><dd className="mono">{backup.storageFormat ?? <span className="na">–</span>}</dd></div>
          <div><dt>{t("storage.backups.sha256")}</dt><dd className="mono">{backup.sha256 ?? <span className="na">–</span>}</dd></div>
        </dl>
      </div>
      <footer className="pane-footer">
        <Button variant="secondary" icon={RiDeleteBinLine} className="pane-footer-start" disabled={jobRunning} onClick={() => openDialog("delete")}>{t("storage.backups.delete")}</Button>
        <a className="btn btn-secondary" href={backupDownloadPath(backup.id)} download={`pgbase-${backup.id}.tar.gz`}>
          <RiDownload2Line size={16} aria-hidden />
          {t("storage.backups.download")}
        </a>
        <Button variant="secondary" icon={RiHistoryLine} disabled={jobRunning} onClick={() => openDialog("restore")}>{t("storage.backups.restore")}</Button>
      </footer>
      <ConfirmDialog open={dialog === "delete"} danger title={t("storage.backups.deleteTitle")} message={t("storage.backups.deleteText")} confirmLabel={t("storage.backups.delete")}
        busy={remove.isPending} confirmDisabled={typed.trim() !== backup.id} onConfirm={() => remove.mutate()} onCancel={() => setDialog(null)}>
        <Field label={t("storage.backups.typeId")} mono autoComplete="off" spellCheck={false} placeholder={backup.id} value={typed} onChange={(event) => setTyped(event.target.value)} />
      </ConfirmDialog>
      <ConfirmDialog open={dialog === "restore"} danger title={t("storage.backups.restoreTitle")} message={t("storage.backups.restoreLead")} confirmLabel={t("storage.backups.restore")}
        busy={restore.isPending} confirmDisabled={!acknowledged} onConfirm={() => restore.mutate()} onCancel={() => setDialog(null)}>
        <ul className="storage-steps">
          <li>{t("storage.backups.restoreStep1")}</li>
          <li>{t("storage.backups.restoreStep2", { time: created })}</li>
          <li>{t("storage.backups.restoreStep3")}</li>
          <li>{t("storage.backups.restoreStep4")}</li>
          <li>{t("storage.backups.restoreKeep")}</li>
        </ul>
        <p className="mono storage-steps-id">{backup.id}</p>
        <Checkbox label={t("storage.backups.restoreAck")} checked={acknowledged} onChange={(event) => setAcknowledged(event.target.checked)} />
      </ConfirmDialog>
    </div>
  );
}
