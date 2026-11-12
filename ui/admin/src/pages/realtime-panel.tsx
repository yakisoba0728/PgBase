import { useCallback, useState } from "react";
import type { RealtimePort } from "../backend/ports";
import { Button, FormSection } from "../components/ui";
import { useFeatureResource } from "../components/feature-state";
const uuid = /^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/i;
export function RealtimePanel({ port }: { port: RealtimePort }) {
  const read = useCallback((signal: AbortSignal) => port.status(signal), [port]);
  const resource = useFeatureResource(read);
  const [eventID, setEventID] = useState("");
  const [message, setMessage] = useState("");
  async function retry() {
    if (!uuid.test(eventID.trim())) { setMessage("Enter a valid event UUID."); return; }
    const result = await resource.run(signal => port.retry(eventID.trim(), signal));
    if (result) { setMessage(`Retry requested for ${eventID.trim()}. Refresh status to check delivery.`); setEventID(""); resource.reload(); }
  }
  return <section id="system-realtime" aria-labelledby="realtime-title" className="realtime-panel">
    <div className="access-list-heading"><h3 id="realtime-title">Realtime delivery</h3><p>Monitor the event delivery queue and retry an event by its ID.</p></div>
    <Button variant="secondary" disabled={resource.busy || resource.loading} onClick={resource.reload}>Refresh realtime status</Button>
    {resource.loading && <p role="status">Loading realtime status…</p>}{resource.error && <p role="alert">{resource.error}</p>}
    {resource.data && <dl className="system-status">{([['Pending',resource.data.pending],['Failed',resource.data.dead],['Delivered',resource.data.delivered],['Cursor floor',resource.data.cursorFloor]] as const).map(([label,value]) => <div key={label}><dt>{label}</dt><dd>{value.toLocaleString()}</dd></div>)}<div><dt>Oldest pending event</dt><dd>{resource.data.oldestPending ? new Date(resource.data.oldestPending).toLocaleString() : "None"}</dd></div></dl>}
    <FormSection title="Retry an event" disabled={resource.busy}>
      <p className="settings-help">Use an event ID from your diagnostic logs. The server does not expose a browsable failed-event list.</p>
      <form className="record-actions" onSubmit={e => { e.preventDefault(); void retry(); }}><label>Event ID<input value={eventID} autoComplete="off" spellCheck={false} onChange={e => setEventID(e.target.value)} placeholder="00000000-0000-0000-0000-000000000000" /></label><Button type="submit" disabled={resource.busy || !uuid.test(eventID.trim())}>{resource.busy ? "Retrying…" : "Retry event"}</Button></form>
      {message && <p role="status">{message}</p>}
    </FormSection>
  </section>;
}
