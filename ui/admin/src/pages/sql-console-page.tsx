import { useCallback, useEffect, useMemo, useState } from "react";
import {
  sqlConsoleParameterKinds, SQLConsoleError, type SQLConsoleCell, type SQLConsoleExecution, type SQLConsoleParameter,
  type SQLConsoleParameterKind, type SQLConsolePort, type SQLConsoleStatementInput, type SQLConsoleStatementResult, type SQLConsoleSubmitInput,
} from "../backend/sql-console-backend";

const terminal = new Set(["succeeded", "failed", "canceled", "timed_out", "unknown"]);
const statusText: Record<SQLConsoleExecution["status"], string> = {
  pending: "Waiting to run", running: "Running", succeeded: "Finished", failed: "Failed", canceled: "Canceled", timed_out: "Timed out", unknown: "Outcome is unknown",
};
const cellText: Record<SQLConsoleCell["kind"], string> = {
  null: "NULL", string: "text", boolean: "boolean", integer: "integer", decimal: "decimal", json: "JSON", bytes: "bytes", temporal: "time",
};
function resolvedWrite(status: SQLConsoleExecution["status"]): boolean { return terminal.has(status) && status !== "unknown"; }
const legacyPendingWriteStorageKey = "pgbase.sql-console.pending-write/v1";
const pendingWriteStorageKey = (scope: string) => `pgbase.sql-console.pending-write/v2/${encodeURIComponent(scope)}`;
interface PendingWrite { scope: string | undefined; request: SQLConsoleSubmitInput }

function pendingWrite(scope: string | undefined): PendingWrite | null {
  try { window.sessionStorage.removeItem(legacyPendingWriteStorageKey); } catch { /* session storage is unavailable */ }
  if (scope === undefined || scope === "") return null;
  try {
    const raw = window.sessionStorage.getItem(pendingWriteStorageKey(scope));
    if (raw === null) return null;
    const value: unknown = JSON.parse(raw);
    if (value === null || typeof value !== "object" || Array.isArray(value)) throw new TypeError("invalid pending write");
    const attempt = value as Partial<SQLConsoleSubmitInput>;
    if (attempt.mode !== "write" || typeof attempt.operationID !== "string" || typeof attempt.idempotencyKey !== "string" || typeof attempt.dryRun !== "boolean" || !Array.isArray(attempt.statements)) throw new TypeError("invalid pending write");
    return { scope, request: attempt as SQLConsoleSubmitInput };
  } catch {
    try { window.sessionStorage.removeItem(pendingWriteStorageKey(scope)); } catch { /* session storage is unavailable */ }
    return null;
  }
}
function definiteWriteRejection(error: unknown): boolean {
  return error instanceof SQLConsoleError && [400, 403, 409, 429, 503].includes(error.status);
}

interface DraftStatement { sql: string; params: SQLConsoleParameter[] }
const newStatement = (): DraftStatement => ({ sql: "SELECT 1", params: [] });

function copiedText(cell: SQLConsoleCell): string { return cell.kind === "null" ? "NULL" : cell.text ?? ""; }
function executionMessage(error: unknown): string {
  if (error instanceof SQLConsoleError) {
    if (error.code === "forbidden") return "This administrator session is not allowed to run that SQL execution.";
    if (error.code === "busy" || error.code === "unavailable") return "SQL execution is temporarily unavailable. Wait before trying again.";
    if (error.code === "conflict") return "This execution identity was already used for a different request.";
    if (error.code === "invalid_request") return "The SQL execution request was rejected.";
  }
  return "Unable to contact the SQL console.";
}

function ResultTable({ statement, filter, copy }: { statement: SQLConsoleStatementResult; filter: string; copy(value: string): void }) {
  const rows = statement.rows ?? [];
  const shown = filter.trim() === "" ? rows : rows.filter((row) => row.some((cell) => copiedText(cell).toLocaleLowerCase().includes(filter.toLocaleLowerCase())));
  if ((statement.columns?.length ?? 0) === 0) return <p>{statement.command ?? "Command completed"}: {statement.affectedRows} affected rows.</p>;
  return <div className="table-scroll sql-console-results" tabIndex={0}>
    <table>
      <thead><tr>{statement.columns?.map((column, index) => <th key={`${column.name}:${index}`} scope="col">{column.name} <small>{column.typeName}</small></th>)}</tr></thead>
      <tbody>{shown.map((row, rowIndex) => <tr key={rowIndex}>{row.map((cell, cellIndex) => <td key={cellIndex} data-cell-kind={cell.kind}>
        <span>{copiedText(cell)}</span> <small>{cellText[cell.kind]}</small>{" "}<button aria-label={`Copy row ${rowIndex + 1} column ${cellIndex + 1}`} onClick={() => copy(copiedText(cell))} type="button">Copy</button>
      </td>)}</tr>)}</tbody>
    </table>
    {shown.length === 0 && <p>No returned values match this bounded result.</p>}
  </div>;
}

function ExecutionResult({ execution }: { execution: SQLConsoleExecution }) {
  const [selected, setSelected] = useState(0);
  const [filter, setFilter] = useState("");
  const [copyError, setCopyError] = useState<string | null>(null);
  const result = execution.result;
  const copy = useCallback((value: string) => {
    setCopyError(null);
    void navigator.clipboard.writeText(value).catch(() => setCopyError("The value could not be copied. Select it and copy it manually."));
  }, []);
  const committed = result?.committed === true ? "Committed" : result?.committed === false ? "Rolled back" : "Commit state unknown";
  if (result === undefined || result.statements === undefined) {
    const summary = result === undefined ? null : <p>{result.rows} rows, {result.affectedRows} affected rows, {result.bytes} bytes, {result.elapsedMs} ms. {result.dryRun ? "Dry run: no write was committed." : committed}.</p>;
    if (result !== undefined) {
      if (execution.resultUntil !== undefined && Date.parse(execution.resultUntil) <= Date.now()) return <section aria-label="SQL result">{summary}<p>The bounded result expired at {execution.resultUntil}; the durable execution record remains available.</p></section>;
      return <section aria-label="SQL result">{summary}<p>The bounded result is unavailable in this execution response. Recheck this execution while its result TTL remains.</p></section>;
    }
    if (execution.resultUntil !== undefined && Date.parse(execution.resultUntil) <= Date.now()) return <p>The bounded result expired at {execution.resultUntil}; the durable execution record remains available.</p>;
    if (execution.resultUntil !== undefined) return <p>The bounded result is not present in this response. Recheck this execution before {execution.resultUntil}.</p>;
    return null;
  }
  const statements = result.statements ?? [];
  const active = statements[Math.min(selected, Math.max(0, statements.length - 1))];
  return <section aria-label="SQL result">
    <p>{result.rows} rows, {result.affectedRows} affected rows, {result.bytes} bytes, {result.elapsedMs} ms. {result.truncated ? "The server truncated this bounded result." : "The result stayed within the server cap."} {result.dryRun ? "Dry run: no write was committed." : committed}.</p>
    {statements.length > 1 && <div aria-label="Statement results" role="tablist">{statements.map((_, index) => <button aria-selected={selected === index} key={index} onClick={() => setSelected(index)} role="tab" type="button">Statement {index + 1}</button>)}</div>}
    {active !== undefined && <><label>Search this returned statement<input aria-label="Search bounded result" onChange={(event) => setFilter(event.target.value)} value={filter} /></label><ResultTable copy={copy} filter={filter} statement={active} /></>}
    {copyError !== null && <p role="alert">{copyError}</p>}
  </section>;
}

function StatementEditor({ index, statement, update, remove }: { index: number; statement: DraftStatement; update(value: DraftStatement): void; remove(): void }) {
  const replaceParameter = (parameterIndex: number, patch: Partial<SQLConsoleParameter>) => update({ ...statement, params: statement.params.map((parameter, current) => current === parameterIndex ? { ...parameter, ...patch } : parameter) });
  return <fieldset>
    <legend>Statement {index + 1}</legend>
    <label>SQL<textarea aria-label={`SQL statement ${index + 1}`} onChange={(event) => update({ ...statement, sql: event.target.value })} rows={5} value={statement.sql} /></label>
    {statement.params.map((parameter, parameterIndex) => <div key={parameterIndex}>
      <label>Parameter {parameterIndex + 1} kind<select aria-label={`Statement ${index + 1} parameter ${parameterIndex + 1} kind`} onChange={(event) => replaceParameter(parameterIndex, { kind: event.target.value as SQLConsoleParameterKind })} value={parameter.kind}>{sqlConsoleParameterKinds.map((kind) => <option key={kind} value={kind}>{kind}</option>)}</select></label>
      <label>Exact value<input aria-label={`Statement ${index + 1} parameter ${parameterIndex + 1} value`} disabled={parameter.kind === "null"} onChange={(event) => replaceParameter(parameterIndex, { text: event.target.value })} value={parameter.text ?? ""} /></label>
      <button onClick={() => update({ ...statement, params: statement.params.filter((_, current) => current !== parameterIndex) })} type="button">Remove parameter</button>
    </div>)}
    <button onClick={() => update({ ...statement, params: [...statement.params, { kind: "string", text: "" }] })} type="button">Add parameter</button>
    {index > 0 && <button onClick={remove} type="button">Remove statement</button>}
  </fieldset>;
}

export function SQLConsolePage({ port }: { port: SQLConsolePort }) {
  const [statements, setStatements] = useState<DraftStatement[]>([newStatement()]);
  const [mode, setMode] = useState<"read" | "write">("read");
  const [dryRun, setDryRun] = useState(false);
  const [timeoutMS, setTimeoutMS] = useState("");
  const [writeConfirmed, setWriteConfirmed] = useState(false);
  const [history, setHistory] = useState<SQLConsoleExecution[]>([]);
  const [cursor, setCursor] = useState<string | undefined>();
  const [writeEnabled, setWriteEnabled] = useState(false);
  const [defaultTimeoutMS, setDefaultTimeoutMS] = useState<number | undefined>();
  const [maximumTimeoutMS, setMaximumTimeoutMS] = useState<number | undefined>();
  const [targets, setTargets] = useState<Array<{ schema: string; table: string; columns: string[] }>>([]);
  const [sessionIdentity, setSessionIdentity] = useState<string | undefined>();
  const [execution, setExecution] = useState<SQLConsoleExecution | null>(null);
  // A write may have reached the server even when its HTTP response did not.
  // Retain the complete request identity until the server itself settles it.
  const [unresolvedWrite, setUnresolvedWrite] = useState<PendingWrite | null>(null);
  const [submitting, setSubmitting] = useState(false);
  const [canceling, setCanceling] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const activeWrite = unresolvedWrite?.scope === sessionIdentity ? unresolvedWrite : null;
  const retainWrite = useCallback((request: SQLConsoleSubmitInput): PendingWrite => {
    const attempt = { scope: sessionIdentity, request };
    if (sessionIdentity !== undefined && sessionIdentity !== "") try { window.sessionStorage.setItem(pendingWriteStorageKey(sessionIdentity), JSON.stringify(request)); } catch { /* in-memory recovery remains available */ }
    setUnresolvedWrite(attempt);
    return attempt;
  }, [sessionIdentity]);
  const releaseWrite = useCallback((attempt: PendingWrite) => {
    if (attempt.scope !== undefined && attempt.scope !== "") try { window.sessionStorage.removeItem(pendingWriteStorageKey(attempt.scope)); } catch { /* session storage is unavailable */ }
    setUnresolvedWrite((current) => current?.request.operationID === attempt.request.operationID && current.scope === attempt.scope ? null : current); setWriteConfirmed(false);
  }, []);

  const loadHistory = useCallback(async (next?: string) => {
    try {
      const page = await port.history(next);
      setWriteEnabled(page.writeEnabled); setTargets(page.writeTargets); setCursor(page.nextCursor); setDefaultTimeoutMS(page.defaultTimeoutMs); setMaximumTimeoutMS(page.maximumTimeoutMs); setSessionIdentity(page.sessionIdentity);
      setHistory((current) => next === undefined ? page.executions : [...current, ...page.executions]);
    } catch (caught) { setError(executionMessage(caught)); }
  }, [port]);
  useEffect(() => { void loadHistory(); }, [loadHistory]);
  useEffect(() => {
    setUnresolvedWrite((current) => current !== null && current.scope === sessionIdentity ? current : pendingWrite(sessionIdentity));
  }, [sessionIdentity]);
  useEffect(() => {
    if (execution === null || terminal.has(execution.status)) return;
    const controller = new AbortController();
    const timer = window.setTimeout(() => void port.execution(execution.id, controller.signal).then((next) => {
      setExecution(next);
      if (activeWrite?.request.operationID === next.id && resolvedWrite(next.status)) releaseWrite(activeWrite);
    }).catch((caught) => { if (!controller.signal.aborted) setError(executionMessage(caught)); }), 750);
    return () => { controller.abort(); window.clearTimeout(timer); };
  }, [activeWrite, execution, port, releaseWrite]);

  const draft = useMemo<SQLConsoleStatementInput[]>(() => statements.map((statement) => ({ sql: statement.sql, ...(statement.params.length === 0 ? {} : { params: statement.params.map((parameter) => parameter.kind === "null" ? { kind: "null" } : parameter) }) })), [statements]);
  const reconcileWrite = async () => {
    const attempt = activeWrite;
    if (attempt === null) return;
    setSubmitting(true); setError(null);
    try {
      const next = await port.execution(attempt.request.operationID);
      setExecution(next);
      if (resolvedWrite(next.status)) releaseWrite(attempt);
    } catch (caught) {
      if (caught instanceof SQLConsoleError && caught.status === 404 && caught.code === "not_found") {
        try {
          const next = await port.submit(attempt.request);
          setExecution(next);
          if (resolvedWrite(next.status)) releaseWrite(attempt);
        } catch (replayError) {
          if (definiteWriteRejection(replayError)) releaseWrite(attempt);
          setError(definiteWriteRejection(replayError) ? executionMessage(replayError) : "The earlier write might still have run. Recheck it before submitting a new write.");
        }
      } else setError(`The earlier write might still have run. ${executionMessage(caught)} Recheck it before submitting a new write.`);
    } finally { setSubmitting(false); }
  };
  const inspectExecution = async (id: string) => {
    setError(null);
    try {
      const next = await port.execution(id);
      setExecution(next);
      if (activeWrite?.request.operationID === next.id && resolvedWrite(next.status)) releaseWrite(activeWrite);
    } catch (caught) { setError(executionMessage(caught)); }
  };
  const submit = async (event: React.FormEvent) => {
    event.preventDefault(); setError(null);
    if (mode === "write" && activeWrite !== null) { await reconcileWrite(); return; }
    if (mode === "write" && !writeEnabled) { setError("Writes are not configured for this SQL console."); return; }
    if (mode === "write" && !dryRun && !writeConfirmed) { setError("Confirm the transactional write before executing it."); return; }
    const timeout = timeoutMS.trim() === "" ? undefined : Number(timeoutMS);
    if (timeout !== undefined && (!Number.isInteger(timeout) || timeout < 1 || (maximumTimeoutMS !== undefined && timeout > maximumTimeoutMS))) { setError(`Timeout must be a whole number from 1 to ${maximumTimeoutMS ?? "the server maximum"} ms.`); return; }
    setSubmitting(true);
    let writeAttempt: PendingWrite | null = null;
    try {
      const operationID = crypto.randomUUID();
      const idempotencyKey = mode === "write" ? crypto.randomUUID() : undefined;
      const request = { operationID, idempotencyKey, mode, dryRun: mode === "write" && dryRun, ...(timeout === undefined ? {} : { timeoutMS: timeout }), statements: draft } as SQLConsoleSubmitInput;
      writeAttempt = mode === "write" ? retainWrite(request) : null;
      const next = await port.submit(request);
      setExecution(next); setHistory((current) => [next, ...current.filter((item) => item.id !== next.id)]); setWriteConfirmed(false);
      if (writeAttempt !== null && resolvedWrite(next.status)) releaseWrite(writeAttempt);
    } catch (caught) {
      if (mode === "write" && definiteWriteRejection(caught)) { if (writeAttempt !== null) releaseWrite(writeAttempt); setError(executionMessage(caught)); }
      else setError(mode === "write" ? "The write response was lost or unavailable. Recheck this execution before submitting a new write." : executionMessage(caught));
    } finally { setSubmitting(false); }
  };
  const cancel = async () => {
    if (execution === null || terminal.has(execution.status)) return;
    setCanceling(true); setError(null);
    try { setExecution(await port.cancel(execution.id)); } catch (caught) { setError(executionMessage(caught)); } finally { setCanceling(false); }
  };

  return <section>
    <h2>SQL console</h2>
    <p>Queries run in read-only mode by default. Results are bounded and kept briefly; durable history never includes SQL, parameters, or returned values.</p>
    {error !== null && <p role="alert">{error}</p>}
    <form onSubmit={(event) => void submit(event)}>
      <fieldset><legend>Execution mode</legend>
        <label><input checked={mode === "read"} name="sql-mode" onChange={() => { setMode("read"); setStatements((current) => [current[0] ?? newStatement()]); setDryRun(false); setWriteConfirmed(false); }} type="radio" />Read only</label>
        <label><input checked={mode === "write"} disabled={!writeEnabled} name="sql-mode" onChange={() => setMode("write")} type="radio" />Transactional write</label>
        {mode === "write" && <><label><input checked={dryRun} onChange={(event) => { setDryRun(event.target.checked); setWriteConfirmed(false); }} type="checkbox" />Dry run and roll back</label>{!dryRun && <label><input checked={writeConfirmed} onChange={(event) => setWriteConfirmed(event.target.checked)} type="checkbox" />I confirm this allowed write transaction should execute once</label>}</>}
      </fieldset>
      <label>Timeout in milliseconds<input aria-label="Timeout in milliseconds" inputMode="numeric" onChange={(event) => setTimeoutMS(event.target.value)} placeholder={defaultTimeoutMS === undefined ? "Server default" : `Server default: ${defaultTimeoutMS} ms`} value={timeoutMS} /></label>{defaultTimeoutMS !== undefined && <p>Server default: {defaultTimeoutMS} ms{maximumTimeoutMS === undefined ? "" : `; maximum: ${maximumTimeoutMS} ms`}.</p>}
      {statements.map((statement, index) => <StatementEditor index={index} key={index} remove={() => setStatements((current) => current.filter((_, currentIndex) => currentIndex !== index))} statement={statement} update={(value) => setStatements((current) => current.map((item, currentIndex) => currentIndex === index ? value : item))} />)}
      {mode === "write" && statements.length < 10 && <button onClick={() => setStatements((current) => [...current, newStatement()])} type="button">Add statement</button>}
      <button disabled={submitting} type="submit">{submitting ? "Submitting…" : mode === "write" && activeWrite !== null ? "Recheck write execution" : mode === "write" && dryRun ? "Dry run write" : mode === "write" ? "Execute write" : "Run read query"}</button>
    </form>
    <section aria-label="Allowed write targets"><h3>Allowed write targets</h3>{!writeEnabled && <p>No extension write target is configured.</p>}<ul>{targets.map((target) => <li key={`${target.schema}.${target.table}`}><code>{target.schema}.{target.table}</code> — {target.columns.join(", ")}</li>)}</ul></section>
    {execution !== null && <section aria-live="polite"><h3>Execution</h3><p><strong>{statusText[execution.status]}</strong> — {execution.id}</p>{!terminal.has(execution.status) && <button disabled={canceling} onClick={() => void cancel()} type="button">{canceling ? "Canceling…" : "Cancel execution"}</button>}{execution.status === "unknown" && <><p>The server could not determine the final commit state. Recheck this execution before deciding whether any write can be retried.</p><button onClick={() => void inspectExecution(execution.id)} type="button">Recheck execution</button></>}{execution.failure !== undefined && <p role="alert">{execution.failure.message}{execution.failure.statementIndex !== undefined ? ` Statement ${execution.failure.statementIndex + 1}.` : ""}{execution.failure.position !== undefined && execution.failure.position > 0 ? ` Character ${execution.failure.position}.` : ""}{execution.failure.sqlState !== undefined ? ` SQLSTATE ${execution.failure.sqlState}.` : ""}</p>}<ExecutionResult execution={execution} /></section>}
    <section aria-label="SQL execution history"><h3>Execution history</h3>{history.length === 0 && <p>No SQL executions are recorded for this administrator session.</p>}<ol>{history.map((item) => <li key={item.id}><button onClick={() => void inspectExecution(item.id)} type="button">Inspect</button> {statusText[item.status]} <time dateTime={item.createdAt}>{item.createdAt}</time></li>)}</ol>{cursor !== undefined && <button onClick={() => void loadHistory(cursor)} type="button">Next history page</button>}</section>
  </section>;
}
