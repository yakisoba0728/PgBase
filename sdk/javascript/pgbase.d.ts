declare module "pgbase" {
  export interface ExactNumber { readonly __pgbaseExact: never; toString(): string }
  export type JSONScalar = null | boolean | string | number | ExactNumber
  export type JSONValue = JSONScalar | JSONValue[] | { [name: string]: JSONValue }
  export interface RecordInput { [name: string]: JSONValue }
  export interface RecordResult {
    data?: { [name: string]: JSONValue }
    etag?: string
    version?: ExactNumber
    eventId?: string
    replayed?: boolean
    compatibility?: string
  }
  export interface RecordPage { items: Array<{ [name: string]: JSONValue }>; nextCursor?: string }
  export interface BatchOperation {
    operation: "create" | "update" | "upsert" | "delete"
    collection: string
    id?: string
    input?: RecordInput
    idempotencyKey?: string
    ifMatch?: string
  }
  export interface FileResult {
    id?: string
    recordId?: string
    field?: string
    filename?: string
    contentType?: string
    size?: ExactNumber
    sha256?: string
    token?: string
    dataBase64?: string
    handle?: string
    eof?: boolean
    closed?: boolean
    deleted?: boolean
    items?: FileResult[]
  }
  export interface RouteRequest {
    method: string
    path: string
    pathValues: Record<string, string>
    query: Record<string, string[]>
    headers: Record<string, string>
    principal?: { kind: "user" | "administrator" | "service"; id: string; authCollectionId?: string }
    json?: JSONValue
    body?: string
  }
  export interface RouteResponse {
    status: number
    headers?: Record<string, string>
    json?: JSONValue
    body?: string
  }
  export interface HookInvocation {
    event: string
    phase: "before" | "after"
    data: { [name: string]: JSONValue }
    failed: boolean
    state?: JSONValue
  }
  export interface HookResult { patch?: { [name: string]: JSONValue }; state?: JSONValue }
  export interface JobRun { jobId: string; name: string; fenceGeneration: ExactNumber; runKey: string }
  export interface MigrationStatement { sql: string; args?: JSONValue[] }
  const pgbase: {
    exact(value: string | number): ExactNumber
    records: {
      list(input: { collection: string; cursor?: string; limit?: number; filter?: string; sort?: string; expand?: string }): RecordPage
      get(input: { collection: string; id: string; expand?: string }): RecordResult
      create(input: { collection: string; input: RecordInput; idempotencyKey: string }): RecordResult
      update(input: { collection: string; id: string; input: RecordInput; ifMatch: string }): RecordResult
      delete(input: { collection: string; id: string; ifMatch: string }): RecordResult
      batch(input: { operations: BatchOperation[] }): { results: RecordResult[] }
    }
    mail: { send(input: { to: string; subject: string; text: string; html?: string }): { sent: true } }
    files: {
      list(input: { collection: string; recordId: string; field: string }): FileResult
      /** Fails with file_stream_active until an open handle is closed. */
      upload(input: { collection: string; recordId: string; field: string; filename: string; contentType: string; dataBase64: string; sha256?: string }): FileResult
      delete(input: { fileId: string }): { deleted: true }
      token(input: { fileId: string; variant?: "original" | "thumbnail" }): { id: string; token: string }
      /** Fails with file_stream_active until an open handle is closed. */
      read(input: { fileId: string; variant?: "original" | "thumbnail"; token?: string }): FileResult
      /** Only one active file stream is allowed per invocation. */
      open(input: { fileId: string; variant?: "original" | "thumbnail"; token?: string }): FileResult & { handle: string }
      readChunk(input: { handle: string; maximumBytes?: number }): FileResult
      close(input: { handle: string }): { closed: true }
    }
    migration: {
      exec(input: MigrationStatement): { rowsAffected: ExactNumber }
      query(input: MigrationStatement): { rows: Array<Record<string, JSONValue>> }
      applySnapshot(input: { before: JSONValue; after: JSONValue }): { applied: true }
    }
    log: {
      debug(input: { message: string; fields?: Record<string, string> }): { logged: true }
      info(input: { message: string; fields?: Record<string, string> }): { logged: true }
      warn(input: { message: string; fields?: Record<string, string> }): { logged: true }
      error(input: { message: string; fields?: Record<string, string> }): { logged: true }
    }
  }
  export = pgbase
}
