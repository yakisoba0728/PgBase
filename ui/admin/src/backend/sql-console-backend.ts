export const sqlConsoleModes = ["read", "write"] as const;
export type SQLConsoleMode = typeof sqlConsoleModes[number];
export const sqlConsoleStatuses = ["pending", "running", "succeeded", "failed", "canceled", "timed_out", "unknown"] as const;
export type SQLConsoleStatus = typeof sqlConsoleStatuses[number];
export const sqlConsoleParameterKinds = ["null", "string", "boolean", "integer", "decimal", "json", "bytes", "uuid", "timestamp"] as const;
export type SQLConsoleParameterKind = typeof sqlConsoleParameterKinds[number];
export const sqlConsoleCellKinds = ["null", "string", "boolean", "integer", "decimal", "json", "bytes", "temporal"] as const;
export type SQLConsoleCellKind = typeof sqlConsoleCellKinds[number];

export interface SQLConsoleParameter { kind: SQLConsoleParameterKind; text?: string }
export interface SQLConsoleStatementInput { sql: string; params?: SQLConsoleParameter[] }
export interface SQLConsoleSubmitInput {
  operationID: string;
  idempotencyKey?: string;
  mode: SQLConsoleMode;
  dryRun: boolean;
  timeoutMS?: number;
  statements: SQLConsoleStatementInput[];
}
export interface SQLConsoleCell { kind: SQLConsoleCellKind; text?: string }
export interface SQLConsoleColumn { name: string; typeOid: number; typeName: string }
export interface SQLConsoleStatementResult { columns?: SQLConsoleColumn[]; rows?: SQLConsoleCell[][]; command?: string; affectedRows: number }
export interface SQLConsoleResult {
  statements?: SQLConsoleStatementResult[];
  rows: number;
  affectedRows: number;
  bytes: number;
  truncated: boolean;
  dryRun: boolean;
  committed?: boolean;
  elapsedMs: number;
}
export interface SQLConsoleFailure { code: string; message: string; sqlState?: string; statementIndex?: number; position?: number }
export interface SQLConsoleExecution {
  id: string;
  mode: SQLConsoleMode;
  dryRun: boolean;
  status: SQLConsoleStatus;
  createdAt: string;
  startedAt?: string;
  finishedAt?: string;
  resultUntil?: string;
  result?: SQLConsoleResult;
  failure?: SQLConsoleFailure;
}
export interface SQLConsoleWriteTarget { schema: string; table: string; columns: string[] }
export interface SQLConsoleHistory { executions: SQLConsoleExecution[]; nextCursor?: string; writeEnabled: boolean; writeTargets: SQLConsoleWriteTarget[] }

export interface SQLConsoleTransportRequest { path: string; method: "GET" | "POST"; headers?: Record<string, string>; body?: unknown; signal?: AbortSignal }
export interface SQLConsoleTransportResponse { status: number; body: unknown }
/** The shared authenticated transport owns cookies and CSRF headers; this module only owns N8's wire shape. */
export interface SQLConsoleTransport { request(request: SQLConsoleTransportRequest): Promise<SQLConsoleTransportResponse> }

export class SQLConsoleError extends Error {
  constructor(readonly status: number, readonly code: string) { super(code); this.name = "SQLConsoleError"; }
}

export interface SQLConsolePort {
  submit(input: SQLConsoleSubmitInput, signal?: AbortSignal): Promise<SQLConsoleExecution>;
  execution(id: string, signal?: AbortSignal): Promise<SQLConsoleExecution>;
  cancel(id: string, signal?: AbortSignal): Promise<SQLConsoleExecution>;
  history(cursor?: string, signal?: AbortSignal): Promise<SQLConsoleHistory>;
}

function object(value: unknown): Record<string, unknown> {
  if (value === null || typeof value !== "object" || Array.isArray(value)) throw new SQLConsoleError(502, "invalid_response");
  return value as Record<string, unknown>;
}
function string(value: unknown): string { if (typeof value !== "string") throw new SQLConsoleError(502, "invalid_response"); return value; }
function number(value: unknown): number { if (typeof value !== "number" || !Number.isSafeInteger(value)) throw new SQLConsoleError(502, "invalid_response"); return value; }
function boolean(value: unknown): boolean { if (typeof value !== "boolean") throw new SQLConsoleError(502, "invalid_response"); return value; }
function optionalString(value: unknown): string | undefined { return value === undefined ? undefined : string(value); }

function oneOf<T extends readonly string[]>(value: unknown, allowed: T): T[number] {
  const candidate = string(value);
  if (!allowed.includes(candidate)) throw new SQLConsoleError(502, "invalid_response");
  return candidate as T[number];
}
function cell(value: unknown): SQLConsoleCell {
  const decoded = object(value);
  const kind = oneOf(decoded.kind, sqlConsoleCellKinds);
  const text = optionalString(decoded.text);
  if ((kind === "null" && text !== undefined) || (kind !== "null" && text === undefined)) throw new SQLConsoleError(502, "invalid_response");
  return { kind, ...(text === undefined ? {} : { text }) };
}
function execution(value: unknown): SQLConsoleExecution {
  const decoded = object(value);
  const decodeStatement = (item: unknown): SQLConsoleStatementResult => {
    const statement = object(item);
    const columns = statement.columns === undefined ? undefined : (Array.isArray(statement.columns) ? statement.columns.map((column) => {
      const value = object(column); return { name: string(value.name), typeOid: number(value.typeOid), typeName: string(value.typeName) };
    }) : (() => { throw new SQLConsoleError(502, "invalid_response"); })());
    const rows = statement.rows === undefined ? undefined : (Array.isArray(statement.rows) ? statement.rows.map((row) => {
      if (!Array.isArray(row)) throw new SQLConsoleError(502, "invalid_response"); return row.map(cell);
    }) : (() => { throw new SQLConsoleError(502, "invalid_response"); })());
    return { ...(columns === undefined ? {} : { columns }), ...(rows === undefined ? {} : { rows }), ...(statement.command === undefined ? {} : { command: string(statement.command) }), affectedRows: number(statement.affectedRows) };
  };
  const result = decoded.result === undefined ? undefined : (() => {
    const value = object(decoded.result);
    const statements = value.statements === undefined ? undefined : (Array.isArray(value.statements) ? value.statements.map(decodeStatement) : (() => { throw new SQLConsoleError(502, "invalid_response"); })());
    return { ...(statements === undefined ? {} : { statements }), rows: number(value.rows), affectedRows: number(value.affectedRows), bytes: number(value.bytes), truncated: boolean(value.truncated), dryRun: boolean(value.dryRun), ...(value.committed === undefined ? {} : { committed: boolean(value.committed) }), elapsedMs: number(value.elapsedMs) };
  })();
  const failure = decoded.failure === undefined ? undefined : (() => {
    const value = object(decoded.failure);
    return { code: string(value.code), message: string(value.message), ...(value.sqlState === undefined ? {} : { sqlState: string(value.sqlState) }), ...(value.statementIndex === undefined ? {} : { statementIndex: number(value.statementIndex) }), ...(value.position === undefined ? {} : { position: number(value.position) }) };
  })();
  return { id: string(decoded.id), mode: oneOf(decoded.mode, sqlConsoleModes), dryRun: boolean(decoded.dryRun), status: oneOf(decoded.status, sqlConsoleStatuses), createdAt: string(decoded.createdAt), ...(decoded.startedAt === undefined ? {} : { startedAt: string(decoded.startedAt) }), ...(decoded.finishedAt === undefined ? {} : { finishedAt: string(decoded.finishedAt) }), ...(decoded.resultUntil === undefined ? {} : { resultUntil: string(decoded.resultUntil) }), ...(result === undefined ? {} : { result }), ...(failure === undefined ? {} : { failure }) };
}

function error(response: SQLConsoleTransportResponse): never {
  const envelope = object(object(response.body).error);
  throw new SQLConsoleError(response.status, string(envelope.code));
}
async function send(transport: SQLConsoleTransport, request: SQLConsoleTransportRequest): Promise<unknown> {
  const response = await transport.request(request);
  if (response.status < 200 || response.status >= 300) error(response);
  return response.body;
}

export function createSQLConsoleBackend(transport: SQLConsoleTransport): SQLConsolePort {
  return {
    submit: async (input, signal) => execution(await send(transport, {
      path: "/api/admin/sql/queries", method: "POST", signal,
      headers: { "PgBase-Operation-ID": input.operationID, ...(input.idempotencyKey === undefined ? {} : { "Idempotency-Key": input.idempotencyKey }) },
      body: { mode: input.mode, dryRun: input.dryRun, ...(input.timeoutMS === undefined ? {} : { timeoutMs: input.timeoutMS }), statements: input.statements },
    })),
    execution: async (id, signal) => execution(await send(transport, { path: `/api/admin/sql/queries/${encodeURIComponent(id)}`, method: "GET", signal })),
    cancel: async (id, signal) => execution(await send(transport, { path: `/api/admin/sql/queries/${encodeURIComponent(id)}/cancel`, method: "POST", signal })),
    history: async (cursor, signal) => {
      const query = cursor === undefined ? "" : `?cursor=${encodeURIComponent(cursor)}`;
      const decoded = object(await send(transport, { path: `/api/admin/sql/queries${query}`, method: "GET", signal }));
      if (!Array.isArray(decoded.executions) || !Array.isArray(decoded.writeTargets)) throw new SQLConsoleError(502, "invalid_response");
      return {
        executions: decoded.executions.map(execution), nextCursor: optionalString(decoded.nextCursor), writeEnabled: boolean(decoded.writeEnabled),
        writeTargets: decoded.writeTargets.map((target) => { const value = object(target); if (!Array.isArray(value.columns)) throw new SQLConsoleError(502, "invalid_response"); return { schema: string(value.schema), table: string(value.table), columns: value.columns.map(string) }; }),
      };
    },
  };
}
