import type { ReactNode } from "react";
import { useQuery } from "@tanstack/react-query";
import { RiCloseLine, RiErrorWarningLine, RiFileCopyLine, RiFilterLine } from "@remixicon/react";
import { stringify } from "lossless-json";
import { errorText } from "../app/queries";
import { Button, IconButton } from "../components/Button";
import { EmptyState, Loader } from "../components/Feedback";
import { toast } from "../components/Toast";
import { useT } from "../i18n";
import { statusTone } from "./format";
import { logQuery, useTimeFormat } from "./queries";

interface LogDetailProps {
  id: string;
  onClose: () => void;
  onFilterRoute: (method: string, route: string) => void;
}

/** One request log entry, fetched by id, with its raw JSON. */
export function LogDetail({ id, onClose, onFilterRoute }: LogDetailProps) {
  const t = useT();
  const time = useTimeFormat();
  const query = useQuery(logQuery(id));
  const entry = query.data;
  const raw = entry ? stringify(entry, null, 2) ?? "" : "";
  const copy = () =>
    navigator.clipboard.writeText(raw).then(
      () => toast(t("records.copied")),
      () => toast(t("records.copyFailed"), "danger"),
    );
  const rows: [string, ReactNode][] = entry
    ? [
        [t("ops.logs.time"), <>{time(entry.at, true)} <span className="txt-hint mono">{entry.at}</span></>],
        [t("ops.logs.status"), <span className="ops-status" data-tone={statusTone(entry.status)}>{entry.status}</span>],
        [t("ops.logs.method"), <span className="mono">{entry.method}</span>],
        [t("ops.logs.route"), <span className="mono">{entry.route}</span>],
        [t("ops.logs.duration"), <span className="mono">{String(entry.durationMs)} ms</span>],
        [t("ops.logs.client"), entry.clientIP ? <span className="mono">{entry.clientIP}</span> : null],
        [t("ops.logs.auth"), entry.authKind ? <span className="mono">{entry.authKind}{entry.authID ? ` · ${entry.authID}` : ""}</span> : null],
      ]
    : [];
  return (
    <>
      <header className="pane-header">
        <h2 className="pane-title mono">#{id}</h2>
        {entry && <IconButton label={t("ops.logs.copyJson")} icon={RiFileCopyLine} onClick={copy} />}
        <IconButton label={t("common.close")} icon={RiCloseLine} onClick={onClose} />
      </header>
      <div className="pane-body">
        {query.isPending ? (
          <Loader />
        ) : query.isError ? (
          <EmptyState icon={RiErrorWarningLine}>{errorText(t, query.error)}</EmptyState>
        ) : (
          <>
            <dl className="ops-props">
              {rows.map(([label, value]) => (
                <div key={label}>
                  <dt>{label}</dt>
                  <dd>{value ?? <span className="na">{t("common.na")}</span>}</dd>
                </div>
              ))}
            </dl>
            <h3 className="ops-subtitle">{t("ops.logs.raw")}</h3>
            <pre className="ops-code">{raw}</pre>
          </>
        )}
      </div>
      {entry && (
        <footer className="pane-footer">
          <Button variant="secondary" icon={RiFilterLine} className="pane-footer-start" onClick={() => onFilterRoute(entry.method, entry.route)}>{t("ops.logs.sameRoute")}</Button>
          <Button variant="secondary" onClick={onClose}>{t("common.close")}</Button>
        </footer>
      )}
    </>
  );
}
