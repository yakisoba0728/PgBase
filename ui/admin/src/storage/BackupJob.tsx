import { useEffect, useRef } from "react";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { RiCloseLine } from "@remixicon/react";
import { ApiError } from "../api/http";
import { IconButton } from "../components/Button";
import { toast } from "../components/Toast";
import { useT } from "../i18n";
import { useParams, useTimeFormat } from "../ops/queries";
import { describePhase } from "./model";
import { backupJobQuery, backupsQuery } from "./queries";

/** True while the job in the URL has not finished; the server runs one backup operation at a time. */
export function useJobBusy(): boolean {
  const { params } = useParams();
  const id = params.get("job");
  const job = useQuery({ ...backupJobQuery(id ?? ""), enabled: id !== null });
  return id !== null && job.data?.status !== "completed" && job.data?.status !== "failed" && !job.isError;
}

/** The backup job named by ?job=, followed to completion; the id in the URL lets a reload re-attach. */
export function BackupJobStrip({ id }: { id: string }) {
  const t = useT();
  const time = useTimeFormat();
  const queryClient = useQueryClient();
  const { update } = useParams();
  const job = useQuery(backupJobQuery(id));
  const status = job.data?.status;
  const notFound = job.error instanceof ApiError && job.error.status === 404;
  const announced = useRef(status !== "running" && status !== undefined);

  useEffect(() => {
    if (notFound) {
      toast(t("storage.job.gone"), "danger");
      update({ job: null });
    }
  }, [notFound, t, update]);

  useEffect(() => {
    if (!job.data || job.data.status === "running" || announced.current) return;
    announced.current = true;
    void queryClient.invalidateQueries({ queryKey: backupsQuery.queryKey });
    if (job.data.status === "completed") toast(t(job.data.operation === "restore" ? "storage.job.restoreDone" : "storage.job.createDone"));
    // A restore swaps the whole project: every cached screen is stale.
    if (job.data.status === "completed" && job.data.operation === "restore") void queryClient.invalidateQueries();
  }, [job.data, queryClient, t]);

  const data = job.data;
  const phase = data ? describePhase(data.phase) : undefined;
  const tone = status === "failed" ? "danger" : status === "completed" ? "success" : "accent";
  return (
    <section className="storage-job" data-tone={tone} aria-live="polite" aria-label={t(data?.operation === "restore" ? "storage.job.restore" : "storage.job.create")}>
      {status === "running" || !data ? <span className="loader" aria-hidden /> : <span className="ops-dot" />}
      <div className="storage-job-text">
        <strong>{data ? t(data.operation === "restore" ? "storage.job.restore" : "storage.job.create") : t("storage.job.running")}</strong>
        <span>{phase ? (phase.key ? t(phase.key, phase.vars) : phase.raw) : t("storage.job.waiting")}</span>
        {data?.error && <span className="storage-job-error">{data.error}</span>}
        {data && <span className="txt-hint mono">{data.backupId} · {time(data.updatedAt)}</span>}
      </div>
      {phase?.ratio !== undefined && <progress className="storage-progress" value={phase.ratio} max={1} />}
      {data && status !== "running" && <IconButton label={t("storage.job.dismiss")} icon={RiCloseLine} onClick={() => update({ job: null })} />}
    </section>
  );
}
