import type { ReactNode } from "react";
import { useQuery } from "@tanstack/react-query";
import { PaneHeader } from "../components/SplitView";
import { Facts } from "../components/Facts";
import { RiFilterLine } from "@remixicon/react";
import { stringify } from "lossless-json";
import { Button } from "../components/Button";
import { CodeBlock } from "../components/CodeBlock";
import { ErrorState, Loader } from "../components/Feedback";
import { useT } from "../i18n";
import { statusTone } from "./format";
import { logQuery } from "./queries";
import { useTimeFormat } from "../app/format";

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
  const rows: [string, ReactNode][] = entry
    ? [
        [t("ops.logs.time"), <>{time(entry.at, true)} <span className="txt-hint mono">{entry.at}</span></>],
        [t("ops.logs.status"), <span className="chip mono" data-tone={statusTone(entry.status)}>{entry.status}</span>],
        [t("ops.logs.method"), <span className="mono">{entry.method}</span>],
        [t("ops.logs.route"), <span className="mono">{entry.route}</span>],
        [t("ops.logs.duration"), <span className="mono">{String(entry.durationMs)} ms</span>],
        [t("ops.logs.client"), entry.clientIP ? <span className="mono">{entry.clientIP}</span> : null],
        [t("ops.logs.auth"), entry.authKind ? <span className="mono">{entry.authKind}{entry.authID ? ` · ${entry.authID}` : ""}</span> : null],
      ]
    : [];
  return (
    <>
      <PaneHeader title={`#${id}`} mono onClose={onClose} />
      <div className="pane-body">
        {query.isPending ? (
          <Loader />
        ) : query.isError ? (
          <ErrorState error={query.error} onRetry={query.refetch} />
        ) : (
          <>
            <Facts items={rows.map(([label, value]) => ({ label, value }))} />
            <h3 className="subtitle">{t("ops.logs.raw")}</h3>
            <CodeBlock label={t("ops.logs.raw")} text={raw} />
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
