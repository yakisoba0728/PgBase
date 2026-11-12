import { useState } from "react";
import { useInfiniteQuery, useMutation, useQueryClient } from "@tanstack/react-query";
import { keyLabel } from "../app/keys";
import { RiPlayLine, RiTerminalBoxLine } from "@remixicon/react";
import { ApiError } from "../api/http";
import { submitSql, type SqlExecutionPage, type SqlMode, type SqlSubmitInput } from "../api/sql";
import { errorText } from "../app/queries";
import { Button } from "../components/Button";
import { ConfirmDialog } from "../components/Dialog";
import { EmptyState, ErrorState, Loader, Notice } from "../components/Feedback";
import { CodeArea } from "../components/CodeArea";
import { Checkbox } from "../components/Field";
import { Tabs } from "../components/Tabs";
import { toast } from "../components/Toast";
import { useT } from "../i18n";
import { useTimeFormat } from "../app/format";
import { useSearchState } from "../app/search";
import { newIdentity } from "../schema/apply";
import { mayHaveRun, recallStatements, rememberStatement, statementText } from "./model";
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

interface SqlAttempt {
  text: string;
  identity: ReturnType<typeof newIdentity>;
  input: SqlSubmitInput;
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

  // A write whose response was lost: resending it unchanged (same execution id, idempotency key and body)
  // makes the server replay the stored outcome instead of running it again. Kept for this page view only, and
  // cleared only by that write's own result or a confirmed "set aside": a resend refused as busy or unavailable
  // says nothing about the first attempt.
  const [lostWrite, setLostWrite] = useState<SqlAttempt | null>(null);
  const [setAside, setSetAside] = useState(false);
  const newAttempt = (): SqlAttempt => {
    const text = statementText(sql);
    return { text, identity: newIdentity(), input: { mode: writing ? "write" : "read", dryRun: writing && dryRun ? true : undefined, statements: [{ sql: text }] } };
  };

  const submit = useMutation({
    mutationFn: async (attempt: SqlAttempt) => {
      const { identity, input } = attempt;
      rememberStatement(scope, identity.operationId, attempt.text);
      try {
        return await submitSql(input, { executionId: identity.operationId, idempotencyKey: input.mode === "write" ? identity.idempotencyKey : undefined });
      } catch (error) {
        if (mayHaveRun(error)) {
          // The request may have landed; its id is ours, so the result pane can still look it up.
          update({ run: identity.operationId });
          if (input.mode === "write") setLostWrite(attempt);
        }
        throw error;
      }
    },
    onSuccess: (execution, attempt) => {
      setLostWrite((lost) => (lost?.identity.operationId === attempt.identity.operationId ? null : lost));
      queryClient.setQueryData(sqlExecutionQuery(execution.id).queryKey, execution);
      // The result pane refreshes the history once this run settles.
      update({ run: execution.id });
    },
    onError: (error) => toast(errorText(t, error), "danger"),
    onSettled: () => setConfirm(false),
  });

  // Another write waits until the lost one is resent or set aside, so it cannot run twice by accident.
  const blocked = writing && lostWrite !== null;
  const start = () => {
    if (!statementText(sql) || submit.isPending || blocked) return;
    if (writing && !dryRun) setConfirm(true);
    else submit.mutate(newAttempt());
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
              <Button size="sm" variant={writing ? "danger" : "primary"} icon={RiPlayLine} loading={submit.isPending} disabled={!statementText(sql) || blocked} onClick={start}>
                {writing ? t("devtools.sql.runWrite") : t("devtools.sql.run")}
              </Button>
            </div>
          </div>
        </div>
        {lostWrite && (
          <Notice tone="danger" title={t("devtools.sql.lostTitle")} actions={<>
            <Button size="sm" variant="secondary" loading={submit.isPending} onClick={() => submit.mutate(lostWrite)}>{t("devtools.sql.resend")}</Button>
            <Button size="sm" variant="transparent" disabled={submit.isPending} onClick={() => setSetAside(true)}>{t("devtools.sql.forget")}</Button>
          </>}>
            <p>{t("devtools.sql.lostText")}</p>
            <pre className="dt-caret mono">{lostWrite.text}</pre>
          </Notice>
        )}
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
        {more && <Button size="sm" variant="secondary" loading={loadingMore} onClick={more}>{t("common.loadMore")}</Button>}
      </aside>
      <ConfirmDialog
        open={setAside}
        title={t("devtools.sql.forgetTitle")}
        message={t("devtools.sql.forgetText")}
        confirmLabel={t("devtools.sql.forget")}
        danger
        onConfirm={() => {
          setLostWrite(null);
          setSetAside(false);
        }}
        onCancel={() => setSetAside(false)}
      />
      <ConfirmDialog
        open={confirm}
        title={t("devtools.sql.confirmTitle")}
        message={t("devtools.sql.confirmText")}
        confirmLabel={t("devtools.sql.runWrite")}
        danger
        busy={submit.isPending}
        onConfirm={() => submit.mutate(newAttempt())}
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
