import { useEffect, useRef, useState } from "react";
import { JobProgress } from "../components/JobProgress";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Facts } from "../components/Facts";
import { RiAddLine, RiArchive2Line, RiDownload2Line, RiErrorWarningLine, RiHistoryLine, RiUpload2Line } from "@remixicon/react";
import { backupDownloadPath, createBackup, deleteBackup, restoreBackup, uploadBackup, type BackupEntry } from "../api/backups";
import type { BackupStorageSettings } from "../api/storage";
import { errorText } from "../app/queries";
import { Button, FileButton, RefreshButton } from "../components/Button";
import { ConfirmDialog } from "../components/Dialog";
import { EmptyState, Skeleton, Unavailable } from "../components/Feedback";
import { QueryView } from "../components/QueryView";
import { SplitView, PaneHeader } from "../components/SplitView";
import { Row, Table, TableScroll } from "../components/Table";
import { Searchbar, Select } from "../components/Field";
import { toast } from "../components/Toast";
import { useT, type Translate } from "../i18n";
import { useTimeFormat } from "../app/format";
import { useSearchState } from "../app/search";
import { Link } from "react-router";
import { trackOperation } from "../app/operations";
import { settingsQuery } from "../system/queries";
import { BackupJobStrip, useJobBusy, useShownJob } from "./BackupJob";
import { formatBytes } from "../app/format";
import { backupJobQuery, backupStorageQuery, backupsQuery, useBackupsConsole } from "./queries";
import "../styles/storage.css";

const offTextId = "backups-off-text";

/** The archive's file name, as downloaded; typed to confirm a delete or restore. */
const fileName = (backup: BackupEntry) => `pgbase-${backup.id}.tar.gz`;

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
  const busy = useJobBusy();
  // Kept in place, disabled, pointing at the screen's explanation of why.
  const { off } = useBackupsConsole();
  const reason = off ? { title: t("storage.backups.offTitle"), "aria-describedby": offTextId } : {};

  const create = useMutation({
    mutationFn: createBackup,
    onSuccess: (started) => {
      queryClient.setQueryData(backupJobQuery(started.id).queryKey, started);
      trackOperation({ kind: "backup", id: started.id, subject: started.backupId });
      update({ job: started.id });
    },
    onError: (error) => toast(errorText(t, error), "danger"),
  });
  const abort = useRef<AbortController | null>(null);
  const [fraction, setFraction] = useState(0);
  const upload = useMutation({
    mutationFn: (file: File) => {
      abort.current = new AbortController();
      setFraction(0);
      return uploadBackup(file, abort.current.signal, setFraction);
    },
    onSuccess: (entry) => {
      toast(t("storage.backups.uploaded"));
      update({ backup: entry.id });
    },
    onError: (error) => (abort.current?.signal.aborted ? toast(t("storage.backups.uploadCanceled")) : toast(errorText(t, error), "danger")),
    // A cancel or failure after the body was sent may still have stored the archive: list what the server has.
    onSettled: () => void queryClient.invalidateQueries({ queryKey: backupsQuery.queryKey }),
  });
  const onFile = ([file]: File[]) => {
    if (!file) return;
    if (!/\.(tar\.gz|tgz)$/i.test(file.name)) toast(t("storage.backups.uploadOnly"), "danger");
    else upload.mutate(file);
  };

  return (
    <>
      <RefreshButton queryKey={backupsQuery.queryKey} />
      {upload.isPending && upload.variables ? (
        <JobProgress
          title={t("storage.backups.upload")}
          state="running"
          detail={t("storage.backups.uploading", { name: upload.variables.name, size: formatBytes(upload.variables.size) })}
          done={fraction}
          total={1}
          action={<Button size="sm" variant="secondary" onClick={() => abort.current?.abort()}>{t("common.cancel")}</Button>}
        />
      ) : (
        <FileButton size="sm" icon={RiUpload2Line} accept=".tar.gz,.tgz,application/gzip" disabled={busy || off} {...reason} onFiles={onFile}>{t("storage.backups.upload")}</FileButton>
      )}
      <Button size="sm" icon={RiAddLine} data-new loading={create.isPending} disabled={busy || off} {...reason} onClick={() => create.mutate()}>{t("storage.backups.create")}</Button>
    </>
  );
}

/** Archives in the backup repository; the pane downloads, restores or deletes one. */
export function BackupsView() {
  const t = useT();
  const time = useTimeFormat();
  const { params, update } = useSearchState();
  const backups = useQuery(backupsQuery);
  const { off, listed } = useBackupsConsole();
  const storage = useQuery({ ...backupStorageQuery, enabled: listed });
  const openId = params.get("backup");
  const open = backups.data?.find((item) => item.id === openId);
  const jobId = useShownJob();
  const busy = useJobBusy();
  const close = () => update({ backup: null });
  const location = locationText(t, storage.data);
  const q = params.get("q") ?? "";
  const oldest = params.get("sort") === "oldest";
  const needle = q.trim().toLowerCase();
  // A copy: the cached array stays in server order. Ties keep that order (Array.sort is stable).
  const sorted = (items: BackupEntry[]) =>
    items.filter((item) => !needle || item.id.toLowerCase().includes(needle) || fileName(item).includes(needle))
      .sort((a, b) => (Date.parse(b.createdAt) - Date.parse(a.createdAt)) * (oldest ? -1 : 1));

  if (off) {
    return (
      <div className="page-content">
        <Unavailable icon={RiArchive2Line} title={t("storage.backups.offTitle")} setting="PGBASE_BACKUPS_DIRECTORY" how={t("storage.backups.offHow")} textId={offTextId}>{t("storage.backups.offText")}</Unavailable>
      </div>
    );
  }

  const list = (
    <>
      <div className="toolbar">
        <FilterParam label={t("storage.backups.filter")} />
        <Select size="sm" className="control-fit" aria-label={t("storage.backups.sort")} value={oldest ? "oldest" : ""} onChange={(event) => update({ sort: event.target.value || null }, { replace: true })}>
          <option value="">{t("storage.backups.newest")}</option>
          <option value="oldest">{t("storage.backups.oldest")}</option>
        </Select>
      </div>
      <TableScroll>
        <BackupSchedule />
        {jobId && <BackupJobStrip key={jobId} id={jobId} />}
        <QueryView query={backups} empty={<EmptyState icon={RiArchive2Line}>{t("storage.backups.empty")}</EmptyState>}>
          {(items) => (
            <Table label={t("nav.backups")}>
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
                {sorted(items).length === 0 && <tr><td colSpan={5} className="txt-hint">{t("storage.backups.noMatch")}</td></tr>}
                {sorted(items).map((item) => (
                  <Row key={item.id} current={openId === item.id} onOpen={() => update({ backup: item.id })}>
                    <td className="mono">{item.id}</td>
                    <td className="mono">{time(item.createdAt)}</td>
                    <td className="num">{formatBytes(item.size)}</td>
                    <td className="mono">{item.postgresqlMajor}</td>
                    <td className={storage.data?.driver === "s3" ? "mono" : undefined}>{location}</td>
                  </Row>
                ))}
              </tbody>
            </Table>
          )}
        </QueryView>
      </TableScroll>
    </>
  );

  let pane = null;
  if (open) pane = <BackupPane key={open.id} backup={open} location={location} jobRunning={busy} onClose={close} />;
  // The full list is loaded, so an archive missing from it once it settles does not exist (an upload arrives with the refetch).
  else if (openId && backups.data && !backups.isFetching) pane = <EmptyState icon={RiErrorWarningLine}>{t("common.notFound")}</EmptyState>;
  else if (openId && !backups.isError) pane = <Skeleton variant="form" />;
  return <SplitView list={list} paneLabel={t("storage.backups.detail")} onClose={close} pane={pane} />;
}

/** The automatic-backup policy lives in Settings; shown here so the archive count makes sense. */
function BackupSchedule() {
  const t = useT();
  const settings = useQuery({ ...settingsQuery, refetchOnMount: true, staleTime: 60_000 });
  const backups = settings.data?.backups;
  if (!backups) return null;
  const keep = backups.keep === 0 ? t("storage.backups.keepStorage") : backups.keep;
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
  const name = fileName(backup);
  const typed = { phrase: name, label: t("common.typeToConfirm", { name }) };
  const created = time(backup.createdAt, true);

  return (
    <div className="pane-form">
      <PaneHeader title={backup.id} mono copy={{ label: t("storage.backups.id"), value: backup.id }} onClose={onClose} />
      <div className="pane-body">
        <Facts items={[
          { label: t("storage.backups.created"), value: created },
          { label: t("storage.backups.size"), value: <>{formatBytes(backup.size)} <span className="txt-hint">({backup.size.toLocaleString()} B)</span></>, mono: true },
          { label: t("storage.backups.postgres"), value: backup.postgresqlMajor, mono: true },
          { label: t("storage.backups.location"), value: location },
          { label: t("storage.backups.format"), value: backup.storageFormat, mono: true },
          { label: t("storage.backups.sha256"), value: backup.sha256, mono: true, copy: true },
        ]} />
      </div>
      <footer className="pane-footer">
        <Button variant="danger" className="pane-footer-start" disabled={jobRunning} onClick={() => setDialog("delete")}>{t("storage.backups.delete")}</Button>
        <a className="btn btn-secondary" href={backupDownloadPath(backup.id)} download={name}>
          <RiDownload2Line size={16} aria-hidden />
          {t("storage.backups.download")}
        </a>
        <Button variant="secondary" icon={RiHistoryLine} disabled={jobRunning} onClick={() => setDialog("restore")}>{t("storage.backups.restore")}</Button>
      </footer>
      <ConfirmDialog open={dialog === "delete"} danger title={t("storage.backups.deleteTitle")} message={t("storage.backups.deleteText")} confirmLabel={t("storage.backups.delete")}
        busy={remove.isPending} typed={typed} onConfirm={() => remove.mutate()} onCancel={() => setDialog(null)} />
      <ConfirmDialog open={dialog === "restore"} danger title={t("storage.backups.restoreTitle")} message={t("storage.backups.restoreLead")} confirmLabel={t("storage.backups.restore")}
        busy={restore.isPending} typed={typed} onConfirm={() => restore.mutate()} onCancel={() => setDialog(null)}>
        <ul className="storage-steps">
          <li>{t("storage.backups.restoreStep1")}</li>
          <li>{t("storage.backups.restoreStep2", { time: created })}</li>
          <li>{t("storage.backups.restoreStep3")}</li>
          <li>{t("storage.backups.restoreStep4")}</li>
          <li>{t("storage.backups.restoreKeep")}</li>
        </ul>
      </ConfirmDialog>
    </div>
  );
}

/** The ?q= filter box: local text so typing is never held up by the URL, mirrored there without new history entries. */
function FilterParam({ label }: { label: string }) {
  const { params, update } = useSearchState();
  const applied = params.get("q") ?? "";
  const [text, setText] = useState(applied);
  useEffect(() => setText(applied), [applied]);
  return <Searchbar size="sm" label={label} primary value={text} onChange={(event) => { setText(event.target.value); update({ q: event.target.value || null }, { replace: true }); }} />;
}
