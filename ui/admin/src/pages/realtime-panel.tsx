import { t, useLocale, message as localizedMessage, type Message, formatDate, getLocale } from "../i18n";
import { useCallback, useState } from "react";
import type { RealtimePort } from "../backend/ports";
import { Button, FormSection } from "../components/ui";
import { useFeatureResource } from "../components/feature-state";
const uuid = /^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/i;
export function RealtimePanel({ port }: { port: RealtimePort }) {
  useLocale();
  const read = useCallback((signal: AbortSignal) => port.status(signal), [port]);
  const resource = useFeatureResource(read);
  const [eventID, setEventID] = useState("");
  const [message, setMessage] = useState<Message>("");
  async function retry() {
    if (!uuid.test(eventID.trim())) { setMessage("Enter a valid event UUID."); return; }
    const result = await resource.run(signal => port.retry(eventID.trim(), signal));
    if (result) { setMessage(localizedMessage(`Retry requested for ${eventID.trim()}. Refresh status to check delivery.`, `${eventID.trim()} 이벤트 재시도를 요청했습니다. 상태를 새로고침하여 전달 여부를 확인하세요.`)); setEventID(""); resource.reload(); }
  }
  return <section id="system-realtime" aria-labelledby="realtime-title" className="realtime-panel">
    <div className="access-list-heading"><h3 id="realtime-title">{t("Realtime delivery")}</h3><p>{t("Monitor the event delivery queue and retry an event by its ID.")}</p></div>
    <Button variant="secondary" disabled={resource.busy || resource.loading} onClick={resource.reload}>{t("Refresh realtime status")}</Button>
    {resource.loading && <p role="status">{t("Loading realtime status…")}</p>}{resource.error && <p role="alert">{t(resource.error)}</p>}
    {resource.data && <dl className="system-status">{([['Pending',resource.data.pending],['Failed',resource.data.dead],['Delivered',resource.data.delivered],['Cursor floor',resource.data.cursorFloor]] as const).map(([label,value]) => <div key={label}><dt>{t(label)}</dt><dd>{value.toLocaleString(getLocale() === "ko" ? "ko-KR" : "en-US")}</dd></div>)}<div><dt>{t("Oldest pending event")}</dt><dd>{resource.data.oldestPending ? formatDate(resource.data.oldestPending) : t("None")}</dd></div></dl>}
    <FormSection title={t("Retry an event")} disabled={resource.busy}>
      <p className="settings-help">{t("Use an event ID from your diagnostic logs. The server does not expose a browsable failed-event list.")}</p>
      <form className="record-actions" onSubmit={e => { e.preventDefault(); void retry(); }}><label>{t("Event ID")}<input value={eventID} autoComplete="off" spellCheck={false} onChange={e => setEventID(e.target.value)} placeholder="00000000-0000-0000-0000-000000000000" /></label><Button type="submit" disabled={resource.busy || !uuid.test(eventID.trim())}>{t(resource.busy ? "Retrying…" : "Retry event")}</Button></form>
      {message && <p role="status">{t(message)}</p>}
    </FormSection>
  </section>;
}
