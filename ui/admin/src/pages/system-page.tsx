import { t, useLocale } from "../i18n";
import { RealtimePanel } from "./realtime-panel";
import { OperationsConsole } from "./operations-console";
import { useEffect, useState } from "react";
import type { SchemaHealth, SystemStatus, SystemStatusProbe } from "../backend/ports";
import { useBackend } from "../auth/session";
import { Button, PageHeading } from "../components/ui";

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
  useLocale();
  if (value === "") return <>{t("Never")}</>;
  return <time dateTime={value}>{value}</time>;
}

function Reading({ label, probes, component, children }: {
  label: string; probes: SystemStatusProbe[]; component: string; children: React.ReactNode;
}) {
  useLocale();
  return <div><dt>{t(label)}</dt><dd>{unconfirmed(probes, component) === null ? children : t(unconfirmed(probes, component))}</dd></div>;
}

export function SystemPage({ initialSection }: { initialSection?: "requests" | "jobs" | "backups" } = {}) {
  useLocale();
  const { system, operations, realtime, capabilities } = useBackend();
  const [load, setLoad] = useState<SystemLoad>({ phase: "loading" });
  const [reloads, setReloads] = useState(0);
  // Both reads answer from a background sample rather than from PostgreSQL, so
  // they are asked for together and fail together; a page that showed one of
  // them would be reporting half an instance's state as the whole of it.
  useEffect(() => {
    if (initialSection) return;
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
  }, [reloads, system, initialSection]);

  const view = load.phase === "ready" ? load.view : null;
  const refresh = () => setReloads((current) => current + 1);

  if (initialSection) return <section className="system-page">
    <PageHeading title={t(initialSection === "requests" ? "Logs" : initialSection === "jobs" ? "Scheduled jobs" : "Backups")} description={t(initialSection === "requests" ? "Inspect HTTP requests and diagnose errors." : "Manage this instance's operational tasks.")} />
    {operations ? <OperationsConsole port={operations} capabilities={capabilities} initialSection={initialSection} /> : <p role="status">{t("Operations are unavailable on this instance.")}</p>}
  </section>;
  return <section className="system-page">
    <PageHeading title={t("System")} description={t("Operational state reported by pgBase.")} actions={
    <Button variant="secondary" aria-disabled={load.phase === "loading"} onClick={load.phase === "loading" ? undefined : refresh}>
      {t(load.phase === "loading" ? "Loading system status…" : load.phase === "failed" ? "Retry system status" : "Refresh system status")}
    </Button>} />
    <nav className="section-navigation" aria-label={t("System sections")}>
      <a href="#system-overview">{t("Status")}</a>
      {realtime && capabilities?.realtime !== false && <a href="#system-realtime">{t("Realtime")}</a>}
      {operations && <>{capabilities?.logs !== false && <a href="#system-requests">{t("Requests")}</a>}{capabilities?.jobs !== false && <a href="#system-jobs">{t("Scheduled jobs")}</a>}{capabilities?.backups !== false && <a href="#system-backups">{t("Backups")}</a>}</>}
    </nav>
    <div id="system-overview">
    {load.phase === "failed" && <p className="system-feedback" role="alert">{t("Unable to load system status.")}</p>}
    {load.phase === "loading" && <p aria-live="polite">{t("Loading system status…")}</p>}
    {view && <div className="system-overview">
      <section aria-labelledby="system-instance-heading">
      <h3 id="system-instance-heading">{t("Instance status")}</h3>
      <dl className="system-status">
        <div><dt>{t("Binary version")}</dt><dd>{view.status.version}</dd></div>
        <Reading label="Database" probes={view.status.unavailable} component="database">
          {t(view.status.databaseReachable ? "Reachable" : "Unavailable")}
        </Reading>
        <Reading label="Migrations" probes={view.status.unavailable} component="migration">
          {view.status.migrationCurrent} / {view.status.migrationLatest}
        </Reading>
        <Reading label="Migration history" probes={view.status.unavailable} component="migration">
          {t(view.status.migrationDirty ? "Dirty" : "Clean")}
        </Reading>
        <Reading label="Catalog version" probes={view.status.unavailable} component="catalog">
          {view.status.catalogVersion}
        </Reading>
        <Reading label="Catalog listener" probes={view.status.unavailable} component="listener">
          {t(view.status.listenerReady ? "Ready" : "Not ready")}
        </Reading>
      </dl>
      <dl className="system-status system-sample"><div><dt>{t("Last successful sample")}</dt><dd><Instant value={view.status.lastSuccessAt} /></dd></div></dl>
      </section>
      <section aria-labelledby="system-health-heading">
      <h3 id="system-health-heading">{t("Schema health")}</h3>
      <p className="health-state" data-state={view.health.state}>{t(healthSummary[view.health.state])}</p>
      <dl className="system-status">
        <div><dt>{t("Last successful diagnosis")}</dt><dd><Instant value={view.health.lastSuccessAt} /></dd></div>
        <div><dt>{t("Retries since the last diagnosis")}</dt><dd>{view.health.retries}</dd></div>
      </dl>
      {view.health.findings.length === 0
        ? <p>{t("No findings reported.")}</p>
        : <ul className="finding-list">{view.health.findings.map((finding) => <li key={finding.code}>
          <span className="finding-code">{finding.code}</span>
          <span>{finding.message}</span>
        </li>)}</ul>}
      {view.health.findingCount > view.health.findings.length &&
        <p>{t("Showing {shown} of {count} findings. Run pgbase doctor for the full report.", { shown: view.health.findings.length, count: view.health.findingCount })}</p>}
      </section>
    </div>}
    </div>
    {realtime && capabilities?.realtime !== false && <RealtimePanel port={realtime} />}
    {operations && <OperationsConsole port={operations} capabilities={capabilities} />}
  </section>;
}
