import { useState } from "react";
import { useInfiniteQuery, useMutation, useQueryClient } from "@tanstack/react-query";
import { keyLabel } from "../app/keys";
import { RiPlayLine, RiTerminalBoxLine } from "@remixicon/react";
import { ApiError } from "../api/http";
import { submitSql, type SqlExecutionPage, type SqlMode } from "../api/sql";
import { errorText } from "../app/queries";
import { Button } from "../components/Button";
import { ConfirmDialog } from "../components/Dialog";
import { EmptyState, ErrorState, Loader } from "../components/Feedback";
import { CodeArea } from "../components/CodeArea";
import { Checkbox } from "../components/Field";
import { Tabs } from "../components/Tabs";
import { toast } from "../components/Toast";
import { useT } from "../i18n";
import { useTimeFormat } from "../app/format";
import { useSearchState } from "../app/search";
import { newIdentity } from "../schema/apply";
import { recallStatements, rememberStatement, statementText } from "./model";
import { sqlExecutionQuery, sqlHistoryQuery } from "./queries";
import { SqlResult, statusTone } from "./SqlResult";
import "../styles/devtools.css";


/** The console is opt-in: a server without it answers 404 on its routes. */
export function SqlConsole() {
  const t = useT();
  const history = useInfiniteQuery(sqlHistoryQuery);
  if (history.isPending) return <Loader />;
  if (history.error instanceof ApiError && history.error.status === 404) {
    return (
      <div className="page-content">
        <div className="dt-disabled">
          <RiTerminalBoxLine size={24} aria-hidden />
          <h2>{t("devtools.sql.offTitle")}</h2>
          <p>{t("devtools.sql.offText")}</p>
          <p className="dt-muted">{t("devtools.sql.offHow")}</p>
        </div>
      </div>
    );
  }
  if (history.isError) return <ErrorState error={history.error} onRetry={history.refetch} />;
  const pages = history.data.pages;
  return <Workbench settings={pages[0]} executions={pages.flatMap((page) => page.executions)} more={history.hasNextPage ? () => history.fetchNextPage() : undefined} loadingMore={history.isFetchingNextPage} />;
}

interface WorkbenchProps {
  settings: SqlExecutionPage;
  executions: SqlExecutionPage["executions"];
  more?: () => void;
  loadingMore: boolean;
}

/** Editor and result on the left, this session's history on the right; ?run= is the shown execution. */
function Workbench({ settings, executions, more, loadingMore }: WorkbenchProps) {
  const t = useT();
  const time = useTimeFormat();
  const queryClient = useQueryClient();
  const { params, update } = useSearchState();
  const scope = settings.sessionIdentity;
  const [sql, setSql] = useState(() => recallStatements(scope)[params.get("run") ?? ""] ?? "");
  const [mode, setMode] = useState<SqlMode>("read");
  const [dryRun, setDryRun] = useState(false);
  const [confirm, setConfirm] = useState(false);
  const run = params.get("run");
  const statements = recallStatements(scope);
  const writing = settings.writeEnabled && mode === "write";

  const submit = useMutation({
    mutationFn: async () => {
      const identity = newIdentity();
      const text = statementText(sql);
      rememberStatement(scope, identity.operationId, text);
      try {
        return await submitSql({ mode: writing ? "write" : "read", dryRun: writing && dryRun ? true : undefined, statements: [{ sql: text }] }, { executionId: identity.operationId, idempotencyKey: writing ? identity.idempotencyKey : undefined });
      } catch (error) {
        // The request may have landed; its id is ours, so the result pane can still look it up.
        if (error instanceof ApiError && error.code === "network_error") update({ run: identity.operationId });
        throw error;
      }
    },
    onSuccess: (execution) => {
      queryClient.setQueryData(sqlExecutionQuery(execution.id).queryKey, execution);
      // The result pane refreshes the history once this run settles.
      update({ run: execution.id });
    },
    onError: (error) => toast(errorText(t, error), "danger"),
    onSettled: () => setConfirm(false),
  });

  const start = () => {
    if (!statementText(sql) || submit.isPending) return;
    if (writing && !dryRun) setConfirm(true);
    else submit.mutate();
  };

  return (
    <div className="dt-sql">
      <section className="dt-sql-main" aria-label={t("nav.sql")}>
        <div className="dt-editor-wrap" data-write={writing ? "" : undefined}>
          <CodeArea
            label={t("devtools.sql.editor")}
            placeholder="select id, created_at from app.posts limit 50"
            tone={writing ? "warning" : undefined}
            autoFocus
            rows={7}
            value={sql}
            onChange={(event) => setSql(event.target.value)}
            onSubmit={start}
          />
          <div className="toolbar dt-sql-bar">
            {settings.writeEnabled && (
              <Tabs
                label={t("devtools.sql.modeLabel")}
                value={mode}
                onChange={setMode}
                tabs={[
                  { value: "read", label: t("devtools.sql.mode.read") },
                  { value: "write", label: t("devtools.sql.mode.write") },
                ]}
              />
            )}
            {writing && <Checkbox label={t("devtools.sql.dryRunLabel")} checked={dryRun} onChange={(event) => setDryRun(event.target.checked)} />}
            <span className="dt-muted">{settings.writeEnabled ? (writing ? t("devtools.sql.writeHint") : t("devtools.sql.readHint")) : t("devtools.sql.readOnly")}</span>
            <div className="dt-actions">
              <kbd>{keyLabel("mod").join("")} ↵</kbd>
              <Button size="sm" variant={writing ? "danger" : "primary"} icon={RiPlayLine} loading={submit.isPending} disabled={!statementText(sql)} onClick={start}>
                {writing ? t("devtools.sql.runWrite") : t("devtools.sql.run")}
              </Button>
            </div>
          </div>
        </div>
        {run ? <SqlResult key={run} id={run} sql={statements[run]} /> : <EmptyState icon={RiTerminalBoxLine}>{t("devtools.sql.empty")}</EmptyState>}
      </section>
      <aside className="dt-history" aria-label={t("devtools.sql.history")}>
        <h2 className="dt-history-title">{t("devtools.sql.history")}</h2>
        {executions.length === 0 ? (
          <p className="dt-muted">{t("devtools.sql.noHistory")}</p>
        ) : (
          <ul>
            {executions.map((execution) => {
              const text = statements[execution.id];
              return (
                <li key={execution.id}>
                  <button
                    type="button"
                    aria-current={run === execution.id ? "true" : undefined}
                    onClick={() => {
                      if (text !== undefined) setSql(text);
                      update({ run: execution.id });
                    }}
                  >
                    <span className="dt-history-meta">
                      <span className="chip" data-tone={statusTone[execution.status]}>{t(`devtools.sql.status.${execution.status}`)}</span>
                      {execution.mode === "write" && <span className="chip" data-tone="warning">{t("devtools.sql.mode.write")}</span>}
                      <span className="mono dt-muted">{time(execution.createdAt)}</span>
                    </span>
                    <span className={text === undefined ? "dt-history-sql dt-muted" : "dt-history-sql mono"}>{text ?? t("devtools.sql.notStored")}</span>
                  </button>
                </li>
              );
            })}
          </ul>
        )}
        {more && <Button size="sm" variant="secondary" loading={loadingMore} onClick={more}>{t("records.loadMore")}</Button>}
      </aside>
      <ConfirmDialog
        open={confirm}
        title={t("devtools.sql.confirmTitle")}
        message={t("devtools.sql.confirmText")}
        confirmLabel={t("devtools.sql.runWrite")}
        danger
        busy={submit.isPending}
        onConfirm={() => submit.mutate()}
        onCancel={() => setConfirm(false)}
      >
        <pre className="dt-caret mono">{statementText(sql)}</pre>
        {settings.writeTargets.length > 0 && (
          <p className="dt-muted">
            {t("devtools.sql.targets")}{" "}
            <span className="mono">{settings.writeTargets.map((target) => `${target.schema}.${target.table}`).join(", ")}</span>
          </p>
        )}
      </ConfirmDialog>
    </div>
  );
}
