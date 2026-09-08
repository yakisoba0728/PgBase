import { useEffect, useState } from "react";
import type { SchemaHealth, SystemStatus, SystemStatusProbe } from "../backend/ports";
import { useBackend } from "../auth/session";

interface SystemView { status: SystemStatus; health: SchemaHealth }
const systemLoadTimeoutMs = 10_000;
type SystemLoad =
  | { phase: "loading" }
  | { phase: "failed" }
  | { phase: "ready"; view: SystemView };

/**
 * A component named in the probe list was not confirmed by the sample beside
 * it, so its reading is shown as unconfirmed rather than as the number it
 * happens to still hold. Without this a database that is down reads as a
 * healthy "0 / 0" and a listener that was never reached reads as "Not ready".
 */
function unconfirmed(probes: SystemStatusProbe[], component: string): string | null {
  const probe = probes.find((entry) => entry.component === component);
  if (probe === undefined) return null;
  return probe.code === "not_sampled" ? "Not sampled" : "Unavailable";
}

const healthSummary: Record<SchemaHealth["state"], string> = {
  healthy: "No drift detected",
  drift: "Schema drift detected",
  unavailable: "Not diagnosed",
};

function Instant({ value }: { value: string }) {
  if (value === "") return <>Never</>;
  return <time dateTime={value}>{value}</time>;
}

function Reading({ label, probes, component, children }: {
  label: string; probes: SystemStatusProbe[]; component: string; children: React.ReactNode;
}) {
  return <div><dt>{label}</dt><dd>{unconfirmed(probes, component) ?? children}</dd></div>;
}

export function SystemPage() {
  const { system } = useBackend();
  const [load, setLoad] = useState<SystemLoad>({ phase: "loading" });
  const [reloads, setReloads] = useState(0);
  // Both reads answer from a background sample rather than from PostgreSQL, so
  // they are asked for together and fail together; a page that showed one of
  // them would be reporting half an instance's state as the whole of it.
  useEffect(() => {
    const controller = new AbortController();
    setLoad({ phase: "loading" });
    const finish = (next?: SystemLoad) => {
      if (controller.signal.aborted) return;
      window.clearTimeout(timeout);
      controller.abort();
      if (next !== undefined) setLoad(next);
    };
    const timeout = window.setTimeout(() => finish({ phase: "failed" }), systemLoadTimeoutMs);
    void Promise.all([system.status(controller.signal), system.health(controller.signal)])
      .then(([status, health]) => finish({ phase: "ready", view: { status, health } }))
      .catch(() => finish({ phase: "failed" }));
    return () => finish();
  }, [reloads, system]);

  const view = load.phase === "ready" ? load.view : null;
  const refresh = () => setReloads((current) => current + 1);

  return <section>
    <h2>System</h2>
    <p>Read-only operational state reported by pgBase.</p>
    {load.phase === "failed" && <p role="alert">Unable to load system status.</p>}
    {load.phase === "loading" && <p aria-live="polite">Loading system status…</p>}
    <button aria-disabled={load.phase === "loading"} onClick={load.phase === "loading" ? undefined : refresh} type="button">
      {load.phase === "loading" ? "Loading system status…" : load.phase === "failed" ? "Retry system status" : "Refresh system status"}
    </button>
    {view && <>
      <dl className="system-status">
        <div><dt>Binary version</dt><dd>{view.status.version}</dd></div>
        <Reading label="Database" probes={view.status.unavailable} component="database">
          {view.status.databaseReachable ? "Reachable" : "Unavailable"}
        </Reading>
        <Reading label="Migrations" probes={view.status.unavailable} component="migration">
          {view.status.migrationCurrent} / {view.status.migrationLatest}
        </Reading>
        <Reading label="Migration history" probes={view.status.unavailable} component="migration">
          {view.status.migrationDirty ? "Dirty" : "Clean"}
        </Reading>
        <Reading label="Catalog version" probes={view.status.unavailable} component="catalog">
          {view.status.catalogVersion}
        </Reading>
        <Reading label="Catalog listener" probes={view.status.unavailable} component="listener">
          {view.status.listenerReady ? "Ready" : "Not ready"}
        </Reading>
        <div><dt>Last successful sample</dt><dd><Instant value={view.status.lastSuccessAt} /></dd></div>
      </dl>

      <h3>Schema health</h3>
      <p className="health-state" data-state={view.health.state}>{healthSummary[view.health.state]}</p>
      <dl className="system-status">
        <div><dt>Last successful diagnosis</dt><dd><Instant value={view.health.lastSuccessAt} /></dd></div>
        <div><dt>Retries since the last diagnosis</dt><dd>{view.health.retries}</dd></div>
      </dl>
      {view.health.findings.length === 0
        ? <p>No findings reported.</p>
        : <ul className="finding-list">{view.health.findings.map((finding) => <li key={finding.code}>
          <span className="finding-code">{finding.code}</span>
          <span>{finding.message}</span>
        </li>)}</ul>}
      {view.health.findingCount > view.health.findings.length &&
        <p>Showing {view.health.findings.length} of {view.health.findingCount} findings. Run pgbase doctor for the full report.</p>}
    </>}
  </section>;
}
