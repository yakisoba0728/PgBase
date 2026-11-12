import { useEffect } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { RiStopLine } from "@remixicon/react";
import { cancelSqlExecution, type SqlCell, type SqlExecution, type SqlStatementResult } from "../api/sql";
import { errorText } from "../app/queries";
import { Button } from "../components/Button";
import { DataGrid, type GridCell } from "../components/DataGrid";
import { ErrorState, Loader, Notice } from "../components/Feedback";
import { toast } from "../components/Toast";
import { en } from "../i18n/en";
import { useT, type MessageKey } from "../i18n";
import { errorLocation, sqlActive } from "./model";
import { sqlExecutionQuery, sqlHistoryKey } from "./queries";

export const statusTone: Record<SqlExecution["status"], string | undefined> = {
  pending: "accent",
  running: "accent",
  succeeded: undefined,
  failed: "danger",
  canceled: "warning",
  timed_out: "warning",
  unknown: "warning",
};

const numeric = new Set(["integer", "decimal"]);

// Exact server text: numbers are never parsed, so bigint and numeric keep every digit.
const gridCell = (cell: SqlCell): GridCell => ({ text: cell.kind === "null" ? null : (cell.text ?? ""), numeric: numeric.has(cell.kind), muted: cell.kind === "json" || cell.kind === "bytes" });

function StatementTable({ result, index }: { result: SqlStatementResult; index: number }) {
  const t = useT();
  if (!result.columns?.length) {
    return <p className="dt-command mono">{result.command || t("devtools.sql.statement", { index: index + 1 })} · {t("devtools.sql.affected", { count: result.affectedRows })}</p>;
  }
  return (
    <DataGrid
      label={t("devtools.sql.statement", { index: index + 1 })}
      columns={result.columns.map((column) => ({ name: column.name, type: column.typeName }))}
      rows={(result.rows ?? []).map((row) => row.map(gridCell))}
    />
  );
}

/** One execution: status, figures, cancel, the failure with its location, and the result grids. */
export function SqlResult({ id, sql }: { id: string; sql: string | undefined }) {
  const t = useT();
  const queryClient = useQueryClient();
  const query = useQuery(sqlExecutionQuery(id));
  const active = query.data ? sqlActive(query.data.status) : false;
  const cancel = useMutation({
    mutationFn: () => cancelSqlExecution(id),
    onSuccess: (next) => queryClient.setQueryData(sqlExecutionQuery(id).queryKey, next),
    onError: (error) => toast(errorText(t, error), "danger"),
  });

  // The history row shows the final status once the run settles.
  const settled = query.data !== undefined && !active;
  useEffect(() => {
    if (settled) void queryClient.invalidateQueries({ queryKey: sqlHistoryKey });
  }, [settled, queryClient]);

  if (query.isPending) return <Loader />;
  if (query.isError) return <ErrorState error={query.error} onRetry={query.refetch} />;
  const execution = query.data;
  const { result, failure } = execution;
  const location = failure && sql !== undefined ? errorLocation(sql, failure.position, failure.code === "rejected" ? "byte" : "char") : null;
  const failureKey = `devtools.sql.failure.${failure?.code}`;

  return (
    <div className="dt-outcome">
      <div className="toolbar dt-figures">
        <span className="chip" data-tone={statusTone[execution.status]}>
          {active && <span className="loader" aria-hidden />}
          {t(`devtools.sql.status.${execution.status}`)}
        </span>
        <span className="chip" data-tone={execution.mode === "write" ? "warning" : undefined}>{t(`devtools.sql.mode.${execution.mode}`)}</span>
        {execution.dryRun && <span className="chip">{t("devtools.sql.dryRun")}</span>}
        {result && (
          <>
            <span>{t("devtools.sql.rows", { count: result.rows })}</span>
            {execution.mode === "write" && <span>{t("devtools.sql.affected", { count: result.affectedRows })}</span>}
            <span className="mono">{result.elapsedMs} ms</span>
            {execution.mode === "write" && result.committed !== undefined && <span>{t(result.committed ? "devtools.sql.committed" : "devtools.sql.notCommitted")}</span>}
          </>
        )}
        {active && (
          <div className="dt-actions">
            <Button size="sm" variant="secondary" icon={RiStopLine} loading={cancel.isPending} onClick={() => cancel.mutate()}>{t("devtools.sql.cancel")}</Button>
          </div>
        )}
      </div>
      {result?.truncated && <Notice role="status" className="dt-inset">{t("devtools.sql.truncated", { rows: result.rows })}</Notice>}
      {failure && (
        <div className="pane-notice dt-inset" data-tone={execution.status === "failed" ? "danger" : undefined} role="alert">
          <strong>{failureKey in en ? t(failureKey as MessageKey) : failure.message}</strong>
          <p className="mono">
            {[failure.sqlState && `SQLSTATE ${failure.sqlState}`, failure.code, location && t("devtools.sql.at", { line: location.line, column: location.column })].filter(Boolean).join(" · ")}
          </p>
          {location && (
            <pre className="dt-caret mono">
              {location.text}
              {"\n"}
              {" ".repeat(Math.max(0, location.column - 1))}^
            </pre>
          )}
        </div>
      )}
      {execution.status === "succeeded" && !result && <p className="dt-muted dt-inset">{t("devtools.sql.expired")}</p>}
      {result?.statements && result.statements.length > 0 && (
        <div className="dt-result" tabIndex={0} aria-label={t("devtools.sql.result")}>
          {result.statements.map((statement, index) => <StatementTable key={index} result={statement} index={index} />)}
        </div>
      )}
    </div>
  );
}
