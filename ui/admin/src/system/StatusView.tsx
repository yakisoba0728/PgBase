import type { ReactNode } from "react";
import { useQuery, type UseQueryResult } from "@tanstack/react-query";
import type { SchemaHealth, SystemStatus, SystemStatusProbe } from "../api/system";
import { Notice } from "../components/Feedback";
import { QueryView } from "../components/QueryView";
import { Facts } from "../components/Facts";
import { FormSection } from "../components/FormSection";
import { useT } from "../i18n";
import { useTimeFormat } from "../app/format";
import { schemaHealthQuery, systemStatusQuery } from "./queries";
import "../styles/system.css";

type Tone = "success" | "warning" | "danger" | undefined;

function Dot({ tone, children }: { tone: Tone; children: ReactNode }) {
  return <span className="dot" data-tone={tone}>{children}</span>;
}

function Props({ rows }: { rows: [string, ReactNode][] }) {
  return <Facts items={rows.map(([label, value]) => ({ label, value }))} />;
}

function Probes({ items }: { items: SystemStatusProbe[] }) {
  const t = useT();
  if (items.length === 0) return null;
  return (
    <Notice role="note">
      {t("system.status.unavailable")} <span className="mono">{items.map((item) => `${item.component} (${item.code})`).join(", ")}</span>
    </Notice>
  );
}

/** Read-only: GET /api/admin/system and /api/admin/system/health, sampled by the server. */
export function StatusView() {
  const status = useQuery(systemStatusQuery);
  // Both requests start together, but health is its own block: a failed health read must not hide the server status.
  const health = useQuery(schemaHealthQuery);
  return <QueryView query={status} skeleton="cards">{(s) => <Status s={s} health={health} />}</QueryView>;
}

function Status({ s, health }: { s: SystemStatus; health: UseQueryResult<SchemaHealth> }) {
  const t = useT();
  const time = useTimeFormat();
  const migrationTone: Tone = s.migrationDirty ? "danger" : s.migrationCurrent < s.migrationLatest ? "warning" : "success";
  const yes = (ok: boolean, good: string, bad: string) => <Dot tone={ok ? "success" : "danger"}>{ok ? good : bad}</Dot>;

  return (
    <div className="sys-scroll">
      <FormSection title={t("system.status.server")} hint={t("system.status.sampled", { time: time(s.sampledAt, true) })}>
        <Probes items={s.unavailable} />
        <Props
          rows={[
            [t("system.status.version"), <span className="mono">{s.version || t("common.na")}</span>],
            [t("system.status.database"), yes(s.databaseReachable, t("system.status.reachable"), t("system.status.unreachable"))],
            [t("system.status.listener"), yes(s.listenerReady, t("system.status.ready"), t("system.status.notReady"))],
            [
              t("system.status.migration"),
              <Dot tone={migrationTone}>
                <span className="mono">{s.migrationCurrent} / {s.migrationLatest}</span>
                {s.migrationDirty && <span> · {t("system.status.dirty")}</span>}
              </Dot>,
            ],
            [t("system.status.catalog"), <span className="mono">{s.catalogVersion}</span>],
            [t("system.status.lastSuccess"), s.lastSuccessAt ? time(s.lastSuccessAt, true) : <span className="na">{t("common.na")}</span>],
          ]}
        />
      </FormSection>
      <FormSection title={t("system.status.schema")} hint={health.data && t("system.status.sampled", { time: time(health.data.sampledAt, true) })}>
        <QueryView query={health} skeleton="list">{(h) => <Health health={h} />}</QueryView>
      </FormSection>
    </div>
  );
}

function Health({ health }: { health: SchemaHealth }) {
  const t = useT();
  const time = useTimeFormat();
  const healthTone: Tone = health.state === "healthy" ? "success" : health.state === "drift" ? "warning" : "danger";
  return (
    <>
      <Probes items={health.unavailable} />
      <Props
        rows={[
          [t("system.status.state"), <Dot tone={healthTone}>{t(`system.health.${health.state}`)}</Dot>],
          [t("system.status.findings"), <span className="mono">{health.findingCount}</span>],
          [t("system.status.retries"), <span className="mono">{health.retries}</span>],
          [t("system.status.lastSuccess"), health.lastSuccessAt ? time(health.lastSuccessAt, true) : <span className="na">{t("common.na")}</span>],
        ]}
      />
      {health.findings.length > 0 && (
        <ul className="sys-findings">
          {health.findings.map((finding, index) => (
            <li key={index}>
              <Dot tone={finding.severity === "error" ? "danger" : "warning"}><span className="mono">{finding.code}</span></Dot>
              <span>{finding.message}</span>
            </li>
          ))}
        </ul>
      )}
    </>
  );
}
