import { request } from "./http";

export type SqlMode = "read" | "write";

/** pending/running are in flight; the rest are terminal. */
export type SqlExecutionStatus = "pending" | "running" | "succeeded" | "failed" | "canceled" | "timed_out" | "unknown";

export type SqlParameterKind = "null" | "string" | "boolean" | "integer" | "decimal" | "json" | "bytes" | "uuid" | "timestamp";

export interface SqlParameter {
  kind: SqlParameterKind;
  text?: string;
}

export interface SqlStatementInput {
  sql: string;
  params?: SqlParameter[];
}

export interface SqlSubmitInput {
  mode?: SqlMode;
  dryRun?: boolean;
  /** 1..maximumTimeoutMs; server default when omitted. */
  timeoutMs?: number;
  statements: SqlStatementInput[];
}

export interface SqlSubmitOptions {
  /** Client-chosen execution UUID (sent as PgBase-Operation-ID); server generates one when omitted. */
  executionId?: string;
  idempotencyKey?: string;
}

export type SqlCellKind = "null" | "string" | "boolean" | "integer" | "decimal" | "json" | "bytes" | "temporal";

/** Every non-null value arrives as exact text; `text` is absent only for "null". */
export interface SqlCell {
  kind: SqlCellKind;
  text?: string;
}

export interface SqlColumn {
  name: string;
  typeOid: number;
  typeName: string;
}

export interface SqlStatementResult {
  columns?: SqlColumn[];
  rows?: SqlCell[][];
  command?: string;
  affectedRows: number;
}

export interface SqlResult {
  statements?: SqlStatementResult[];
  rows: number;
  affectedRows: number;
  bytes: number;
  truncated: boolean;
  dryRun: boolean;
  committed?: boolean;
  elapsedMs: number;
}

export interface SqlFailure {
  code: string;
  message: string;
  sqlState?: string;
  statementIndex?: number;
  position?: number;
}

export interface SqlExecution {
  id: string;
  mode: SqlMode;
  dryRun: boolean;
  status: SqlExecutionStatus;
  createdAt: string;
  startedAt?: string;
  finishedAt?: string;
  /** Results are retained until this time. */
  resultUntil?: string;
  result?: SqlResult;
  failure?: SqlFailure;
}

export interface SqlWriteTarget {
  schema: string;
  table: string;
  columns: string[];
}

export interface SqlExecutionPage {
  executions: SqlExecution[];
  nextCursor?: string;
  sessionIdentity: string;
  writeEnabled: boolean;
  writeTargets: SqlWriteTarget[];
  defaultTimeoutMs: number;
  maximumTimeoutMs: number;
}

export async function listSqlExecutions(cursor?: string, signal?: AbortSignal): Promise<SqlExecutionPage> {
  return request<SqlExecutionPage>("GET", "/api/admin/sql/queries", { query: { cursor }, signal });
}

/** Returns immediately (202) with a pending/running execution; poll getSqlExecution. */
export async function submitSql(input: SqlSubmitInput, options: SqlSubmitOptions = {}): Promise<SqlExecution> {
  const headers: Record<string, string> = {};
  if (options.executionId) headers["PgBase-Operation-ID"] = options.executionId;
  if (options.idempotencyKey) headers["Idempotency-Key"] = options.idempotencyKey;
  return request<SqlExecution>("POST", "/api/admin/sql/queries", { body: input, headers });
}

export async function getSqlExecution(id: string, signal?: AbortSignal): Promise<SqlExecution> {
  return request<SqlExecution>("GET", `/api/admin/sql/queries/${encodeURIComponent(id)}`, { signal });
}

export async function cancelSqlExecution(id: string): Promise<SqlExecution> {
  return request<SqlExecution>("POST", `/api/admin/sql/queries/${encodeURIComponent(id)}/cancel`);
}
