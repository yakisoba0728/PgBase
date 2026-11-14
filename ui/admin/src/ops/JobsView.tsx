import { useState, type FormEvent } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { trackOperation } from "../app/operations";
import { Facts } from "../components/Facts";
import { UnsavedGuard } from "../components/UnsavedGuard";
import { openRow } from "../components/DataGrid";
import { RiPlayLine, RiTimerLine } from "@remixicon/react";
import { ApiError } from "../api/http";
import { runJob, updateJob, type ManagedJob } from "../api/jobs";
import { errorText } from "../app/queries";
import { Button, RefreshButton } from "../components/Button";
import { EmptyState, ErrorState, Skeleton, Notice } from "../components/Feedback";
import { Checkbox, Field } from "../components/Field";
import { SaveBar } from "../components/SaveBar";
import { SplitView, PaneHeader } from "../components/SplitView";
import { toast } from "../components/Toast";
import { useT, type Translate } from "../i18n";
import { describeSchedule } from "./format";
import { jobsQuery } from "./queries";
import { useTimeFormat } from "../app/format";
import { useSearchState } from "../app/search";
import "../styles/ops.css";

// Not every engine lists zones; the input stays free text either way.
const zones: string[] = (() => {
  try {
    return ["UTC", ...Intl.supportedValuesOf("timeZone")];
  } catch {
    return ["UTC"];
  }
})();

function ScheduleText({ schedule }: { schedule: string }) {
  const t = useT();
  const summary = describeSchedule(schedule);
  return (
    <>
      <span className="mono">{schedule}</span>
      {summary && <span className="txt-hint ops-aside">{t(summary.key, summary.vars)}</span>}
    </>
  );
}

function stateOf(t: Translate, job: ManagedJob): { label: string; tone?: string } {
  if (job.running) return { label: t("ops.jobs.running"), tone: "accent" };
  if (!job.registered) return { label: t("ops.jobs.unregistered"), tone: "warning" };
  if (!job.enabled) return { label: t("ops.jobs.disabled") };
  return { label: t("ops.jobs.enabled"), tone: "success" };
}

export function JobsActions() {
  return <RefreshButton queryKey={jobsQuery.queryKey} />;
}

/** Scheduled jobs with their last and next run; the pane edits schedule, zone and enabled state. */
export function JobsView() {
  const t = useT();
  const time = useTimeFormat();
  const { params, update } = useSearchState();
  const jobs = useQuery(jobsQuery);
  const items = jobs.data?.items ?? [];
  const openId = params.get("job");
  const open = items.find((job) => job.id === openId);
  const close = () => update({ job: null });

  const list = (
    <div className="grid-scroll ops-top">
      {jobs.isPending ? (
        <Skeleton />
      ) : jobs.isError ? (
        <ErrorState error={jobs.error} onRetry={jobs.refetch} />
      ) : items.length === 0 ? (
        <EmptyState icon={RiTimerLine}>{t("ops.jobs.empty")}</EmptyState>
      ) : (
        <>
          {!jobs.data?.workerEnabled && <p className="warning-note">{t("ops.jobs.noWorker")}</p>}
          <table className="grid" aria-label={t("nav.jobs")}>
            <thead>
              <tr>
                <th>{t("ops.jobs.name")}</th>
                <th>{t("ops.jobs.schedule")}</th>
                <th>{t("ops.jobs.timezone")}</th>
                <th>{t("ops.jobs.state")}</th>
                <th>{t("ops.jobs.lastRun")}</th>
                <th>{t("ops.jobs.nextRun")}</th>
              </tr>
            </thead>
            <tbody>
              {items.map((job) => {
                const state = stateOf(t, job);
                return (
                  <tr
                    key={job.id}
                    {...openRow(() => update({ job: job.id }), openId === job.id)}
                  >
                    <td className="mono">{job.name}</td>
                    <td><ScheduleText schedule={job.schedule} /></td>
                    <td className="mono">{job.timezone}</td>
                    <td><span className="dot" data-tone={state.tone}>{state.label}</span></td>
                    <td>
                      {job.lastStartedAt ? (
                        <>
                          <span className="mono">{time(job.lastStartedAt)}</span>
                          <span className="ops-aside" data-tone={job.failed ? "danger" : undefined}>
                            {job.failed ? t("ops.jobs.failed", { count: job.attempts }) : job.lastFinishedAt ? t("ops.jobs.ok") : ""}
                          </span>
                        </>
                      ) : (
                        <span className="na">{t("ops.jobs.never")}</span>
                      )}
                    </td>
                    <td className="mono">{job.enabled ? time(job.nextRunAt) : <span className="na">–</span>}</td>
                  </tr>
                );
              })}
            </tbody>
          </table>
        </>
      )}
    </div>
  );

  return (
    <SplitView
      list={list}
      paneLabel={t("ops.jobs.detail")}
      onClose={close}
      pane={open ? <JobPane key={open.id} job={open} workerEnabled={jobs.data?.workerEnabled ?? false} onClose={close} /> : null}
    />
  );
}

function JobPane({ job, workerEnabled, onClose }: { job: ManagedJob; workerEnabled: boolean; onClose: () => void }) {
  const t = useT();
  const time = useTimeFormat();
  const queryClient = useQueryClient();
  const [schedule, setSchedule] = useState(job.schedule);
  const [timezone, setTimezone] = useState(job.timezone);
  const [enabled, setEnabled] = useState(job.enabled);
  const dirty = schedule !== job.schedule || timezone !== job.timezone || enabled !== job.enabled;
  const refresh = () => queryClient.invalidateQueries({ queryKey: jobsQuery.queryKey });

  const reset = () => {
    setSchedule(job.schedule);
    setTimezone(job.timezone);
    setEnabled(job.enabled);
  };
  const save = useMutation({
    mutationFn: () => updateJob(job.id, { schedule: schedule.trim(), timezone: timezone.trim(), enabled }),
    onSuccess: () => { setSchedule(schedule.trim()); setTimezone(timezone.trim() || "UTC"); toast(t("ops.jobs.saved")); },
    onSettled: refresh,
  });
  const run = useMutation({
    mutationFn: () => runJob(job.id),
    onSuccess: () => {
      toast(t("ops.jobs.runRequested", { name: job.name }));
      trackOperation({ kind: "job", id: job.id, subject: job.name, since: job.lastStartedAt });
    },
    onError: (error) => toast(errorText(t, error), "danger"),
    onSettled: refresh,
  });
  const onSubmit = (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault();
    save.mutate();
  };
  const summary = describeSchedule(schedule);
  const locked = !workerEnabled || !job.registered;

  return (
    <form className="pane-form" onSubmit={onSubmit}>
      <PaneHeader title={job.name} mono onClose={onClose} />
      <div className="pane-body">
        {locked && <p className="warning-note">{workerEnabled ? t("ops.jobs.unregisteredNote") : t("ops.jobs.noWorker")}</p>}
        {save.isError && (
          <Notice tone="danger">{save.error instanceof ApiError && save.error.code === "invalid_request" ? t("ops.jobs.invalid") : errorText(t, save.error)}</Notice>
        )}
        <fieldset className="form" disabled={locked}>
          <Field label={t("ops.jobs.schedule")} mono spellCheck={false} autoComplete="off" required value={schedule} onChange={(event) => setSchedule(event.target.value)}
            hint={summary ? t(summary.key, summary.vars) : t("ops.jobs.scheduleHint")} />
          <Field label={t("ops.jobs.timezone")} mono spellCheck={false} autoComplete="off" list="ops-zones" placeholder="UTC" value={timezone} onChange={(event) => setTimezone(event.target.value)} />
          <datalist id="ops-zones">
            {zones.map((zone) => (
              <option key={zone} value={zone} />
            ))}
          </datalist>
          <Checkbox label={t("ops.jobs.enabledLabel")} checked={enabled} onChange={(event) => setEnabled(event.target.checked)} />
        </fieldset>
        <Facts items={[
          { label: t("ops.jobs.state"), value: <span className="dot" data-tone={stateOf(t, job).tone}>{stateOf(t, job).label}</span> },
          { label: t("ops.jobs.nextRun"), value: job.enabled ? time(job.nextRunAt, true) : null },
          { label: t("ops.jobs.lastStarted"), value: job.lastStartedAt ? time(job.lastStartedAt, true) : <span className="na">{t("ops.jobs.never")}</span> },
          { label: t("ops.jobs.lastFinished"), value: job.lastFinishedAt ? time(job.lastFinishedAt, true) : null },
          { label: t("ops.jobs.result"), value: job.failed ? t("ops.jobs.failed", { count: job.attempts }) : job.lastFinishedAt ? t("ops.jobs.ok") : null, tone: job.failed ? "danger" : undefined },
          { label: t("ops.jobs.id"), value: job.id, mono: true, copy: true },
        ]} />
      </div>
      <footer className="pane-footer">
        <Button variant="secondary" icon={RiPlayLine} className="pane-footer-start" disabled={locked || job.running || !job.enabled} loading={run.isPending} onClick={() => run.mutate()}>{t("ops.jobs.run")}</Button>
        <SaveBar dirty={dirty} busy={save.isPending} saveDisabled={locked || !dirty} onDiscard={reset} />
      </footer>
      <UnsavedGuard dirty={dirty} />
    </form>
  );
}
