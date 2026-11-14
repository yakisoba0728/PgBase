import { useInfiniteQuery, useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { RiCheckboxCircleLine, RiReplyLine } from "@remixicon/react";
import { retryRealtimeEvent } from "../api/realtime";
import { errorText } from "../app/queries";
import { Button, RefreshButton } from "../components/Button";
import { EmptyState, ErrorState, Skeleton, LoadMore } from "../components/Feedback";
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
  const t = useT();
  const time = useTimeFormat();
  const status = useQuery(realtimeQuery);

  if (status.isPending) return <Skeleton variant="cards" />;
  if (status.isError) return <ErrorState error={status.error} onRetry={status.refetch} />;
  const data = status.data;
  const dead = String(data.dead) !== "0";
  const lag = data.oldestPending ? Date.now() - new Date(data.oldestPending).getTime() : null;

  return (
    <div className="page-content ops-realtime">
      <dl className="ops-tiles">
        <div>
          <dt>{t("ops.realtime.pending")}</dt>
          <dd className="mono">{String(data.pending)}</dd>
        </div>
        <div>
          <dt>{t("ops.realtime.lag")}</dt>
          <dd className="mono" data-tone={lag !== null && lag > 60_000 ? "warning" : undefined}>{lag === null ? "–" : formatDuration(lag)}</dd>
          {data.oldestPending && <dd className="ops-aside">{t("ops.realtime.oldestPending", { time: time(data.oldestPending) })}</dd>}
        </div>
        <div>
          <dt>{t("ops.realtime.dead")}</dt>
          <dd className="mono" data-tone={dead ? "danger" : undefined}>{String(data.dead)}</dd>
        </div>
        <div>
          <dt>{t("ops.realtime.delivered")}</dt>
          <dd className="mono">{String(data.delivered)}</dd>
        </div>
        <div>
          <dt>{t("ops.realtime.cursorFloor")}</dt>
          <dd className="mono">{String(data.cursorFloor)}</dd>
        </div>
      </dl>
      <DeadEvents />
    </div>
  );
}

function DeadEvents() {
  const t = useT();
  const time = useTimeFormat();
  const queryClient = useQueryClient();
  const dead = useInfiniteQuery(deadEventsQuery);
  const retry = useMutation({
    mutationFn: (id: string) => retryRealtimeEvent(id),
    onSuccess: () => toast(t("ops.realtime.retried")),
    onError: (error) => toast(errorText(t, error), "danger"),
    // A retried event leaves the dead list; the cursor stays valid, so a refetch is enough.
    onSettled: () => queryClient.invalidateQueries({ queryKey: realtimeKey }),
  });
  const items = dead.data?.pages.flatMap((page) => page.items) ?? [];

  return (
    <section className="ops-section" aria-labelledby="ops-dead-title">
      <h2 id="ops-dead-title" className="subtitle">{t("ops.realtime.deadTitle")}</h2>
      <p className="txt-hint">{t("ops.realtime.deadText")}</p>
      {dead.isPending ? (
        <Skeleton />
      ) : dead.isError ? (
        <ErrorState error={dead.error} onRetry={dead.refetch} />
      ) : items.length === 0 ? (
        <EmptyState icon={RiCheckboxCircleLine}>{t("ops.realtime.noDead")}</EmptyState>
      ) : (
        <>
          <table className="grid ops-grid" aria-label={t("ops.realtime.deadTitle")}>
            <thead>
              <tr>
                <th>{t("ops.realtime.deadAt")}</th>
                <th>{t("ops.realtime.record")}</th>
                <th className="num">{t("ops.realtime.attempts")}</th>
                <th>{t("ops.realtime.eventId")}</th>
                <th />
              </tr>
            </thead>
            <tbody>
              {items.map((item) => (
                <tr key={item.eventId}>
                  <td className="mono cell-time">{time(item.deadAt)}</td>
                  <td className="ops-request">
                    <span className="ops-method">{item.operation}</span>
                    <span className="mono">{item.collectionName}/{item.recordId}</span>
                  </td>
                  <td className="num mono">{item.attempts}</td>
                  <td className="mono">{item.eventId}</td>
                  <td>
                    <Button size="sm" variant="secondary" icon={RiReplyLine} loading={retry.isPending && retry.variables === item.eventId} disabled={retry.isPending} onClick={() => retry.mutate(item.eventId)}>{t("common.retry")}</Button>
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
          <LoadMore query={dead} />
        </>
      )}
    </section>
  );
}
