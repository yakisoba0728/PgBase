import { useEffect, useRef } from "react";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { RiCloseLine, RiRefreshLine } from "@remixicon/react";
import { ApiError } from "../api/http";
import { findOperation, useOperations } from "../app/operations";
import { errorText } from "../app/queries";
import { IconButton } from "../components/Button";
import { JobProgress } from "../components/JobProgress";
import { toast } from "../components/Toast";
import { useT } from "../i18n";
import { useTimeFormat } from "../app/format";
import { useSearchState } from "../app/search";
import { describePhase } from "./model";
import { backupJobQuery, backupsQuery, runningBackupJobsQuery, useBackupsConsole } from "./queries";

/** The job to show: the one in the URL, else one this browser started and has not seen finish, else one the server is running. */
export function useShownJob(): string | null {
  const { params } = useSearchState();
  useOperations();
  const { listed } = useBackupsConsole();
  const running = useQuery({ ...runningBackupJobsQuery, enabled: listed });
  return params.get("job") ?? (findOperation("backup") ?? findOperation("restore"))?.id ?? running.data?.[0]?.id ?? null;
}

/** True while a job this browser knows of has not finished; the server runs one backup operation at a time. */
export function useJobBusy(): boolean {
  const id = useShownJob();
  const job = useQuery({ ...backupJobQuery(id ?? ""), enabled: id !== null });
  return id !== null && job.data?.status !== "completed" && job.data?.status !== "failed" && !job.isError;
}

/** A backup job followed to completion; its id (URL or tracked) lets a reload re-attach. The navigation reports the outcome. */
export function BackupJobStrip({ id }: { id: string }) {
  const t = useT();
  const time = useTimeFormat();
  const queryClient = useQueryClient();
  const { params, update } = useSearchState();
  const job = useQuery(backupJobQuery(id));
  const status = job.data?.status;
  const notFound = job.error instanceof ApiError && job.error.status === 404;
  const settled = useRef(status !== "running" && status !== undefined);

  useEffect(() => {
    if (notFound) {
      toast(t("storage.job.gone"), "danger");
      update({ job: null });
    }
  }, [notFound, t, update]);

  useEffect(() => {
    if (!job.data || job.data.status === "running" || settled.current) return;
    settled.current = true;
    void queryClient.invalidateQueries({ queryKey: backupsQuery.queryKey });
    void queryClient.invalidateQueries({ queryKey: runningBackupJobsQuery.queryKey });
  }, [job.data, queryClient]);

  const data = job.data;
  const phase = data ? describePhase(data.phase) : undefined;
  const title = data ? t(data.operation === "restore" ? "storage.job.restore" : "storage.job.create") : t("storage.job.running");
  const state = status === "failed" ? "failed" : status === "completed" ? "done" : "running";
  return (
    <div className="storage-job">
      <JobProgress
        title={title}
        state={state}
        detail={phase ? (phase.key ? t(phase.key, phase.vars) : phase.raw) : t("storage.job.waiting")}
        meta={data && `${data.backupId} · ${time(data.updatedAt)}`}
        done={phase?.vars?.done}
        total={phase?.vars?.total}
        error={data?.error ?? (job.error ? errorText(t, job.error) : undefined)}
        action={job.error ? <IconButton label={t("common.retry")} icon={RiRefreshLine} onClick={() => void job.refetch()} /> : data && status !== "running" && params.has("job") && <IconButton label={t("storage.job.dismiss")} icon={RiCloseLine} onClick={() => update({ job: null })} />}
      />
    </div>
  );
}
