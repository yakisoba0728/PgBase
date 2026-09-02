import { useEffect, useState } from "react";
import type { SystemStatus } from "../backend/ports";
import { useBackend } from "../auth/session";

export function SystemPage() {
  const { system } = useBackend();
  const [status, setStatus] = useState<SystemStatus | null>(null);
  const [failed, setFailed] = useState(false);
  useEffect(() => {
    const controller = new AbortController();
    void system.status(controller.signal).then(setStatus).catch(() => {
      if (!controller.signal.aborted) setFailed(true);
    });
    return () => controller.abort();
  }, [system]);

  return <section>
    <h2>System</h2>
    <p>Read-only operational state reported by pgBase.</p>
    {failed && <p role="alert">Unable to load system status.</p>}
    {!status && !failed && <p aria-live="polite">Loading system status…</p>}
    {status && <dl className="system-status">
      <div><dt>Binary version</dt><dd>{status.version}</dd></div>
      <div><dt>Database</dt><dd>{status.databaseReachable ? "Reachable" : "Unavailable"}</dd></div>
      <div><dt>Migrations</dt><dd>{status.migrationCurrent} / {status.migrationLatest}</dd></div>
      <div><dt>Migration history</dt><dd>{status.migrationDirty ? "Dirty" : "Clean"}</dd></div>
      <div><dt>Catalog version</dt><dd>{status.catalogVersion}</dd></div>
      <div><dt>Catalog listener</dt><dd>{status.listenerReady ? "Ready" : "Not ready"}</dd></div>
    </dl>}
  </section>;
}
