import { useEffect, useRef, useState } from "react";
import { Button } from "../components/ui";
import type { AdminCapabilities, BackupEntry, BackupJob, JobPage, OperationsPort, RequestLogEntry, RequestLogPage, RequestLogStatistics, ScheduledJob } from "../backend/ports";

function JobEditor({ job, busy, save, run }: { job: ScheduledJob; busy: boolean; save: (schedule: string, timezone: string, enabled: boolean) => void; run: () => void }) {
  const [schedule, setSchedule] = useState(job.schedule);
  const [timezone, setTimezone] = useState(job.timezone ?? "UTC");
  const [enabled, setEnabled] = useState(job.enabled);
  return <li className="system-job">
    <h4>{job.name}</h4>
    <p>{!job.registered ? "Handler unavailable in this instance" : job.running ? "Running" : job.failed ? "Last execution failed; retry scheduled" : job.enabled ? "Scheduled" : "Disabled"}</p>
    <p>Next: {job.nextRunAt}. Last completed: {job.lastFinishedAt ?? "Never"}. Attempts: {job.attempts}.</p>
    <fieldset className="system-job-fields" disabled={busy || job.running || !job.registered}>
      <div className="system-filter-grid">
      <label>Interval for {job.name}<input value={schedule} maxLength={40} onChange={event => setSchedule(event.target.value)} placeholder="@every 5m" /></label>
      <label>Timezone for {job.name}<input value={timezone} maxLength={64} onChange={event => setTimezone(event.target.value)} placeholder="UTC" /></label>
      </div>
      <label className="system-toggle"><input type="checkbox" checked={enabled} onChange={event => setEnabled(event.target.checked)} />Enabled</label>
      <div className="system-actions">
      <Button onClick={() => save(schedule, timezone, enabled)}>Save job</Button>
      <Button variant="secondary" disabled={!job.enabled} onClick={run}>Queue run</Button>
      </div>
    </fieldset>
  </li>;
}

export function OperationsConsole({ port, capabilities }: { port: OperationsPort; capabilities?: AdminCapabilities }) {
  const [logs, setLogs] = useState<RequestLogPage | null>(null);
  const [jobs, setJobs] = useState<JobPage | null>(null);
  const [statusClass, setStatusClass] = useState(0);
  const [method, setMethod] = useState("");
  const [route, setRoute] = useState("");
  const [from, setFrom] = useState("");
  const [to, setTo] = useState("");
  const [statistics, setStatistics] = useState<RequestLogStatistics | null>(null);
  const [detail, setDetail] = useState<RequestLogEntry | null>(null);
  const [backups, setBackups] = useState<BackupEntry[] | null>(null);
  const [backupJob, setBackupJob] = useState<BackupJob | null>(null);
  const [before, setBefore] = useState<number>();
  const [reload, setReload] = useState(0);
  const [loading, setLoading] = useState(true);
  const [busy, setBusy] = useState(false);
  const [message, setMessage] = useState("");
  const request = useRef<AbortController | null>(null);
  const mutation = useRef<AbortController | null>(null);
  useEffect(() => () => mutation.current?.abort(), []);
  useEffect(() => {
    if (!port.backups || capabilities?.backups === false) return;
    const controller=new AbortController();void port.backups(controller.signal).then(items=>{if(!controller.signal.aborted)setBackups(items)}).catch(()=>{if(!controller.signal.aborted)setMessage("Unable to load backups. Refresh to retry.")});return()=>controller.abort();
  }, [port,reload,capabilities?.backups]);
  useEffect(() => {
    if (!backupJob || backupJob.status !== "running" || !port.backupJob) return;
    const controller=new AbortController();const timer=window.setInterval(()=>void port.backupJob!(backupJob.id,controller.signal).then(job=>{if (controller.signal.aborted) return; setBackupJob(job);if(job.status!=="running")setReload(value=>value+1)}).catch(()=>{if (!controller.signal.aborted) setMessage("Backup status is unavailable. If restore activated the replacement database, sign in again.")}),1000);
    return()=>{controller.abort();window.clearInterval(timer)};
  },[backupJob,port]);
  useEffect(() => {
    const controller = new AbortController();
    request.current = controller;
    setLoading(true); setLogs(null); setJobs(null);
    const timeout = window.setTimeout(() => { controller.abort(); setMessage("Operations request timed out. Refresh to retry."); setLoading(false); }, 10_000);
    const logRequest = capabilities?.logs === false ? Promise.resolve(null) : port.searchLogs ? port.searchLogs({ before, statusClass: statusClass || undefined, method: method || undefined, route: route || undefined, from: from ? new Date(from).toISOString() : undefined, to: to ? new Date(to).toISOString() : undefined }, controller.signal) : port.logs(before, statusClass || undefined, controller.signal);
    void Promise.all([logRequest, capabilities?.jobs === false ? Promise.resolve(null) : port.jobs(controller.signal), capabilities?.logs === false ? undefined : port.logStatistics?.(controller.signal)])
      .then(([logPage, jobPage, stats]) => { if (!controller.signal.aborted) { setLogs(logPage); setJobs(jobPage); setStatistics(stats ?? null); } })
      .catch(() => { if (!controller.signal.aborted) setMessage("Unable to load operations. Refresh to retry."); })
      .finally(() => { if (!controller.signal.aborted) { window.clearTimeout(timeout); setLoading(false); } });
    return () => { controller.abort(); window.clearTimeout(timeout); };
  }, [port, before, statusClass, method, route, from, to, reload, capabilities?.logs, capabilities?.jobs]);

  async function change(action: (signal: AbortSignal) => Promise<void>, success: string, timeoutMs = 10_000) {
    if (mutation.current) return;
    const controller = new AbortController(); mutation.current = controller;
    setBusy(true); setMessage("");
    const timeout = window.setTimeout(() => controller.abort(), timeoutMs);
    try {
      await action(controller.signal);
      if (!controller.signal.aborted) { setMessage(success); setReload(value => value + 1); }
    } catch {
      // An aborted write may already have committed. Refresh before retrying.
      if (request.current && !request.current.signal.aborted) setMessage("Unable to confirm the operation. Refresh its state before retrying.");
    } finally {
      window.clearTimeout(timeout);
      if (mutation.current === controller) { mutation.current = null; setBusy(false); }
    }
  }
	function exportLogs() {
		if (!logs) return;
		const cell = (value: string | number) => `"${String(value).replaceAll('"', '""')}"`;
		const csv = [["at", "method", "route", "status", "durationMs"], ...logs.items.map(item => [item.at, item.method, item.route, item.status, item.durationMs])].map(row => row.map(cell).join(",")).join("\r\n");
		const url = URL.createObjectURL(new Blob([csv], { type: "text/csv;charset=utf-8" })); const link = document.createElement("a"); link.href = url; link.download = "pgbase-request-logs.csv"; link.click(); URL.revokeObjectURL(url);
	}

  return <section className="operations-console" aria-label="Operations console">
    <div className="system-section-heading"><h3>Operations console</h3>
    <Button variant="secondary" disabled={loading || busy} onClick={() => { setMessage(""); setBefore(undefined); setReload(value => value + 1); }}>Refresh operations</Button></div>
    {message && <p className="system-feedback" role="status">{message}</p>}
    {capabilities?.logs !== false && <section id="system-requests" className="system-operation-section" aria-labelledby="system-requests-heading">
    <h4 id="system-requests-heading">Recent HTTP requests</h4>
    <p>Completed requests are retained in PostgreSQL with bounded age and row limits. Only method, route template, status, timing and timestamp are stored.</p>
    <div className="system-filter-grid system-request-filters">
    <label>Status class<select value={statusClass} disabled={busy} onChange={event => { setStatusClass(Number(event.target.value)); setBefore(undefined); }}>
      <option value={0}>All statuses</option>{[1, 2, 3, 4, 5].map(value => <option key={value} value={value}>{value}xx</option>)}
    </select></label>
    <label>Method<input value={method} maxLength={16} onChange={event => { setMethod(event.target.value.toUpperCase()); setBefore(undefined); }} placeholder="GET" /></label>
    <label>Route template<input value={route} maxLength={256} onChange={event => { setRoute(event.target.value); setBefore(undefined); }} placeholder="/api/records/{collection}/{id}" /></label>
    <label>From<input type="datetime-local" value={from} onChange={event => { setFrom(event.target.value); setBefore(undefined); }} /></label>
    <label>To<input type="datetime-local" value={to} onChange={event => { setTo(event.target.value); setBefore(undefined); }} /></label>
    </div>
    {statistics && <><p>{statistics.total} retained requests · {statistics.averageDurationMs.toFixed(1)} ms average · {statistics.dropped} dropped before persistence.</p><div className="system-request-chart" aria-label="Request status chart" role="img">{Object.entries(statistics.statusClasses).map(([status, count]) => <div key={status}><span>{status}</span><meter max={Math.max(1, statistics.total)} value={count}>{count}</meter> {count}</div>)}</div></>}
    {loading && <p aria-live="polite">Loading operations…</p>}
    {logs && <>
      <div className="table-scroll system-request-table" role="region" aria-label="Recent HTTP requests" tabIndex={0}><table><thead><tr><th>Time</th><th>Method</th><th>Route</th><th>Status</th><th>ms</th></tr></thead><tbody>
        {logs.items.map(entry => <tr key={entry.id}><td><Button variant="secondary" disabled={!port.logDetail} onClick={() => void port.logDetail?.(entry.id).then(setDetail).catch(()=>setMessage("Unable to load request detail."))}>{entry.at}</Button></td><td>{entry.method}</td><td>{entry.route}</td><td>{entry.status}</td><td>{entry.durationMs}</td></tr>)}
      </tbody></table></div>
      {logs.items.length === 0 && <p>No retained requests match this page.</p>}
      {before !== undefined && <p>Entries older than retained ID {logs.oldestId} are no longer available.</p>}
      <div className="system-actions">
      <Button variant="secondary" disabled={!logs.nextBefore || busy} onClick={() => setBefore(logs.nextBefore)}>Older requests</Button>
		<Button variant="secondary" disabled={logs.items.length === 0 || busy} onClick={exportLogs}>Export displayed logs as CSV</Button>
      {port.truncateLogs && <Button variant="danger" disabled={busy} onClick={() => void change(signal => port.truncateLogs!(signal), "Request log truncated.")}>Truncate request log</Button>}
      </div>
    </>}
    {detail && <p className="system-feedback" role="status">Request {detail.id}: {detail.method} {detail.route}, {detail.status}, {detail.durationMs} ms at {detail.at}.</p>}
    </section>}
    {capabilities?.jobs !== false && <section id="system-jobs" className="system-operation-section" aria-labelledby="system-jobs-heading">
    <h4 id="system-jobs-heading">Scheduled jobs</h4>
    <p>Schedules accept @every 1s through @every 24h or five-field calendar cron with an IANA timezone. Queue run makes an enabled job due for the existing worker; it does not bypass a running lease.</p>
    {jobs && (!jobs.workerEnabled ? <p>The job worker is disabled in this instance. Configure named Go handlers and enable jobs to use scheduling.</p> : jobs.items.length === 0 ? <p>No scheduled handlers are registered.</p> : <ul>{jobs.items.map(job => <JobEditor key={`${job.id}:${job.schedule}:${job.timezone}:${job.enabled}`} job={job} busy={busy || loading} save={(schedule, timezone, enabled) => void change(signal => port.configureJobInTimezone ? port.configureJobInTimezone(job.id, schedule, timezone, enabled, signal) : port.configureJob(job.id, schedule, enabled, signal), "Job settings saved.")} run={() => void change(signal => port.runJob(job.id, signal), "Run queued. Refresh to inspect completion.")} />)}</ul>)}
    </section>}
    {capabilities?.backups !== false && <section id="system-backups" className="system-operation-section" aria-labelledby="system-backups-heading">
    <h4 id="system-backups-heading">Backup and restore</h4>
    <p>Online backups use a consistent PostgreSQL snapshot and immutable file inventory. Restore builds and verifies a fresh target before activation. A successful activation may require you to sign in again.</p>
    {backups === null && <p>Managed backups are unavailable. Configure the server backup directory to enable backup and restore.</p>}
    {backups !== null && port.createBackup && <Button variant="secondary" disabled={busy||backupJob?.status==="running"} onClick={()=>void change(async signal=>{setBackupJob(await port.createBackup!(signal))},"Backup started.")}>Create backup</Button>}
    {backups !== null && port.uploadBackup && <label>Upload gzip backup<input type="file" accept=".gz,application/gzip" disabled={busy||backupJob?.status==="running"} onChange={event=>{const file=event.target.files?.[0];if(file)void change(async signal=>{await port.uploadBackup!(file,signal)},"Backup uploaded.",30*60_000)}} /></label>}
    {backupJob && <p role="status">{backupJob.operation} backup {backupJob.backupId}: {backupJob.status} ({backupJob.phase}){backupJob.error ? ` — ${backupJob.error}` : ""}</p>}
    {backups !== null && (backups.length===0?<p>No managed backups are available.</p>:<ul>{backups.map(entry=><li key={entry.id}>{entry.createdAt} · {(entry.size/1048576).toFixed(1)} MiB · PostgreSQL {entry.postgresqlMajor} <Button variant="secondary" disabled={busy} onClick={()=>{if(port.backupDownloadURL){const link=document.createElement("a");link.href=port.backupDownloadURL(entry.id);link.download=`pgbase-${entry.id}.tar.gz`;link.click();return}void port.downloadBackup?.(entry.id).then(blob=>{const url=URL.createObjectURL(blob);const link=document.createElement("a");link.href=url;link.download=`pgbase-${entry.id}.tar.gz`;link.click();URL.revokeObjectURL(url)}).catch(()=>setMessage("Unable to download backup."))}}>Download</Button> <Button variant="danger" disabled={busy||backupJob?.status==="running"} onClick={()=>{if(window.confirm("Restore this backup to a fresh target and activate it?"))void change(async signal=>{setBackupJob(await port.restoreBackup!(entry.id,signal))},"Restore started.")}}>Restore</Button> <Button variant="danger" disabled={busy||backupJob?.status==="running"} onClick={()=>void change(signal=>port.deleteBackup!(entry.id,signal),"Backup deleted.")}>Delete</Button></li>)}</ul>)}
    </section>}
  </section>;
}
