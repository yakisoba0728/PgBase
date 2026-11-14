import { useEffect, useState, type FormEvent } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { trackOperation } from "../app/operations";
import { Facts } from "../components/Facts";
import { UnsavedGuard } from "../components/UnsavedGuard";
import { Row, Table, TableScroll } from "../components/Table";
import { QueryView } from "../components/QueryView";
import { RiErrorWarningLine, RiPlayLine, RiTimerLine } from "@remixicon/react";
import { ApiError } from "../api/http";
import { runJob, updateJob, type ManagedJob } from "../api/jobs";
import { errorText } from "../app/queries";
import { Button, RefreshButton } from "../components/Button";
import { EmptyState, Notice, Skeleton } from "../components/Feedback";
import { Checkbox, Field, Searchbar, Select } from "../components/Field";
import { SaveBar } from "../components/SaveBar";
import { SplitView, PaneHeader } from "../components/SplitView";
import { toast } from "../components/Toast";
import { useT, type Translate } from "../i18n";
import { describeSchedule } from "./format";
import { jobsQuery } from "./queries";
import { useTimeFormat } from "../app/format";
import { useSearchState } from "../app/search";
import { useDraft } from "../auth/useDraft";
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

const states = ["running", "unregistered", "disabled", "enabled"] as const;
const stateKey = (job: ManagedJob): (typeof states)[number] => (job.running ? "running" : !job.registered ? "unregistered" : !job.enabled ? "disabled" : "enabled");
const tones = { running: "accent", unregistered: "warning", disabled: undefined, enabled: "success" };

function stateOf(t: Translate, job: ManagedJob): { label: string; tone?: string } {
  const key = stateKey(job);
  return { label: t(`ops.jobs.${key}`), tone: tones[key] };
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
  const q = params.get("q") ?? "";
  const state = params.get("state") ?? "";
  const needle = q.trim().toLowerCase();
  const shown = items.filter((job) => (!needle || job.name.toLowerCase().includes(needle) || job.id.toLowerCase().includes(needle)) && (!state || stateKey(job) === state));

  const list = (
    <>
      <div className="toolbar ops-filters">
        <FilterParam label={t("ops.jobs.filter")} />
        <Select size="sm" className="control-fit" aria-label={t("ops.jobs.state")} value={state} onChange={(event) => update({ state: event.target.value || null }, { replace: true })}>
          <option value="">{t("ops.jobs.anyState")}</option>
          {states.map((key) => <option key={key} value={key}>{t(`ops.jobs.${key}`)}</option>)}
        </Select>
        {(q || state) && <Button size="sm" variant="transparent" onClick={() => update({ q: null, state: null }, { replace: true })}>{t("ops.clearFilters")}</Button>}
      </div>
      <TableScroll className="ops-top">
        <QueryView query={jobs} empty={<EmptyState icon={RiTimerLine}>{t("ops.jobs.empty")}</EmptyState>} isEmpty={(data) => data.items.length === 0}>
          {(data) => (
            <>
              {!data.workerEnabled && <Notice role="note">{t("ops.jobs.noWorker")}</Notice>}
              <Table label={t("nav.jobs")}>
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
                  {shown.length === 0 && <tr><td colSpan={6} className="txt-hint">{t("ops.jobs.noMatch")}</td></tr>}
                  {shown.map((job) => {
                    const state = stateOf(t, job);
                    return (
                      <Row key={job.id} current={openId === job.id} onOpen={() => update({ job: job.id })}>
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
                      </Row>
                    );
                  })}
                </tbody>
              </Table>
            </>
          )}
        </QueryView>
      </TableScroll>
    </>
  );

  let pane = null;
  if (open) pane = <JobPane key={open.id} job={open} workerEnabled={jobs.data?.workerEnabled ?? false} onClose={close} />;
  // The full list is loaded (and polled, and jobs are not created here), so a job missing from it does not exist.
  else if (openId && jobs.data) pane = <EmptyState icon={RiErrorWarningLine}>{t("common.notFound")}</EmptyState>;
  else if (openId && !jobs.isError) pane = <Skeleton variant="form" />;
  return <SplitView list={list} paneLabel={t("ops.jobs.detail")} onClose={close} pane={pane} />;
}

function JobPane({ job, workerEnabled, onClose }: { job: ManagedJob; workerEnabled: boolean; onClose: () => void }) {
  const t = useT();
  const time = useTimeFormat();
  const queryClient = useQueryClient();
  // An untouched form follows changes another administrator makes; an edited one keeps the edits.
  const { draft, patch, dirty, reset } = useDraft({ schedule: job.schedule, timezone: job.timezone, enabled: job.enabled });
  const { schedule, timezone, enabled } = draft;
  const refresh = () => queryClient.invalidateQueries({ queryKey: jobsQuery.queryKey });

  const save = useMutation({
    mutationFn: () => updateJob(job.id, { schedule: schedule.trim(), timezone: timezone.trim(), enabled }),
    onSuccess: () => { patch({ schedule: schedule.trim(), timezone: timezone.trim() || "UTC" }); toast(t("ops.jobs.saved")); },
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
        {locked && <Notice role="note">{workerEnabled ? t("ops.jobs.unregisteredNote") : t("ops.jobs.noWorker")}</Notice>}
        {save.isError && (
          <Notice tone="danger">{save.error instanceof ApiError && save.error.code === "invalid_request" ? t("ops.jobs.invalid") : errorText(t, save.error)}</Notice>
        )}
        <fieldset className="form" disabled={locked}>
          <Field label={t("ops.jobs.schedule")} mono spellCheck={false} autoComplete="off" required value={schedule} onChange={(event) => patch({ schedule: event.target.value })}
            hint={summary ? t(summary.key, summary.vars) : t("ops.jobs.scheduleHint")} />
          <Field label={t("ops.jobs.timezone")} mono spellCheck={false} autoComplete="off" list="ops-zones" placeholder="UTC" value={timezone} onChange={(event) => patch({ timezone: event.target.value })} />
          <datalist id="ops-zones">
            {zones.map((zone) => (
              <option key={zone} value={zone} />
            ))}
          </datalist>
          <Checkbox label={t("ops.jobs.enabledLabel")} checked={enabled} onChange={(event) => patch({ enabled: event.target.checked })} />
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

/** The ?q= filter box: local text so typing is never held up by the URL, mirrored there without new history entries. */
function FilterParam({ label }: { label: string }) {
  const { params, update } = useSearchState();
  const applied = params.get("q") ?? "";
  const [text, setText] = useState(applied);
  useEffect(() => setText(applied), [applied]);
  return <Searchbar size="sm" label={label} primary value={text} onChange={(event) => { setText(event.target.value); update({ q: event.target.value || null }, { replace: true }); }} />;
}
