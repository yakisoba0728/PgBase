import type { ReactNode } from "react";
import { useQuery } from "@tanstack/react-query";
import type { SystemStatusProbe } from "../api/system";
import { errorText } from "../app/queries";
import { Loader } from "../components/Feedback";
import { ErrorState } from "../components/Feedback";
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
    <p className="warning-note" role="note">
      {t("system.status.unavailable")} <span className="mono">{items.map((item) => `${item.component} (${item.code})`).join(", ")}</span>
    </p>
  );
}

/** Read-only: GET /api/admin/system and /api/admin/system/health, sampled by the server. */
export function StatusView() {
  const t = useT();
  const time = useTimeFormat();
  const status = useQuery(systemStatusQuery);
  const health = useQuery(schemaHealthQuery);
  if (status.isPending || health.isPending) return <Loader />;
  if (status.isError) return <ErrorState error={status.error} busy={status.isFetching} onRetry={() => void status.refetch()} />;
  const s = status.data;
  const migrationTone: Tone = s.migrationDirty ? "danger" : s.migrationCurrent < s.migrationLatest ? "warning" : "success";
  const yes = (ok: boolean, good: string, bad: string) => <Dot tone={ok ? "success" : "danger"}>{ok ? good : bad}</Dot>;
  const healthTone: Tone = health.data?.state === "healthy" ? "success" : health.data?.state === "drift" ? "warning" : "danger";

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
        {health.isError ? (
          <p className="field-error">{errorText(t, health.error)}</p>
        ) : (
          <>
            <Probes items={health.data.unavailable} />
            <Props
              rows={[
                [t("system.status.state"), <Dot tone={healthTone}>{t(`system.health.${health.data.state}`)}</Dot>],
                [t("system.status.findings"), <span className="mono">{health.data.findingCount}</span>],
                [t("system.status.retries"), <span className="mono">{health.data.retries}</span>],
                [t("system.status.lastSuccess"), health.data.lastSuccessAt ? time(health.data.lastSuccessAt, true) : <span className="na">{t("common.na")}</span>],
              ]}
            />
            {health.data.findings.length > 0 && (
              <ul className="sys-findings">
                {health.data.findings.map((finding, index) => (
                  <li key={index}>
                    <Dot tone={finding.severity === "error" ? "danger" : "warning"}><span className="mono">{finding.code}</span></Dot>
                    <span>{finding.message}</span>
                  </li>
                ))}
              </ul>
            )}
          </>
        )}
      </FormSection>
    </div>
  );
}
