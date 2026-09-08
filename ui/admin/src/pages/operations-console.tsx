import { useEffect, useRef, useState } from "react";
import type { JobPage, OperationsPort, RequestLogPage, ScheduledJob } from "../backend/ports";

function JobEditor({ job, busy, save, run }: { job: ScheduledJob; busy: boolean; save: (schedule: string, enabled: boolean) => void; run: () => void }) {
  const [schedule, setSchedule] = useState(job.schedule);
  const [enabled, setEnabled] = useState(job.enabled);
  return <li>
    <h4>{job.name}</h4>
    <p>{!job.registered ? "Handler unavailable in this instance" : job.running ? "Running" : job.failed ? "Last execution failed; retry scheduled" : job.enabled ? "Scheduled" : "Disabled"}</p>
    <p>Next: {job.nextRunAt}. Last completed: {job.lastFinishedAt ?? "Never"}. Attempts: {job.attempts}.</p>
    <fieldset disabled={busy || job.running || !job.registered}>
      <label>Interval for {job.name}<input value={schedule} maxLength={40} onChange={event => setSchedule(event.target.value)} placeholder="@every 5m" /></label>
      <label><input type="checkbox" checked={enabled} onChange={event => setEnabled(event.target.checked)} />Enabled</label>
      <button type="button" onClick={() => save(schedule, enabled)}>Save job</button>
      <button type="button" disabled={!job.enabled} onClick={run}>Queue run</button>
    </fieldset>
  </li>;
}

export function OperationsConsole({ port }: { port: OperationsPort }) {
  const [logs, setLogs] = useState<RequestLogPage | null>(null);
  const [jobs, setJobs] = useState<JobPage | null>(null);
  const [statusClass, setStatusClass] = useState(0);
  const [before, setBefore] = useState<number>();
  const [reload, setReload] = useState(0);
  const [loading, setLoading] = useState(true);
  const [busy, setBusy] = useState(false);
  const [message, setMessage] = useState("");
  const request = useRef<AbortController | null>(null);
  const mutation = useRef<AbortController | null>(null);
  useEffect(() => () => mutation.current?.abort(), []);
  useEffect(() => {
    const controller = new AbortController();
    request.current = controller;
    setLoading(true); setLogs(null); setJobs(null);
    const timeout = window.setTimeout(() => { controller.abort(); setMessage("Operations request timed out. Refresh to retry."); setLoading(false); }, 10_000);
    void Promise.all([port.logs(before, statusClass || undefined, controller.signal), port.jobs(controller.signal)])
      .then(([logPage, jobPage]) => { if (!controller.signal.aborted) { setLogs(logPage); setJobs(jobPage); } })
      .catch(() => { if (!controller.signal.aborted) setMessage("Unable to load operations. Refresh to retry."); })
      .finally(() => { if (!controller.signal.aborted) { window.clearTimeout(timeout); setLoading(false); } });
    return () => { controller.abort(); window.clearTimeout(timeout); };
  }, [port, before, statusClass, reload]);

  async function change(action: (signal: AbortSignal) => Promise<void>, success: string) {
    if (mutation.current) return;
    const controller = new AbortController(); mutation.current = controller;
    setBusy(true); setMessage("");
    const timeout = window.setTimeout(() => controller.abort(), 10_000);
    try {
      await action(controller.signal);
      if (!controller.signal.aborted) { setMessage(success); setReload(value => value + 1); }
    } catch {
      // An aborted write may already have committed. Refresh before retrying.
      if (request.current && !request.current.signal.aborted) setMessage("Unable to confirm the job change. Refresh its state before retrying.");
    } finally {
      window.clearTimeout(timeout);
      if (mutation.current === controller) { mutation.current = null; setBusy(false); }
    }
  }

  return <section aria-label="Operations console">
    <h3>Operations console</h3>
    {message && <p role="status">{message}</p>}
    <button type="button" disabled={loading || busy} onClick={() => { setMessage(""); setBefore(undefined); setReload(value => value + 1); }}>Refresh operations</button>
    <h4>Recent HTTP requests</h4>
    <p>This instance retains its last 1,000 completed requests in memory. Restarting clears them. Only route templates, status and timing are retained.</p>
    <label>Status class<select value={statusClass} disabled={busy} onChange={event => { setStatusClass(Number(event.target.value)); setBefore(undefined); }}>
      <option value={0}>All statuses</option>{[1, 2, 3, 4, 5].map(value => <option key={value} value={value}>{value}xx</option>)}
    </select></label>
    {loading && <p aria-live="polite">Loading operations…</p>}
    {logs && <>
      <div className="table-scroll"><table><thead><tr><th>Time</th><th>Method</th><th>Route</th><th>Status</th><th>ms</th></tr></thead><tbody>
        {logs.items.map(entry => <tr key={entry.id}><td>{entry.at}</td><td>{entry.method}</td><td>{entry.route}</td><td>{entry.status}</td><td>{entry.durationMs}</td></tr>)}
      </tbody></table></div>
      {logs.items.length === 0 && <p>No retained requests match this page.</p>}
      {before !== undefined && <p>Entries older than retained ID {logs.oldestId} are no longer available.</p>}
      <button type="button" disabled={!logs.nextBefore || busy} onClick={() => setBefore(logs.nextBefore)}>Older requests</button>
    </>}
    <h4>Scheduled jobs</h4>
    <p>Intervals accept @every 1s through @every 24h. Queue run makes an enabled job due for the existing worker; it does not bypass a running lease.</p>
    {jobs && (!jobs.workerEnabled ? <p>The job worker is disabled in this instance. Configure named Go handlers and enable jobs to use scheduling.</p> : jobs.items.length === 0 ? <p>No scheduled handlers are registered.</p> : <ul>{jobs.items.map(job => <JobEditor key={`${job.id}:${job.schedule}:${job.enabled}`} job={job} busy={busy || loading} save={(schedule, enabled) => void change(signal => port.configureJob(job.id, schedule, enabled, signal), "Job settings saved.")} run={() => void change(signal => port.runJob(job.id, signal), "Run queued. Refresh to inspect completion.")} />)}</ul>)}
    <h4>Local backup and restore</h4>
    <p>Stop every PgBase instance before creating a recovery set. The CLI refuses backup while a participating writer is active. It copies the database and local files together; PostgreSQL client tools must match the server major version.</p>
    <pre><code>{`pgbase backup create --directory ./backups/recovery-set
pgbase backup verify --directory ./backups/recovery-set`}</code></pre>
    <p>To restore, configure PGBASE_DATABASE_URL for a separate, empty database and PGBASE_FILES_LOCAL_DIRECTORY for a new directory, using your protected environment configuration. Then run:</p>
    <pre><code>pgbase backup restore --directory ./backups/recovery-set</code></pre>
    <p>The existing backup remains available after restore. Verify the restored project with pgbase doctor --json before starting it. This workflow supports local file storage; S3 objects require their own recovery set.</p>
  </section>;
}
