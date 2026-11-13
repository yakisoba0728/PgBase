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
  export interface JobRun { jobId: string; name: string; fenceGeneration: ExactNumber; runKey: string; scheduledAt: string; occurrenceKey: string }
  export interface MigrationStatement { sql: string; args?: JSONValue[] }
  export interface DatabaseStatement extends MigrationStatement { query?: boolean }
  const pgbase: {
    /** Available while the manifest bootstrap module registers predeclared handlers. */
    registration: {
      route(method: string, path: string, handler: (input: RouteRequest) => RouteResponse, ...middleware: Array<(next: Function) => Function>): void
      middleware(...middleware: Array<(next: Function) => Function>): void
      job(name: string, schedule: string, handler: (input: JobRun) => JSONValue | void): void
      hooks: Record<string, (handler: (input: HookInvocation) => HookResult | void, ...tags: string[]) => void>
    }
    schema: {
      export(input?: {}): JSONValue
      preview(input: { document: JSONValue }): { baseVersion: ExactNumber; checksum: string }
      apply(input: { document: JSONValue; baseVersion: ExactNumber | number; checksum: string; operationId?: string; idempotencyKey?: string }): JSONValue
    }
    os: {
      args(input?: {}): string[]
      getenv(input: { name: string }): string
      command(input: { name: string; args?: string[]; input?: string; env?: string[]; dir?: string; mode?: "output" | "combined" }): { output: string; stdout: string; stderr: string; success: boolean; error: string }
      exit(input: { code: number }): { requested: true }
    }
    filesystem: {
      read(input: { path: string }): { dataBase64: string; size: ExactNumber }
      write(input: { path: string; dataBase64: string; mode?: number }): { written: true }
      stat(input: { path: string }): JSONValue
      list(input: { path: string }): Array<{ name: string; isDir: boolean; type: number }>
      mkdir(input: { path: string; mode?: number }): { ok: true }
      rename(input: { oldPath: string; newPath: string }): { ok: true }
      remove(input: { path: string }): { ok: true }
    }
    jobs: { list(input?: {}): JSONValue; configure(input: JSONValue): JSONValue; run(input: { id: string }): JSONValue }
    realtime: { clients(input?: {}): JSONValue; publish(input: JSONValue): JSONValue }
    backups: { list(input?: {}): JSONValue; create(input?: {}): JSONValue; restore(input: { id: string }): JSONValue; read(input: { id: string }): JSONValue; delete(input: { id: string }): JSONValue }
    http: { send(input: { url: string; method?: string; headers?: Record<string,string>; body?: string }): JSONValue }
    app: {
      settings(input?: {}): JSONValue
      reloadSettings(input?: {}): JSONValue
      restart(input?: {}): JSONValue
      transactionInfo(input?: {}): JSONValue
      /** Administrator-only, synchronous, actor-bound transaction. Nested calls share its outcome. */
      transaction<T>(callback: (services: typeof pgbase) => T): T
    }
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
    database: { transaction(input: { statements: DatabaseStatement[] }): { results: Array<{ rowsAffected?: ExactNumber; items?: Array<Record<string, JSONValue>> }> } }
    objects: {
      write(input: { key: string; data: string }): { written: true; size: ExactNumber }
      read(input: { key: string }): { data: string; size: ExactNumber }
      delete(input: { key: string }): { deleted: true }
      list(input: { prefix?: string; cursor?: string; limit?: number }): { objects: Array<{ key:string; size:ExactNumber; updatedAt:string }>; nextCursor?: string }
      copy(input: { sourceKey: string; destinationKey: string }): { copied: true; size: ExactNumber }
    }
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
