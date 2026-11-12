import { t, useLocale, message as localizedMessage, type Message } from "../i18n";
import { useEffect, useRef, useState } from "react";
import { Button } from "../components/ui";
import type { AdminCapabilities, BackupEntry, BackupJob, JobPage, OperationsPort, RequestLogEntry, RequestLogPage, RequestLogStatistics, ScheduledJob } from "../backend/ports";

function JobEditor({ job, busy, save, run }: { job: ScheduledJob; busy: boolean; save: (schedule: string, timezone: string, enabled: boolean) => void; run: () => void }) {
  useLocale();
  const [schedule, setSchedule] = useState(job.schedule);
  const [timezone, setTimezone] = useState(job.timezone ?? "UTC");
  const [enabled, setEnabled] = useState(job.enabled);
  return <li className="system-job">
    <h4>{job.name}</h4>
    <p>{t(!job.registered ? "Handler unavailable in this instance" : job.running ? "Running" : job.failed ? "Last execution failed; retry scheduled" : job.enabled ? "Scheduled" : "Disabled")}</p>
    <p>{t("Next: {next}. Last completed: {last}. Attempts: {attempts}.", { next: job.nextRunAt, last: job.lastFinishedAt ?? t("Never"), attempts: job.attempts })}</p>
    <fieldset className="system-job-fields" disabled={busy || job.running || !job.registered}>
      <div className="system-filter-grid">
      <label>{t("Interval for {name}", { name: job.name })}<input value={schedule} maxLength={40} onChange={event => setSchedule(event.target.value)} placeholder="@every 5m" /></label>
      <label>{t("Timezone for {name}", { name: job.name })}<input value={timezone} maxLength={64} onChange={event => setTimezone(event.target.value)} placeholder="UTC" /></label>
      </div>
      <label className="system-toggle"><input type="checkbox" checked={enabled} onChange={event => setEnabled(event.target.checked)} />{t("Enabled")}</label>
      <div className="system-actions">
      <Button onClick={() => save(schedule, timezone, enabled)}>{t("Save job")}</Button>
      <Button variant="secondary" disabled={!job.enabled} onClick={run}>{t("Queue run")}</Button>
      </div>
    </fieldset>
  </li>;
}

export function OperationsConsole({ port, capabilities, initialSection = "overview" }: { port: OperationsPort; capabilities?: AdminCapabilities; initialSection?: "overview" | "requests" | "jobs" | "backups" }) {
  useLocale();
  const [section, setSection] = useState<string>(initialSection);
  const detailRequest = useRef<AbortController | null>(null);
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
  const [message, setMessage] = useState<Message>("");
  const request = useRef<AbortController | null>(null);
  const mutation = useRef<AbortController | null>(null);
  useEffect(() => () => { mutation.current?.abort(); detailRequest.current?.abort(); }, []);
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
    setLoading(true); setLogs(null); setJobs(null); setStatistics(null); setDetail(null); detailRequest.current?.abort();
    const timeout = window.setTimeout(() => { controller.abort(); setMessage("Operations request timed out. Refresh to retry."); setLoading(false); }, 10_000);
    const logRequest = capabilities?.logs === false ? Promise.resolve(null) : port.searchLogs ? port.searchLogs({ before, statusClass: statusClass || undefined, method: method || undefined, route: route || undefined, from: from ? new Date(from).toISOString() : undefined, to: to ? new Date(to).toISOString() : undefined }, controller.signal) : port.logs(before, statusClass || undefined, controller.signal);
    const reads = [logRequest, capabilities?.jobs === false ? Promise.resolve(null) : port.jobs(controller.signal), capabilities?.logs === false ? Promise.resolve(null) : port.logStatistics?.(controller.signal)] as const;
    void Promise.allSettled(reads)
      .then(([logResult, jobResult, statsResult]) => {
        if (controller.signal.aborted) return;
        if (logResult?.status === "fulfilled") setLogs(logResult.value);
        if (jobResult?.status === "fulfilled") setJobs(jobResult.value);
        if (statsResult?.status === "fulfilled") setStatistics(statsResult.value ?? null);
        const failed = [logResult?.status === "rejected" && "request logs", jobResult?.status === "rejected" && "scheduled jobs", statsResult?.status === "rejected" && "log statistics"].filter(Boolean);
        if (failed.length) setMessage(localizedMessage(`Unable to load ${failed.join(", ")}. Other available sections are still shown. Refresh to retry.`, `불러오기 실패: ${[logResult?.status === "rejected" && "요청 로그", jobResult?.status === "rejected" && "예약 작업", statsResult?.status === "rejected" && "로그 통계"].filter(Boolean).join(", ")}. 다른 사용 가능한 항목은 계속 표시됩니다. 새로고침하여 다시 시도하세요.`));
      })
      .finally(() => { if (!controller.signal.aborted) { window.clearTimeout(timeout); setLoading(false); } });
    return () => { controller.abort(); window.clearTimeout(timeout); };
  }, [port, before, statusClass, method, route, from, to, reload, capabilities?.logs, capabilities?.jobs]);

  async function change(action: (signal: AbortSignal) => Promise<void>, success: Message, timeoutMs = 10_000) {
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

  async function inspectLog(id: number) {
    if (!port.logDetail) return;
    detailRequest.current?.abort();
    const controller = new AbortController(); detailRequest.current = controller;
    try { const next = await port.logDetail(id, controller.signal); if (!controller.signal.aborted) setDetail(next); }
    catch { if (!controller.signal.aborted) setMessage("Unable to load request detail."); }
  }
  const tabs = [{ id: "overview", label: "Overview" }, ...(capabilities?.logs !== false ? [{ id: "requests", label: "Requests" }] : []), ...(capabilities?.jobs !== false ? [{ id: "jobs", label: "Scheduled jobs" }] : []), ...(capabilities?.backups !== false ? [{ id: "backups", label: "Backups" }] : [])];
  return <section className="operations-console" aria-label={t("Operations console")}>
    <div className="system-section-heading"><h3>{t("Operations console")}</h3>
    <Button variant="secondary" disabled={loading || busy} onClick={() => { setMessage(""); setBefore(undefined); setReload(value => value + 1); }}>{t("Refresh operations")}</Button></div>
    <div className="operations-tabs" role="tablist" aria-label={t("Operations views")}>{tabs.map(tab => <button key={tab.id} type="button" role="tab" tabIndex={section === tab.id ? 0 : -1} onKeyDown={event => {
      if (!["ArrowLeft", "ArrowRight", "Home", "End"].includes(event.key)) return;
      event.preventDefault();
      const current = tabs.findIndex(item => item.id === section);
      const index = event.key === "Home" ? 0 : event.key === "End" ? tabs.length - 1 : (current + (event.key === "ArrowRight" ? 1 : -1) + tabs.length) % tabs.length;
      const next = tabs[index]!; setSection(next.id); document.getElementById(`operations-tab-${next.id}`)?.focus();
    }} id={`operations-tab-${tab.id}`} aria-selected={section === tab.id} aria-controls="operations-panels" onClick={() => setSection(tab.id)}>{t(tab.label)}</button>)}</div>
    {message && <p className="system-feedback" role="status">{t(message)}</p>}
    <div id="operations-panels" role="tabpanel" aria-labelledby={`operations-tab-${section}`}>
    {capabilities?.logs !== false && <section hidden={section !== "overview" && section !== "requests"} id="system-requests" className="system-operation-section" aria-labelledby="system-requests-heading">
    <h4 id="system-requests-heading">{t("Recent HTTP requests")}</h4>
    <p>{t("Completed requests are retained in PostgreSQL with bounded age and row limits. Only method, route template, status, timing and timestamp are stored.")}</p>
    <div className="system-filter-grid system-request-filters">
    <label>{t("Status class")}<select value={statusClass} disabled={busy} onChange={event => { setStatusClass(Number(event.target.value)); setBefore(undefined); }}>
      <option value={0}>{t("All statuses")}</option>{[1, 2, 3, 4, 5].map(value => <option key={value} value={value}>{value}xx</option>)}
    </select></label>
    <label>{t("Method")}<input value={method} maxLength={16} onChange={event => { setMethod(event.target.value.toUpperCase()); setBefore(undefined); }} placeholder="GET" /></label>
    <label>{t("Route template")}<input value={route} maxLength={256} onChange={event => { setRoute(event.target.value); setBefore(undefined); }} placeholder="/api/records/{collection}/{id}" /></label>
    <label>{t("From")}<input type="datetime-local" value={from} onChange={event => { setFrom(event.target.value); setBefore(undefined); }} /></label>
    <label>{t("To")}<input type="datetime-local" value={to} onChange={event => { setTo(event.target.value); setBefore(undefined); }} /></label>
    </div>
    {statistics && <><p>{t("{total} retained requests · {average} ms average · {dropped} dropped before persistence.", { total: statistics.total, average: statistics.averageDurationMs.toFixed(1), dropped: statistics.dropped })}</p><div className="system-request-chart" aria-label={t("Request status chart")} role="img">{Object.entries(statistics.statusClasses).map(([status, count]) => <div key={status}><span>{status}</span><meter max={Math.max(1, statistics.total)} value={count}>{count}</meter> {count}</div>)}</div></>}
    {loading && <p aria-live="polite">{t("Loading operations…")}</p>}
    {logs && <>
      <div className="table-scroll system-request-table" role="region" aria-label={t("Recent HTTP requests")} tabIndex={0}><table><thead><tr><th scope="col">{t("Time")}</th><th scope="col">{t("Method")}</th><th scope="col">{t("Route")}</th><th scope="col">{t("Status")}</th><th scope="col">{t("ms")}</th></tr></thead><tbody>
        {logs.items.map(entry => <tr key={entry.id}><td><Button variant="secondary" disabled={!port.logDetail} onClick={() => void inspectLog(entry.id)}>{entry.at}</Button></td><td>{entry.method}</td><td><code>{entry.route}</code></td><td><span className="request-status" data-status={Math.floor(entry.status / 100)}>{entry.status}</span></td><td>{entry.durationMs}</td></tr>)}
      </tbody></table></div>
      {logs.items.length === 0 && <p>{t("No retained requests match this page.")}</p>}
      {before !== undefined && <p>{t("Entries older than retained ID {id} are no longer available.", { id: logs.oldestId })}</p>}
      <div className="system-actions">
      <Button variant="secondary" disabled={!logs.nextBefore || busy} onClick={() => setBefore(logs.nextBefore)}>{t("Older requests")}</Button>
		<Button variant="secondary" disabled={logs.items.length === 0 || busy} onClick={exportLogs}>{t("Export displayed logs as CSV")}</Button>
      {port.truncateLogs && <Button variant="danger" disabled={busy} onClick={() => void change(signal => port.truncateLogs!(signal), "Request log truncated.")}>{t("Truncate request log")}</Button>}
      </div>
    </>}
    {detail && <p className="system-feedback" role="status">{t("Request {id}: {method} {route}, {status}, {duration} ms at {at}.", { id: detail.id, method: detail.method, route: detail.route, status: detail.status, duration: detail.durationMs, at: detail.at })}</p>}
    </section>}
    {capabilities?.jobs !== false && <section hidden={section !== "overview" && section !== "jobs"} id="system-jobs" className="system-operation-section" aria-labelledby="system-jobs-heading">
    <h4 id="system-jobs-heading">{t("Scheduled jobs")}</h4>
    <p>{t("Schedules accept @every 1s through @every 24h or five-field calendar cron with an IANA timezone. Queue run makes an enabled job due for the existing worker; it does not bypass a running lease.")}</p>
    {jobs && (!jobs.workerEnabled ? <p>{t("The job worker is disabled in this instance. Configure named Go handlers and enable jobs to use scheduling.")}</p> : jobs.items.length === 0 ? <p>{t("No scheduled handlers are registered.")}</p> : <ul>{jobs.items.map(job => <JobEditor key={`${job.id}:${job.schedule}:${job.timezone}:${job.enabled}`} job={job} busy={busy || loading} save={(schedule, timezone, enabled) => void change(signal => port.configureJobInTimezone ? port.configureJobInTimezone(job.id, schedule, timezone, enabled, signal) : port.configureJob(job.id, schedule, enabled, signal), "Job settings saved.")} run={() => void change(signal => port.runJob(job.id, signal), "Run queued. Refresh to inspect completion.")} />)}</ul>)}
    </section>}
    {capabilities?.backups !== false && <section hidden={section !== "overview" && section !== "backups"} id="system-backups" className="system-operation-section" aria-labelledby="system-backups-heading">
    <h4 id="system-backups-heading">{t("Backup and restore")}</h4>
    <p>{t("Online backups use a consistent PostgreSQL snapshot and immutable file inventory. Restore builds and verifies a fresh target before activation. A successful activation may require you to sign in again.")}</p>
    {backups === null && <p>{t("Managed backups are unavailable. Configure the server backup directory to enable backup and restore.")}</p>}
    {backups !== null && port.createBackup && <Button variant="secondary" disabled={busy||backupJob?.status==="running"} onClick={()=>void change(async signal=>{setBackupJob(await port.createBackup!(signal))},"Backup started.")}>{t("Create backup")}</Button>}
    {backups !== null && port.uploadBackup && <label>{t("Upload gzip backup")}<input type="file" accept=".gz,application/gzip" disabled={busy||backupJob?.status==="running"} onChange={event=>{const file=event.target.files?.[0];if(file)void change(async signal=>{await port.uploadBackup!(file,signal)},"Backup uploaded.",30*60_000)}} /></label>}
    {backupJob && <p role="status">{t("{operation} backup {id}: {status} ({phase})", { operation: backupJob.operation, id: backupJob.backupId, status: backupJob.status, phase: backupJob.phase })}{backupJob.error ? ` — ${backupJob.error}` : ""}</p>}
    {backups !== null && (backups.length===0?<p>{t("No managed backups are available.")}</p>:<ul>{backups.map(entry=><li key={entry.id}>{entry.createdAt} · {(entry.size/1048576).toFixed(1)} MiB · PostgreSQL {entry.postgresqlMajor} <Button variant="secondary" disabled={busy || (!port.backupDownloadURL && !port.downloadBackup)} onClick={()=>{if(port.backupDownloadURL){const link=document.createElement("a");link.href=port.backupDownloadURL(entry.id);link.download=`pgbase-${entry.id}.tar.gz`;link.click();return}void port.downloadBackup?.(entry.id).then(blob=>{const url=URL.createObjectURL(blob);const link=document.createElement("a");link.href=url;link.download=`pgbase-${entry.id}.tar.gz`;link.click();URL.revokeObjectURL(url)}).catch(()=>setMessage("Unable to download backup."))}}>{t("Download")}</Button> <Button variant="danger" disabled={busy||backupJob?.status==="running"||!port.restoreBackup} onClick={()=>{if(window.confirm(t("Restore this backup to a fresh target and activate it?")))void change(async signal=>{setBackupJob(await port.restoreBackup!(entry.id,signal))},"Restore started.")}}>{t("Restore")}</Button> <Button variant="danger" disabled={busy||backupJob?.status==="running"||!port.deleteBackup} onClick={()=>void change(signal=>port.deleteBackup!(entry.id,signal),"Backup deleted.")}>{t("Delete")}</Button></li>)}</ul>)}
    </section>}
    </div>
  </section>;
}
