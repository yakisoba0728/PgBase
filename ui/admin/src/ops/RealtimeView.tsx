import { useState } from "react";
import { useInfiniteQuery, useQuery, useQueryClient } from "@tanstack/react-query";
import { RiCheckboxCircleLine, RiReplyLine } from "@remixicon/react";
import { retryRealtimeEvent, type RealtimeStatus } from "../api/realtime";
import { errorText, windowOpen, windowShifted } from "../app/queries";
import { Button, RefreshButton } from "../components/Button";
import { EmptyState, LoadMore, WindowNotice } from "../components/Feedback";
import { Checkbox, GroupCheckbox } from "../components/Field";
import { settleLimited } from "../app/limit";
import { JobProgress } from "../components/JobProgress";
import { QueryView } from "../components/QueryView";
import { Table } from "../components/Table";
import { toast } from "../components/Toast";
import { useT } from "../i18n";
import { formatDuration } from "../app/format";
import { deadEventsQuery, realtimeKey, realtimeQuery } from "./queries";
import { useTimeFormat } from "../app/format";
import "../styles/ops.css";

export function RealtimeActions() {
  return <RefreshButton queryKey={realtimeKey} />;
}

/** Outbox delivery counts (polled) and the dead events, each with a retry. */
export function RealtimeView() {
  const status = useQuery(realtimeQuery);
  return <QueryView query={status} skeleton="cards">{(data) => <Outbox data={data} />}</QueryView>;
}

function Outbox({ data }: { data: RealtimeStatus }) {
  const t = useT();
  const time = useTimeFormat();
  const dead = String(data.dead) !== "0";
  const lag = data.oldestPending ? Date.now() - new Date(data.oldestPending).getTime() : null;

  return (
    <div className="page-content ops-realtime">
      <dl className="ops-tiles stagger">
        <div>
          <dt>{t("ops.realtime.pending")}</dt>
          <dd className="mono tick" key={String(data.pending)}>{String(data.pending)}</dd>
        </div>
        <div>
          <dt>{t("ops.realtime.lag")}</dt>
          <dd className="mono tick" key={lag === null ? "–" : formatDuration(lag)} data-tone={lag !== null && lag > 60_000 ? "warning" : undefined}>{lag === null ? "–" : formatDuration(lag)}</dd>
          {data.oldestPending && <dd className="ops-aside">{t("ops.realtime.oldestPending", { time: time(data.oldestPending) })}</dd>}
        </div>
        <div>
          <dt>{t("ops.realtime.dead")}</dt>
          <dd className="mono tick" key={String(data.dead)} data-tone={dead ? "danger" : undefined}>{String(data.dead)}</dd>
        </div>
        <div>
          <dt>{t("ops.realtime.delivered")}</dt>
          <dd className="mono tick" key={String(data.delivered)}>{String(data.delivered)}</dd>
        </div>
        <div>
          <dt>{t("ops.realtime.cursorFloor")}</dt>
          <dd className="mono tick" key={String(data.cursorFloor)}>{String(data.cursorFloor)}</dd>
        </div>
      </dl>
      <DeadEvents />
    </div>
  );
}

const retryConcurrency = 4;

interface RetryRun {
  total: number;
  done: number;
  failed: number;
  error?: unknown;
  running: boolean;
}

function DeadEvents() {
  const t = useT();
  const time = useTimeFormat();
  const queryClient = useQueryClient();
  const dead = useInfiniteQuery(deadEventsQuery);
  const [selected, setSelected] = useState<Set<string>>(new Set());
  const [inFlight, setInFlight] = useState<Set<string>>(new Set());
  const [run, setRun] = useState<RetryRun | null>(null);
  const items = dead.data?.pages.flatMap((page) => page.items) ?? [];
  const shifted = windowShifted(dead.data);
  // Only what is still listed counts; retried events leave the list on the next refetch.
  const chosen = items.filter((item) => selected.has(item.eventId)).map((item) => item.eventId);
  // A row already being retried is never sent twice.
  const idle = (ids: string[]) => ids.filter((id) => !inFlight.has(id));

  const retry = async (ids: string[], bulk: boolean) => {
    setInFlight((current) => new Set([...current, ...ids]));
    if (bulk) setRun({ total: ids.length, done: 0, failed: 0, running: true });
    const settle = (id: string, result: PromiseSettledResult<unknown>) => {
      setInFlight((current) => {
        const next = new Set(current);
        next.delete(id);
        return next;
      });
      if (bulk) setRun((current) => current && { ...current, done: current.done + 1, failed: current.failed + (result.status === "rejected" ? 1 : 0) });
    };
    const results = await settleLimited(ids, retryConcurrency, retryRealtimeEvent, settle);
    const failed = ids.filter((_, index) => results[index].status === "rejected");
    const firstError = results.find((result) => result.status === "rejected")?.reason;
    // A retried event leaves the dead list; one refetch per run rather than one per event.
    void queryClient.invalidateQueries({ queryKey: realtimeKey });
    if (bulk) {
      setRun((current) => current && { ...current, running: false, error: firstError });
      // Failed ones stay selected for another try.
      setSelected(new Set(failed));
    } else if (firstError === undefined) toast(t("ops.realtime.retried"));
    else toast(errorText(t, firstError), "danger");
  };
  const toggle = (id: string, on: boolean) =>
    setSelected((current) => {
      const next = new Set(current);
      if (on) next.add(id);
      else next.delete(id);
      return next;
    });
  const bulkBusy = run?.running ?? false;

  return (
    <section className="stack-section" aria-labelledby="ops-dead-title">
      <h2 id="ops-dead-title">{t("ops.realtime.deadTitle")}</h2>
      <p className="txt-hint">{t("ops.realtime.deadText")}</p>
      {run && (
        <JobProgress
          title={t("ops.realtime.bulkTitle")}
          state={run.running ? "running" : run.failed === 0 ? "done" : run.failed === run.total ? "failed" : "attention"}
          detail={run.running ? t("ops.realtime.bulkProgress", { done: run.done, total: run.total }) : t("ops.realtime.bulkDone", { ok: run.done - run.failed, total: run.total, failed: run.failed })}
          done={run.done}
          total={run.total}
          error={run.error === undefined ? undefined : errorText(t, run.error)}
          action={!run.running && <Button size="sm" variant="transparent" onClick={() => setRun(null)}>{t("common.dismiss")}</Button>}
        />
      )}
      <QueryView query={dead} empty={<EmptyState icon={RiCheckboxCircleLine}>{t("ops.realtime.noDead")}</EmptyState>} isEmpty={(data) => data.pages.every((page) => page.items.length === 0)}>
        {() => (
          <>
            <div className="toolbar toolbar-inset">
              <Button size="sm" variant="secondary" icon={RiReplyLine} disabled={bulkBusy || chosen.length === 0} onClick={() => void retry(idle(chosen), true)}>
                {t("ops.realtime.retrySelected", { count: chosen.length })}
              </Button>
              <Button size="sm" variant="secondary" disabled={bulkBusy} onClick={() => void retry(idle(items.map((item) => item.eventId)), true)}>
                {t("ops.realtime.retryLoaded", { count: items.length })}
              </Button>
            </div>
            {shifted && (
              <WindowNotice action={t("ops.logs.newest")} onReset={(button) => {
                button.closest("section")?.scrollIntoView({ block: "start" });
                void queryClient.resetQueries({ queryKey: deadEventsQuery.queryKey });
              }}>{t("ops.realtime.shifted", { count: items.length })}</WindowNotice>
            )}
            <Table className="ops-grid" label={t("ops.realtime.deadTitle")}>
              <thead>
                <tr>
                  <th>
                    <GroupCheckbox label={t("ops.realtime.selectAll")} chosen={chosen.length} total={items.length}
                      onChange={(on) => setSelected(on ? new Set(items.map((item) => item.eventId)) : new Set())} />
                  </th>
                  <th>{t("ops.realtime.deadAt")}</th>
                  <th>{t("ops.realtime.record")}</th>
                  <th className="num">{t("ops.realtime.attempts")}</th>
                  <th>{t("ops.realtime.eventId")}</th>
                  <th />
                </tr>
              </thead>
              <tbody>
                {items.map((item) => {
                  const busy = inFlight.has(item.eventId);
                  return (
                    <tr key={item.eventId}>
                      <td>
                        <Checkbox aria-label={t("ops.realtime.select", { id: item.eventId })} checked={selected.has(item.eventId)} disabled={busy}
                          onChange={(event) => toggle(item.eventId, event.target.checked)} />
                      </td>
                      <td className="mono cell-time">{time(item.deadAt)}</td>
                      <td className="ops-request">
                        <span className="ops-method">{item.operation}</span>
                        <span className="mono">{item.collectionName}/{item.recordId}</span>
                      </td>
                      <td className="num">{item.attempts}</td>
                      <td className="mono">{item.eventId}</td>
                      <td>
                        <Button size="sm" variant="secondary" icon={RiReplyLine} loading={busy} disabled={busy} onClick={() => void retry([item.eventId], false)}>{t("common.retry")}</Button>
                      </td>
                    </tr>
                  );
                })}
              </tbody>
            </Table>
            <LoadMore query={dead} auto={windowOpen(dead.data)} />
          </>
        )}
      </QueryView>
    </section>
  );
}
