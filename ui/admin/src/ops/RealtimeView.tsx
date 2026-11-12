import { useState, type FormEvent } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Searchbar } from "../components/Field";
import { RiHashtag, RiRefreshLine, RiReplyLine } from "@remixicon/react";
import { retryRealtimeEvent } from "../api/realtime";
import { errorText } from "../app/queries";
import { Button, IconButton } from "../components/Button";
import { ErrorState, Loader } from "../components/Feedback";
import { toast } from "../components/Toast";
import { useT } from "../i18n";
import { isUuid } from "./format";
import { formatDuration } from "../app/format";
import { realtimeQuery } from "./queries";
import { useTimeFormat } from "../app/format";
import "../styles/ops.css";

export function RealtimeActions() {
  const t = useT();
  const queryClient = useQueryClient();
  return <IconButton label={t("ops.refresh")} icon={RiRefreshLine} onClick={() => queryClient.invalidateQueries({ queryKey: realtimeQuery.queryKey })} />;
}

/** Outbox delivery counts (polled) and a retry for one dead event. */
export function RealtimeView() {
  const t = useT();
  const time = useTimeFormat();
  const queryClient = useQueryClient();
  const status = useQuery(realtimeQuery);
  const [eventId, setEventId] = useState("");
  const retry = useMutation({
    mutationFn: (id: string) => retryRealtimeEvent(id),
    // 204 also covers ids that are not dead, so this only says the request went through.
    onSuccess: () => {
      toast(t("ops.realtime.retried"));
      setEventId("");
    },
    onError: (error) => toast(errorText(t, error), "danger"),
    onSettled: () => queryClient.invalidateQueries({ queryKey: realtimeQuery.queryKey }),
  });
  const onSubmit = (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault();
    if (isUuid(eventId)) retry.mutate(eventId.trim());
  };

  if (status.isPending) return <Loader />;
  if (status.isError) return <ErrorState error={status.error} onRetry={status.refetch} />;
  const data = status.data;
  const dead = String(data.dead) !== "0";
  const lag = data.oldestPending ? Date.now() - new Date(data.oldestPending).getTime() : null;
  const invalid = eventId.trim() !== "" && !isUuid(eventId);

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
      <section className="ops-section" aria-labelledby="ops-retry-title">
        <h2 id="ops-retry-title" className="ops-subtitle">{t("ops.realtime.retryTitle")}</h2>
        <p className="txt-hint">{t("ops.realtime.retryText")}</p>
        <form className="ops-inline" onSubmit={onSubmit}>
          <Searchbar label={t("ops.realtime.eventId")} icon={RiHashtag} type="text" mono invalid={invalid} name="eventId" value={eventId} onChange={(event) => setEventId(event.target.value)} placeholder="00000000-0000-0000-0000-000000000000" />
          <Button type="submit" variant="secondary" icon={RiReplyLine} disabled={!isUuid(eventId)} loading={retry.isPending}>{t("common.retry")}</Button>
        </form>
        {invalid && <p className="field-error">{t("ops.realtime.badId")}</p>}
      </section>
    </div>
  );
}
